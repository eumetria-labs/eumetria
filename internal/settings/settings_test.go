package settings

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/db"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func open(t *testing.T, key string) *Store {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s, err := New(d, key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func stored(t *testing.T, s *Store, key string) string {
	t.Helper()
	rows, err := s.rows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows[key]
}

func TestKey(t *testing.T) {
	for _, k := range []string{"short", strings.Repeat("g", 64), testKey[:62]} {
		if _, err := New(nil, k); err == nil {
			t.Errorf("%q was accepted", k)
		}
	}
}

func TestSecretsAreEncrypted(t *testing.T) {
	ctx := context.Background()
	s := open(t, testKey)
	if err := s.Save(ctx, map[string]string{"yazio_password": "hunter2 hunter2"}, nil); err != nil {
		t.Fatal(err)
	}
	raw := stored(t, s, "yazio_password")
	if !strings.HasPrefix(raw, secretPrefix) || strings.Contains(raw, "hunter2") {
		t.Fatalf("stored = %q", raw)
	}
	v, err := s.Values(ctx)
	if err != nil || v.YazioPassword != "hunter2 hunter2" {
		t.Fatalf("values = %+v, %v", v, err)
	}

	// The same text twice is two different rows.
	s.Save(ctx, map[string]string{"yazio_password": "hunter2 hunter2"}, nil)
	if stored(t, s, "yazio_password") == raw {
		t.Error("the nonce was reused")
	}

	// A value moved to another row doesn't decrypt.
	if _, err := s.DB.ExecContext(ctx, upsert, "anthropic_key", raw); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Values(ctx); v.AnthropicKey != "" {
		t.Errorf("a moved secret reads as %q", v.AnthropicKey)
	}

	// Neither does anything under another key, and the page is told.
	other, _ := New(s.DB, strings.Repeat("ab", 32))
	if v, _ := other.Values(ctx); v.YazioPassword != "" {
		t.Errorf("the wrong key read %q", v.YazioPassword)
	}
	view, err := other.View(ctx)
	if err != nil || view["yazio_password"].State != "unreadable" {
		t.Errorf("view = %+v, %v", view["yazio_password"], err)
	}
	if strings.Contains(view["yazio_password"].Value, "v1:") {
		t.Error("the view shows a secret's row")
	}
}

func TestWithoutAKeySecretsStayInTheEnvironment(t *testing.T) {
	ctx := context.Background()
	t.Setenv("YAZIO_PASSWORD", "from env")
	t.Setenv("YAZIO_EMAIL", "a@example.org")
	s := open(t, "")
	if s.CanEncrypt() {
		t.Fatal("no key, but it can encrypt")
	}
	if err := s.Save(ctx, map[string]string{"yazio_password": "x"}, nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("saving a secret = %v", err)
	}
	if _, err := s.Import(ctx); err != nil {
		t.Fatal(err)
	}
	if stored(t, s, "yazio_password") != "" {
		t.Error("a secret went in unencrypted")
	}
	if stored(t, s, "yazio_email") != "a@example.org" {
		t.Error("the plain value wasn't imported")
	}
	v, _ := s.Values(ctx)
	if v.YazioPassword != "from env" {
		t.Errorf("password = %q", v.YazioPassword)
	}
	view, _ := s.View(ctx)
	if view["yazio_password"].State != "env" {
		t.Errorf("state = %+v", view["yazio_password"])
	}

	// With the key the secrets follow, while what's stored stays.
	t.Setenv("YAZIO_EMAIL", "changed@example.org")
	withKey, _ := New(s.DB, testKey)
	if n, err := withKey.Import(ctx); err != nil || n != 1 {
		t.Fatalf("import with a key = %d, %v", n, err)
	}
	if raw := stored(t, withKey, "yazio_password"); !strings.HasPrefix(raw, secretPrefix) {
		t.Errorf("stored = %q", raw)
	}
	if v, _ := withKey.Values(ctx); v.YazioEmail != "a@example.org" || v.YazioPassword != "from env" {
		t.Errorf("after the second import: %+v", v)
	}
}

func TestImportRunsOnce(t *testing.T) {
	ctx := context.Background()
	t.Setenv("STATS_TITLE", "From env")
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	t.Setenv("SYNC_INTERVAL", "1m") // too short: left out
	s := open(t, testKey)
	if n, err := s.Import(ctx); err != nil || n != 2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if stored(t, s, "sync_interval") != "" {
		t.Error("an invalid value was imported")
	}
	if err := s.Save(ctx, map[string]string{"title": "From the page"}, []string{"anthropic_key"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Import(ctx); err != nil || n != 0 {
		t.Fatalf("second import = %d, %v", n, err)
	}
	v, _ := s.Values(ctx)
	if v.Title != "From the page" {
		t.Errorf("title = %q", v.Title)
	}
	// A secret removed in the page doesn't come back from the environment.
	if v.AnthropicKey != "" {
		t.Errorf("the removed key came back: %q", v.AnthropicKey)
	}
	view, _ := s.View(ctx)
	if view["anthropic_key"].State != "none" {
		t.Errorf("state = %+v", view["anthropic_key"])
	}
}

func TestDefaultsAndReset(t *testing.T) {
	ctx := context.Background()
	s := open(t, "")
	v, err := s.Values(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "Eumetria" || v.Interval != 30*time.Minute || v.RefreshDays != 3 || !v.Review || !v.OpenFoodFacts || v.Model == "" || v.Effort != "medium" {
		t.Errorf("defaults = %+v", v)
	}
	t.Setenv("TZ", "Europe/Berlin")
	if v, _ := s.Values(ctx); v.ZoneName != "Europe/Berlin" {
		t.Errorf("zone = %s", v.ZoneName)
	}
	if err := s.Save(ctx, map[string]string{"timezone": "Asia/Tokyo", "openfoodfacts": "false", "review": "false"}, nil); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Values(ctx)
	if v.ZoneName != "Asia/Tokyo" || v.Zone.String() != "Asia/Tokyo" || v.OpenFoodFacts || v.Review {
		t.Errorf("saved = %+v", v)
	}
	view, _ := s.View(ctx)
	if view["timezone"].Source != "db" {
		t.Errorf("source = %+v", view["timezone"])
	}
	if err := s.Save(ctx, nil, []string{"timezone"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Values(ctx); v.ZoneName != "Europe/Berlin" {
		t.Errorf("after the reset: %s", v.ZoneName)
	}
}

func TestSaveChecks(t *testing.T) {
	ctx := context.Background()
	s := open(t, testKey)
	for key, value := range map[string]string{
		"title": " ", "language": "fr", "timezone": "Mars/Base", "sync_interval": "4m",
		"refresh_days": "0", "since": "yesterday", "yazio_email": "nope", "ai_effort": "huge",
		"ai_credit_usd": "-1", "review": "maybe", "yazio_password": "",
	} {
		var bad BadValue
		if err := s.Save(ctx, map[string]string{key: value}, nil); !errors.As(err, &bad) || bad.Key != key {
			t.Errorf("%s = %q: %v", key, value, err)
		}
	}
	for _, c := range []struct {
		values map[string]string
		reset  []string
	}{{map[string]string{"nope": "x"}, nil}, {nil, []string{"env_imported"}}} {
		if err := s.Save(ctx, c.values, c.reset); !errors.Is(err, ErrUnknown) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	// Nothing of a refused change is written.
	if err := s.Save(ctx, map[string]string{"title": "New", "since": "nope"}, nil); err == nil {
		t.Fatal("refused nothing")
	}
	if v, _ := s.Values(ctx); v.Title != "Eumetria" {
		t.Errorf("title = %q", v.Title)
	}
}

func TestBackupPassphrase(t *testing.T) {
	ctx := context.Background()
	s := open(t, testKey)
	var bad BadValue
	if err := s.Save(ctx, map[string]string{"backup_passphrase": "too short"}, nil); !errors.As(err, &bad) {
		t.Errorf("a short passphrase: %v", err)
	}
	if err := s.Save(ctx, map[string]string{"backup_passphrase_at": "2020-01-01"}, nil); !errors.As(err, &bad) {
		t.Errorf("setting the date by hand: %v", err)
	}
	if err := s.Save(ctx, map[string]string{"backup_passphrase": "long enough to pass"}, nil); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Values(ctx)
	if v.BackupPassphrase != "long enough to pass" || v.BackupPassphraseAt != time.Now().UTC().Format(time.DateOnly) {
		t.Errorf("values = %q, %q", v.BackupPassphrase, v.BackupPassphraseAt)
	}
	if v.BackupTime != "03:30" || v.BackupKeepDays != 14 || v.BackupDriveKeepDays != 30 || v.BackupFolder == "" || v.BackupDrive || !v.Backups {
		t.Errorf("defaults = %+v", v)
	}
	for _, c := range []string{"3:30", "24:00", "nope"} {
		if err := s.Save(ctx, map[string]string{"backup_time": c}, nil); !errors.As(err, &bad) {
			t.Errorf("time %q: %v", c, err)
		}
	}
}
