// Package settings keeps what an admin can change in the page, in the app's
// database. A setting's value is what the database holds; without a row, the
// environment variable it came from, then its default. The first start after
// this existed copies the environment over once (Import), so nothing has to
// be typed in.
//
// Secrets are encrypted with AES-256-GCM under SETTINGS_KEY, which is not in
// the data folder, so a backup alone can't reveal them. Nothing but the
// server's own use ever reads one back: the page learns only whether it is set.
package settings

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"eumetria/internal/db"
	"eumetria/internal/review"
	"eumetria/internal/seal"
)

// MinInterval is the shortest time between syncs: Yazio's API is private,
// don't hammer it.
const MinInterval = 5 * time.Minute

const secretPrefix = seal.Prefix

var (
	ErrNoKey   = errors.New("SETTINGS_KEY is not set, so secrets can't be stored")
	ErrUnknown = errors.New("no such setting")
)

// BadValue says which setting a value was refused for.
type BadValue struct{ Key string }

func (e BadValue) Error() string { return "bad value for " + e.Key }

type def struct {
	key, env, fallback string
	secret             bool
	atLeast            int // a secret's shortest length
	valid              func(string) bool
	// boundTo, for a secret: the settings that say where it is sent. A
	// change to one of them clears the secret unless the same save sets
	// it again (pentest re-test N4): else whoever may change the settings
	// could point it to their own server and read it there.
	boundTo []string
}

var defs = []def{
	{key: "title", env: "STATS_TITLE", fallback: "Eumetria", valid: text(64, false)},
	{key: "language", valid: oneOf("", "en", "de")},
	{key: "timezone", valid: zone},
	{key: "public_url", env: "PUBLIC_URL", valid: publicURL},
	{key: "sync_interval", env: "SYNC_INTERVAL", fallback: "30m", valid: interval},
	{key: "refresh_days", env: "YAZIO_REFRESH_DAYS", fallback: "3", valid: number(1, 60)},
	{key: "since", env: "YAZIO_SINCE", valid: day},
	{key: "yazio_email", env: "YAZIO_EMAIL", valid: email},
	{key: "yazio_password", env: "YAZIO_PASSWORD", secret: true},
	{key: "yazio_client_id", env: "YAZIO_CLIENT_ID", valid: text(256, true)},
	{key: "yazio_client_secret", env: "YAZIO_CLIENT_SECRET", secret: true},
	{key: "google_client_id", env: "GOOGLE_CLIENT_ID", valid: text(256, true)},
	{key: "google_client_secret", env: "GOOGLE_CLIENT_SECRET", secret: true},
	{key: "review", fallback: "true", valid: boolean},
	{key: "anthropic_key", env: "ANTHROPIC_API_KEY", secret: true},
	{key: "ai_model", env: "AI_MODEL", fallback: review.DefaultModel, valid: text(64, false)},
	{key: "ai_effort", env: "AI_EFFORT", fallback: "medium", valid: oneOf("low", "medium", "high", "xhigh", "max")},
	{key: "ai_credit_usd", env: "AI_CREDIT_USD", valid: money},
	{key: "openfoodfacts", env: "OPENFOODFACTS", fallback: "true", valid: boolean},
	{key: "sleep_goal", fallback: "480", valid: number(240, 720)},
	{key: "backups", fallback: "true", valid: boolean},
	{key: "backup_time", fallback: "03:30", valid: clock},
	{key: "backup_keep_days", fallback: "14", valid: number(1, 365)},
	{key: "backup_passphrase", secret: true, atLeast: 12},
	{key: "backup_passphrase_at", valid: never},
	{key: "backup_drive", fallback: "false", valid: boolean},
	{key: "backup_folder", fallback: "Eumetria backups", valid: text(100, false)},
	{key: "backup_drive_keep_days", fallback: "30", valid: number(1, 3650)},
	// E-mail (optional): security alerts and links (internal/mail).
	{key: "smtp_host", env: "SMTP_HOST", valid: host},
	{key: "smtp_port", env: "SMTP_PORT", fallback: "587", valid: number(1, 65535)},
	{key: "smtp_tls", env: "SMTP_TLS", fallback: "starttls", valid: oneOf("starttls", "implicit")},
	{key: "smtp_user", env: "SMTP_USER", valid: text(256, true)},
	{key: "smtp_password", env: "SMTP_PASSWORD", secret: true, boundTo: []string{"smtp_host", "smtp_port", "smtp_tls", "smtp_user"}},
	{key: "smtp_from", env: "SMTP_FROM", valid: email},
}

func find(key string) *def {
	for i := range defs {
		if defs[i].key == key {
			return &defs[i]
		}
	}
	return nil
}

/* ---------- what a value may be ---------- */

func text(most int, empty bool) func(string) bool {
	return func(s string) bool {
		return (empty || strings.TrimSpace(s) != "") && len(s) <= most && !strings.ContainsFunc(s, unicode.IsControl)
	}
}

func oneOf(list ...string) func(string) bool {
	return func(s string) bool { return slices.Contains(list, s) }
}

func number(lo, hi int) func(string) bool {
	return func(s string) bool {
		n, err := strconv.Atoi(s)
		return err == nil && n >= lo && n <= hi
	}
}

func zone(s string) bool {
	_, err := time.LoadLocation(s)
	return err == nil && s != "" && s != "Local"
}

// host is a mail server's name or address, or empty.
func host(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 253 || strings.ContainsAny(s, " /:@") || strings.ContainsFunc(s, unicode.IsControl) {
		return false
	}
	u, err := url.Parse("smtp://" + s)
	return err == nil && u.Hostname() == s
}

// publicURL is the site's address as people open it: https (http only for
// localhost), a host, nothing after it. Empty: taken from each request.
func publicURL(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(s) > 256 {
		return false
	}
	h := u.Hostname()
	local := h == "localhost" || strings.HasSuffix(h, ".localhost")
	return u.Scheme == "https" || u.Scheme == "http" && local
}

func interval(s string) bool {
	d, err := time.ParseDuration(s)
	return err == nil && d >= MinInterval && d <= 7*24*time.Hour
}

func day(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func email(s string) bool {
	if s == "" {
		return true
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && len(s) <= 254
}

func boolean(s string) bool {
	_, err := strconv.ParseBool(s)
	return err == nil
}

func clock(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil && len(s) == 5
}

// never is for what the server sets itself.
func never(string) bool { return false }

func money(s string) bool {
	if s == "" {
		return true
	}
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && f >= 0 && f < 1e6
}

/* ---------- the store ---------- */

// Store reads and writes the settings. box is nil without SETTINGS_KEY.
type Store struct {
	DB  *db.DB
	box *seal.Box
}

// New takes SETTINGS_KEY as openssl rand -hex 32 makes it; empty is allowed,
// and then secrets stay in the environment. A malformed key is an error: it
// would fail later, when a secret is saved or read.
func New(d *db.DB, hexKey string) (*Store, error) {
	box, err := seal.FromHex(hexKey)
	if err != nil {
		return nil, err
	}
	return &Store{DB: d, box: box}, nil
}

// CanEncrypt reports whether secrets can be stored.
func (s *Store) CanEncrypt() bool { return s.box != nil }

// Box is what seals secrets with SETTINGS_KEY, nil without one; the
// accounts use it for the authenticator apps' secrets.
func (s *Store) Box() *seal.Box { return s.box }

// seal encrypts a secret. The key name is authenticated with it, so a value
// can't be moved to another row.
func (s *Store) seal(key, plain string) (string, error) {
	if s.box == nil {
		return "", ErrNoKey
	}
	return s.box.Seal(plain, key)
}

func (s *Store) open(key, stored string) (string, error) {
	if s.box == nil {
		return "", ErrNoKey
	}
	return s.box.Open(stored, key)
}

func (s *Store) rows(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

const upsert = `INSERT INTO settings (key, value) VALUES (?, ?)
	ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`

// Import copies the environment into the database, once for the plain values
// and once for the secrets, which may have to wait for SETTINGS_KEY. It
// returns how many it copied. A value the database already has stays.
func (s *Store) Import(ctx context.Context) (int, error) {
	have, err := s.rows(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	copied := 0
	step := func(mark string, secret bool) error {
		if _, done := have[mark]; done {
			return nil
		}
		for _, d := range defs {
			v := os.Getenv(d.env)
			if d.secret != secret || d.env == "" || v == "" {
				continue
			}
			if _, set := have[d.key]; set {
				continue
			}
			if !secret && d.valid != nil && !d.valid(v) {
				log.Printf("settings: %s isn't valid, leaving it out", d.env)
				continue
			}
			if secret {
				if v, err = s.seal(d.key, v); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, upsert, d.key, v); err != nil {
				return err
			}
			copied++
		}
		_, err := tx.ExecContext(ctx, upsert, mark, "1")
		return err
	}
	if err := step("env_imported", false); err != nil {
		return 0, err
	}
	if s.CanEncrypt() {
		if err := step("env_secrets_imported", true); err != nil {
			return 0, err
		}
	}
	return copied, tx.Commit()
}

// Save writes values and resets the keys in reset, all or nothing. Resetting
// a plain setting removes its row, so the environment or the default applies
// again. A secret stays gone: an empty row hides what the environment has.
func (s *Store) Save(ctx context.Context, values map[string]string, reset []string) error {
	_, err := s.SaveReport(ctx, values, reset)
	return err
}

// SaveReport is Save that also returns the secrets it cleared because a
// setting they are bound to changed (def.boundTo).
func (s *Store) SaveReport(ctx context.Context, values map[string]string, reset []string) (cleared []string, err error) {
	have, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	now := func(key string) string {
		if v, ok := have[key]; ok {
			return v
		}
		return fromEnv(find(key))
	}
	for _, d := range defs {
		if len(d.boundTo) == 0 || slices.Contains(reset, d.key) {
			continue
		}
		if _, set := values[d.key]; set {
			continue
		}
		// Nothing to clear: no row, and nothing from the environment.
		if stored, ok := have[d.key]; ok && stored == "" || !ok && fromEnv(&d) == "" {
			continue
		}
		for _, k := range d.boundTo {
			v, set := values[k]
			if set && strings.TrimSpace(v) != strings.TrimSpace(now(k)) ||
				slices.Contains(reset, k) && now(k) != fromEnv(find(k)) {
				cleared = append(cleared, d.key)
				break
			}
		}
	}
	if err := s.save(ctx, values, append(slices.Clone(reset), cleared...)); err != nil {
		return nil, err
	}
	return cleared, nil
}

func (s *Store) save(ctx context.Context, values map[string]string, reset []string) error {
	type row struct{ key, value string }
	var rows []row
	for k, v := range values {
		d := find(k)
		if d == nil {
			return fmt.Errorf("%w: %s", ErrUnknown, k)
		}
		if d.secret {
			if v == "" || len(v) > 4096 || len(v) < d.atLeast {
				return BadValue{k}
			}
			sealed, err := s.seal(k, v)
			if err != nil {
				return err
			}
			rows = append(rows, row{k, sealed})
			// Archives name the day their passphrase was set (internal/backup).
			if k == "backup_passphrase" {
				rows = append(rows, row{"backup_passphrase_at", time.Now().UTC().Format(time.DateOnly)})
			}
			continue
		}
		if d.valid != nil && !d.valid(v) {
			return BadValue{k}
		}
		rows = append(rows, row{k, v})
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, upsert, r.key, r.value); err != nil {
			return err
		}
	}
	for _, k := range reset {
		d := find(k)
		if d == nil {
			return fmt.Errorf("%w: %s", ErrUnknown, k)
		}
		if d.secret {
			_, err = tx.ExecContext(ctx, upsert, k, "")
		} else {
			_, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", k)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

/* ---------- reading ---------- */

// Values are the settings in force.
type Values struct {
	Title, Language string
	// PublicURL is the site's address (https://host[:port], no slash at the
	// end), or "" to take it from each request's Host.
	PublicURL         string
	Zone              *time.Location
	ZoneName          string
	Interval          time.Duration
	RefreshDays       int
	Since             string
	YazioEmail        string
	YazioPassword     string
	YazioClientID     string
	YazioClientSecret string
	GoogleClientID    string
	GoogleSecret      string
	Review            bool
	AnthropicKey      string
	Model, Effort     string
	CreditUSD         float64
	OpenFoodFacts     bool
	SleepGoal         int // minutes, for the sleep score

	Backups             bool
	BackupTime          string // "HH:MM" in the site's time zone
	BackupKeepDays      int
	BackupPassphrase    string
	BackupPassphraseAt  string // the day it was set
	BackupDrive         bool
	BackupFolder        string
	BackupDriveKeepDays int

	SMTP SMTP
}

// SMTP is the mail server for the app's e-mails; set up when Host and From
// are.
type SMTP struct {
	Host, User, Password, From string
	FromName                   string // the site's title, shown with From
	Port                       int
	Implicit                   bool // TLS from the start; otherwise STARTTLS
}

// Ready reports whether e-mail is set up.
func (m SMTP) Ready() bool { return m.Host != "" && m.From != "" }

// envZone is the zone the environment names, and its name; UTC without one
// or with a wrong one.
func envZone() (*time.Location, string) {
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc, tz
		}
		log.Printf("TZ %q is not a valid time zone, using UTC", tz)
	}
	return time.UTC, "UTC"
}

// resolve builds Values from a function giving one setting's value.
func resolve(get func(key string) string) Values {
	v := Values{
		Title: get("title"), Language: get("language"), Since: get("since"),
		YazioEmail: get("yazio_email"), YazioPassword: get("yazio_password"),
		YazioClientID: get("yazio_client_id"), YazioClientSecret: get("yazio_client_secret"),
		GoogleClientID: get("google_client_id"), GoogleSecret: get("google_client_secret"),
		AnthropicKey: get("anthropic_key"), Model: get("ai_model"), Effort: get("ai_effort"),
		BackupTime: get("backup_time"), BackupPassphrase: get("backup_passphrase"), BackupPassphraseAt: get("backup_passphrase_at"),
		BackupFolder: get("backup_folder"),
		PublicURL:    strings.TrimRight(get("public_url"), "/"),
	}
	v.Zone, v.ZoneName = envZone()
	if name := get("timezone"); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			v.Zone, v.ZoneName = loc, name
		}
	}
	v.Interval, _ = time.ParseDuration(get("sync_interval"))
	if v.Interval <= 0 {
		v.Interval = 30 * time.Minute
	}
	v.RefreshDays, _ = strconv.Atoi(get("refresh_days"))
	if v.RefreshDays < 1 {
		v.RefreshDays = 3
	}
	v.CreditUSD, _ = strconv.ParseFloat(get("ai_credit_usd"), 64)
	v.SleepGoal, _ = strconv.Atoi(get("sleep_goal"))
	if v.SleepGoal < 240 {
		v.SleepGoal = 480
	}
	onOff := func(key string) bool {
		b, err := strconv.ParseBool(get(key))
		return err != nil || b
	}
	v.Review, v.OpenFoodFacts = onOff("review"), onOff("openfoodfacts")
	v.Backups = onOff("backups")
	v.BackupDrive, _ = strconv.ParseBool(get("backup_drive"))
	v.BackupKeepDays, _ = strconv.Atoi(get("backup_keep_days"))
	if v.BackupKeepDays < 1 {
		v.BackupKeepDays = 14
	}
	v.BackupDriveKeepDays, _ = strconv.Atoi(get("backup_drive_keep_days"))
	if v.BackupDriveKeepDays < 1 {
		v.BackupDriveKeepDays = 30
	}
	v.SMTP = SMTP{Host: get("smtp_host"), User: get("smtp_user"), Password: get("smtp_password"), From: get("smtp_from"), FromName: v.Title,
		Implicit: get("smtp_tls") == "implicit"}
	v.SMTP.Port, _ = strconv.Atoi(get("smtp_port"))
	if v.SMTP.Port < 1 {
		v.SMTP.Port = 587
	}
	return v
}

func fromEnv(d *def) string {
	if v := os.Getenv(d.env); d.env != "" && v != "" {
		return v
	}
	return d.fallback
}

// Env is the settings from the environment alone, for commands that run
// where no database exists.
func Env() Values {
	return resolve(func(key string) string { return fromEnv(find(key)) })
}

// Values reads the settings in force. A secret that can't be decrypted is
// empty, and the log says so: SETTINGS_KEY changed, or the row is damaged.
func (s *Store) Values(ctx context.Context) (Values, error) {
	have, err := s.rows(ctx)
	if err != nil {
		return Values{}, err
	}
	return resolve(func(key string) string {
		d := find(key)
		stored, set := have[key]
		switch {
		case !set:
			return fromEnv(d)
		case !d.secret || stored == "":
			return stored
		}
		plain, err := s.open(key, stored)
		if err != nil {
			log.Printf("settings: %s can't be read: is SETTINGS_KEY the one it was saved with?", key)
			return ""
		}
		return plain
	}), nil
}

// Preview returns the settings as they would be after saving values and
// resetting reset, without saving anything; a secret set by values counts
// as set (its value isn't checked). For rules across settings.
func (s *Store) Preview(ctx context.Context, values map[string]string, reset []string) (Values, error) {
	have, err := s.rows(ctx)
	if err != nil {
		return Values{}, err
	}
	return resolve(func(key string) string {
		d := find(key)
		if v, ok := values[key]; ok {
			return v
		}
		if slices.Contains(reset, key) {
			if d.secret {
				return ""
			}
			return fromEnv(d)
		}
		stored, set := have[key]
		switch {
		case !set:
			return fromEnv(d)
		case !d.secret || stored == "":
			return stored
		}
		return "set" // a sealed secret: only whether there is one matters here
	}), nil
}

// Item is a setting as the page sees it. A secret has no Value, only State.
type Item struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source"` // db, env or default
	Secret bool   `json:"secret,omitempty"`
	State  string `json:"state,omitempty"` // a secret's: set, env, none or unreadable
}

// View describes every setting for the page, without a secret's value.
func (s *Store) View(ctx context.Context) (map[string]Item, error) {
	have, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Item{}
	for i := range defs {
		d := &defs[i]
		stored, set := have[d.key]
		it := Item{Secret: d.secret, Source: "default"}
		if set {
			it.Source = "db"
		} else if d.env != "" && os.Getenv(d.env) != "" {
			it.Source = "env"
		}
		switch {
		case !d.secret && set:
			it.Value = stored
		case !d.secret:
			it.Value = fromEnv(d)
		case set && stored == "":
			it.State = "none"
		case set:
			it.State = "set"
			if _, err := s.open(d.key, stored); err != nil {
				it.State = "unreadable"
			}
		case it.Source == "env":
			it.State = "env"
		default:
			it.State = "none"
		}
		out[d.key] = it
	}
	return out, nil
}
