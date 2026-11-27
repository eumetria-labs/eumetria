package main

// Updates, PLAN phase 15a: once a day (and on "Check now") Eumetria asks
// GitHub whether its channel has something newer (internal/update), keeps
// the answer in update_state, tells the admin in the page and, if they want
// it, by e-mail once per version. Settings → Update shows it, with the
// version, the database schema and the system Eumetria runs on. Installing
// is the updater container's job (phase 15b).
//
//	GET  /api/admin/update        version, channel, the last check, the system   admin
//	POST /api/admin/update/check  checks now and answers like GET                admin
//	POST /api/admin/update        installs (apply.go)                            admin, recent sign-in

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/settings"
	"eumetria/internal/update"
)

const (
	updateEvery    = 24 * time.Hour   // how often the channel is checked
	updateLook     = time.Minute      // how often the loop sees whether a check or an install is due
	updateCooldown = 30 * time.Second // "Check now" at most this often
)

// updates is the update check's state, shared by the loop and the API.
type updates struct {
	mu       sync.Mutex
	checking bool
	asked    time.Time  // the last check that ran
	apply    applyState // an update being started (apply.go)
	check    func(ctx context.Context, channel, running string) (update.Result, error)
}

// checkResult is a check as stored and shown: the result, or why it failed.
type checkResult struct {
	update.Result
	Error string `json:"error,omitempty"`
	// Rebuild: no new version, but the registry has newer images of the
	// running one (the weekly security rebuild); the updater knows.
	Rebuild bool `json:"rebuild,omitempty"`
}

// updateCheck asks GitHub; tests replace it, so they never go online.
var updateCheck = (&update.Checker{}).Check

func newUpdates() *updates {
	return &updates{check: func(ctx context.Context, channel, running string) (update.Result, error) {
		return updateCheck(ctx, channel, running)
	}}
}

/* ---------- update_state ---------- */

func (a *app) updateState(ctx context.Context, key string) (string, error) {
	var v string
	err := a.store.DB.QueryRowContext(ctx, "SELECT value FROM update_state WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (a *app) setUpdateState(ctx context.Context, key, value string) error {
	_, err := a.store.DB.ExecContext(ctx, `INSERT INTO update_state (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// lastCheck is the stored result of the last check; nil before the first.
func (a *app) lastCheck(ctx context.Context) *checkResult {
	s, err := a.updateState(ctx, "last_check")
	if err != nil || s == "" {
		return nil
	}
	var r checkResult
	if json.Unmarshal([]byte(s), &r) != nil {
		return nil
	}
	return &r
}

/* ---------- checking ---------- */

// checkUpdates asks GitHub now, stores the answer and mails about a new
// version. It reports false when a check is already running.
func (a *app) checkUpdates(ctx context.Context) bool {
	u := a.updates
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return false
	}
	u.checking, u.asked = true, time.Now()
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.checking = false
		u.mu.Unlock()
	}()

	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		log.Printf("update: %v", err)
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := u.check(ctx, v.UpdateChannel, appVersion)
	out := checkResult{Result: res}
	if err != nil {
		out.Error = err.Error()
		log.Printf("update: checking the %s channel: %v", v.UpdateChannel, err)
		// Keep what the last good check found, with the error beside it.
		if last := a.lastCheck(ctx); last != nil && last.Channel == v.UpdateChannel && last.Running == appVersion && last.Error == "" {
			out.Result = last.Result
			out.CheckedAt = res.CheckedAt
		}
	}
	if c, err := newUpdaterClient(); err == nil {
		if list, err := c.Check(ctx); err == nil {
			for _, n := range list {
				out.Rebuild = out.Rebuild || n.Newer && !res.Available
			}
		} else {
			log.Printf("update: asking the updater: %v", err)
		}
	}
	if b, err := json.Marshal(out); err == nil {
		if err := a.setUpdateState(context.WithoutCancel(ctx), "last_check", string(b)); err != nil {
			log.Printf("update: %v", err)
		}
	}
	if err == nil && res.Available {
		log.Printf("update: %s is available on the %s channel (running %s)", res.Target, res.Channel, appVersion)
		a.mailUpdate(context.WithoutCancel(ctx), v, res)
	}
	return true
}

// updateDue reports whether the loop should check: never checked, the last
// check is a day old, or it was for another channel or version.
func updateDue(last *checkResult, channel string, now time.Time) bool {
	return last == nil || last.Channel != channel || last.Running != appVersion || now.Sub(last.CheckedAt) >= updateEvery
}

// updateLoop checks once a day and installs at the set time. It looks
// every minute; the first look is a minute after the start, so a restart
// doesn't ask GitHub at once, and the stored last check keeps restarts from
// asking more than once a day.
func (a *app) updateLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateLook
		v, err := a.runner.settings.Values(ctx)
		if err != nil {
			continue
		}
		if updateDue(a.lastCheck(ctx), v.UpdateChannel, time.Now()) {
			a.checkUpdates(ctx)
		}
		a.autoInstall(ctx, v, time.Now())
	}
}

/* ---------- the mail ---------- */

// mailUpdate tells the admins about res.Target once: the version is
// remembered when the mail went out.
func (a *app) mailUpdate(ctx context.Context, v settings.Values, res update.Result) {
	if !v.UpdateMail || res.Target == "" {
		return
	}
	if mailed, _ := a.updateState(ctx, "mailed"); mailed == res.Target {
		return
	}
	if _, ok := a.mailValues(ctx); !ok {
		return
	}
	subject, body := updateMailText(v, res)
	users, err := a.store.Users(ctx)
	if err != nil {
		log.Printf("mail: %v", err)
		return
	}
	sent := false
	for _, u := range users {
		if !u.Can(auth.ScopeAdmin) || u.Disabled {
			continue
		}
		addr, err := a.store.VerifiedEmail(ctx, u)
		if err == nil && a.mailTo(ctx, bulkMail, u.ID, addr, subject, body) {
			sent = true
		}
	}
	if sent {
		if err := a.setUpdateState(ctx, "mailed", res.Target); err != nil {
			log.Printf("update: %v", err)
		}
	}
}

// updateMailText is the mail about a new version, in the site's language.
func updateMailText(v settings.Values, res update.Result) (subject, body string) {
	de := mailLang(v) == "de"
	var b strings.Builder
	if de {
		subject = fmt.Sprintf("%s: Update auf %s verfügbar", v.Title, res.Target)
		fmt.Fprintf(&b, "Für %s ist ein Update verfügbar: %s (läuft: %s, Kanal %s).\n\n", v.Title, res.Target, res.Running, res.Channel)
	} else {
		subject = fmt.Sprintf("%s: update to %s available", v.Title, res.Target)
		fmt.Fprintf(&b, "An update for %s is available: %s (running: %s, %s channel).\n\n", v.Title, res.Target, res.Running, res.Channel)
	}
	switch {
	case len(res.Releases) > 0:
		for _, r := range res.Releases {
			fmt.Fprintf(&b, "%s: %s\n", r.Version, r.URL)
		}
		b.WriteString("\n")
	case len(res.Commits) > 0:
		for _, c := range res.Commits[:min(len(res.Commits), 10)] {
			fmt.Fprintf(&b, "%s %s\n", c.SHA, c.Subject)
		}
		if res.Ahead > 10 {
			if de {
				fmt.Fprintf(&b, "… und %d weitere\n", res.Ahead-10)
			} else {
				fmt.Fprintf(&b, "… and %d more\n", res.Ahead-10)
			}
		}
		b.WriteString("\n")
	}
	if v.UpdateAuto && res.Channel == update.Stable {
		if de {
			fmt.Fprintf(&b, "Es wird um %s automatisch installiert.\n\n", v.UpdateTime)
		} else {
			fmt.Fprintf(&b, "It installs by itself at %s.\n\n", v.UpdateTime)
		}
	}
	if v.PublicURL != "" {
		if de {
			fmt.Fprintf(&b, "Einstellungen → Update: %s/#settings=update\n\n", v.PublicURL)
		} else {
			fmt.Fprintf(&b, "Settings → Update: %s/#settings=update\n\n", v.PublicURL)
		}
	}
	fmt.Fprintf(&b, "– %s\n", v.Title)
	return subject, b.String()
}

/* ---------- the system ---------- */

// systemInfo is what Settings → Update shows about where Eumetria runs.
// Inside a container, the hostname is the container's (its ID unless
// compose sets one) and the uptime is the host's: /proc/uptime isn't kept
// apart per container.
type systemInfo struct {
	Hostname   string    `json:"hostname"`
	User       string    `json:"user"`
	Started    time.Time `json:"started"`
	HostUptime int64     `json:"host_uptime,omitempty"` // seconds
	Cwd        string    `json:"cwd"`
	DataDir    string    `json:"data_dir"`
	DBBytes    int64     `json:"db_bytes"`
	Schema     int       `json:"schema"`
	SchemaMax  int       `json:"schema_max"`
	Go         string    `json:"go"`
	Platform   string    `json:"platform"`
	CPUs       int       `json:"cpus"`
	Zone       string    `json:"zone"`
	Container  bool      `json:"container"`
}

func (a *app) system(ctx context.Context, v settings.Values) systemInfo {
	s := systemInfo{
		Started:   a.runner.started.UTC(),
		SchemaMax: db.Latest(),
		Go:        runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		CPUs:      runtime.NumCPU(),
		Zone:      v.ZoneName,
	}
	s.Hostname, _ = os.Hostname()
	s.User = strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	if u, err := user.LookupId(strconv.Itoa(os.Getuid())); err == nil && u.Username != "" {
		s.User = u.Username + " (" + s.User + ")"
	}
	s.Cwd, _ = os.Getwd()
	s.DataDir, _ = filepath.Abs(a.runner.cfg.dataDir)
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(a.runner.cfg.dbPath + suffix); err == nil {
			s.DBBytes += fi.Size()
		}
	}
	s.Schema, _ = a.store.DB.Version(ctx)
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f, _, ok := strings.Cut(string(b), " "); ok {
			if secs, err := strconv.ParseFloat(f, 64); err == nil {
				s.HostUptime = int64(secs)
			}
		}
	}
	_, err := os.Stat("/.dockerenv")
	s.Container = err == nil || os.Getenv("container") != ""
	return s
}

/* ---------- the API ---------- */

func (a *app) updateGet(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		a.failed(w, "update", err)
		return
	}
	a.updates.mu.Lock()
	checking := a.updates.checking
	a.updates.mu.Unlock()
	last := a.lastCheck(ctx)
	// A check for another channel or an older version says nothing now.
	if last != nil && (last.Channel != v.UpdateChannel || last.Running != appVersion) {
		last = nil
	}
	b := update.ParseBuild(appVersion)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  appVersion,
		"build":    b.Kind,
		"channel":  v.UpdateChannel,
		"auto":     v.UpdateAuto,
		"time":     v.UpdateTime,
		"mail":     v.UpdateMail,
		"mail_on":  v.SMTP.Ready(),
		"last":     last,
		"checking": checking,
		"apply":    a.updates.applying(),
		"demo":     a.demo,
		// The updater container (updater.go): whether it is there, and
		// its tag, the host, the running job and the last updates. Without
		// it the page shows the command to run instead of a button.
		"updater": updaterInfo(ctx),
		"system":  a.system(ctx, v),
	})
}

func (a *app) updateCheck(w http.ResponseWriter, req *http.Request, p *principal) {
	if a.demo {
		writeError(w, http.StatusConflict, "demo")
		return
	}
	a.updates.mu.Lock()
	recent := time.Since(a.updates.asked) < updateCooldown
	a.updates.mu.Unlock()
	if recent {
		writeError(w, http.StatusTooManyRequests, "too_soon")
		return
	}
	if !a.checkUpdates(req.Context()) {
		writeError(w, http.StatusConflict, "checking")
		return
	}
	a.updateGet(w, req, p)
}
