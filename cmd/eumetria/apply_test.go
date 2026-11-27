package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eumetria/internal/update"
	"eumetria/internal/updater"
)

func TestUpdateTag(t *testing.T) {
	stable := &checkResult{Result: update.Result{Channel: "stable", Target: "1.4.0", Major: &update.Release{Version: "2.0.0"}}}
	for _, c := range []struct {
		kind, channel, running string
		last                   *checkResult
		want                   string
		err                    error
	}{
		{"update", "stable", "1.2.0", nil, "1", nil},
		{"update", "edge", "1.2.0", nil, "latest", nil},
		{"update", "stable", "main-abc1234", stable, "1", nil}, // edge → stable
		{"update", "stable", "main-abc1234", nil, "", errNoUpdateTarget},
		{"major", "stable", "1.2.0", stable, "2", nil},
		{"major", "stable", "2.0.0", stable, "", errNoUpdateTarget}, // not newer
		{"major", "stable", "1.2.0", nil, "", errNoUpdateTarget},
	} {
		got, err := updateTag(c.kind, c.channel, c.running, c.last)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s %s %s: %q %v, want %q %v", c.kind, c.channel, c.running, got, err, c.want, c.err)
		}
	}
}

// fakeUpdater is the updater container: it records what it was asked.
type fakeUpdater struct {
	mu      sync.Mutex
	updates []map[string]string
	newer   bool
}

func (f *fakeUpdater) asked() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string{}, f.updates...)
}

func withUpdater(t *testing.T) *fakeUpdater {
	t.Helper()
	f := &fakeUpdater{}
	const tok = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(updater.Status{Tag: "1", Host: "vps-1", History: []updater.Job{}})
		case "/check":
			f.mu.Lock()
			json.NewEncoder(w).Encode(map[string]any{"images": []updater.Newer{{Service: "eumetria", Newer: f.newer}}})
			f.mu.Unlock()
		case "/update":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			f.updates = append(f.updates, in)
			f.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(updater.Job{ID: "job-1", Tag: in["tag"], State: "running"})
		}
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte(tok+"\n"), 0o644)
	t.Setenv("UPDATER_TOKEN_FILE", file)
	t.Setenv("UPDATER_URL", srv.URL)
	return f
}

// applied waits for the update to reach the updater, or to fail.
func applied(t *testing.T, a *app, f *fakeUpdater) applyState {
	t.Helper()
	for range 500 {
		if s := a.updates.applying(); !s.Running {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the update didn't get to the updater")
	return applyState{}
}

func TestApplyNeedsTheUpdater(t *testing.T) {
	e := newTestEnv(t, true)
	t.Setenv("UPDATER_TOKEN_FILE", filepath.Join(t.TempDir(), "none"))
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "POST", "/api/admin/update", `{}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_updater") {
		t.Fatalf("without the updater = %d %s", res.StatusCode, body)
	}
	anna := e.signIn(t, "anna", viewerPassword, false)
	if res, _ := e.do(t, anna, "POST", "/api/admin/update", `{}`, sameSite); res.StatusCode != 403 {
		t.Errorf("viewer = %d", res.StatusCode)
	}
}

func TestApplyBacksUpFirst(t *testing.T) {
	e := newTestEnv(t, true)
	f := withUpdater(t)
	fakeUpdates(t, update.Result{Available: true, Target: "1.1.0"}, nil)
	a := e.app(t)
	ctx := context.Background()
	a.checkUpdates(ctx)

	if err := a.startApply(ctx, "update", nil, "", false); err != nil {
		t.Fatal(err)
	}
	s := applied(t, a, f)
	asked := f.asked()
	if s.Error != "" || s.Step != "updating" || s.Job != "job-1" || len(asked) != 1 || asked[0]["tag"] != "1" {
		t.Fatalf("state %+v, asked %v", s, asked)
	}
	// The backup the updater was told about exists, and came first.
	run := e.lastBackup(t)
	if run.Error != "" || run.File == "" || asked[0]["backup"] != run.File {
		t.Fatalf("backup %+v, asked %v", run, asked)
	}
	if _, err := os.Stat(filepath.Join(e.runner.cfg.backupDir, run.File)); err != nil {
		t.Errorf("the backup file: %v", err)
	}
	// A major version only when the last check announced one.
	if err := a.startApply(ctx, "major", nil, "", false); !errors.Is(err, errNoUpdateTarget) {
		t.Errorf("major without one: %v", err)
	}
}

func TestApplyAPI(t *testing.T) {
	e := newTestEnv(t, true)
	withUpdater(t)
	fakeUpdates(t, update.Result{Available: true, Target: "1.1.0"}, nil)
	admin := e.signIn(t, "patrick", adminPassword, false)
	e.do(t, admin, "POST", "/api/admin/update/check", "{}", sameSite)
	if res, _ := e.do(t, admin, "POST", "/api/admin/update", `{"kind":"downgrade"}`, sameSite); res.StatusCode != 400 {
		t.Errorf("bad kind = %d", res.StatusCode)
	}
	res, body := e.do(t, admin, "POST", "/api/admin/update", `{"kind":"update"}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"apply":{"running":true`) || !strings.Contains(body, `"host":"vps-1"`) {
		t.Fatalf("update = %d %s", res.StatusCode, body)
	}
	e.runner.wait()
}

func TestAutoInstallOncePerDay(t *testing.T) {
	// A release: a development build has no stable channel to install from.
	old := appVersion
	appVersion = "1.0.0"
	t.Cleanup(func() { appVersion = old })
	e := newTestEnv(t, true)
	f := withUpdater(t)
	a := e.app(t)
	ctx := context.Background()
	v, _ := e.runner.settings.Values(ctx)
	at := func(hm string) time.Time {
		t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-12-01 "+hm, v.Zone)
		return t
	}

	// Nothing new: no install, also not later that day.
	fakeUpdates(t, update.Result{}, nil)
	a.autoInstall(ctx, v, at("03:59"))
	a.autoInstall(ctx, v, at("04:00"))
	if len(f.asked()) != 0 {
		t.Fatal("installed with nothing new")
	}
	// A security rebuild the next day: installed at 04:00, once.
	f.mu.Lock()
	f.newer = true
	f.mu.Unlock()
	next := func(hm string) time.Time { return at(hm).AddDate(0, 0, 1) }
	a.autoInstall(ctx, v, next("04:01"))
	applied(t, a, f)
	a.autoInstall(ctx, v, next("05:00"))
	if got := f.asked(); len(got) != 1 || got[0]["tag"] != "1" {
		t.Fatalf("asked %v", got)
	}
	e.runner.wait()

	// Off: nothing, even when due.
	e.runner.settings.Save(ctx, map[string]string{"update_auto": "false"}, nil)
	v, _ = e.runner.settings.Values(ctx)
	a.autoInstall(ctx, v, next("04:00").AddDate(0, 0, 1))
	if len(f.asked()) != 1 {
		t.Error("installed while off")
	}
}
