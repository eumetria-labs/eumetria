package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"eumetria/internal/update"
)

// No test asks GitHub: the loop's checks (a minute after a test server
// starts) get this.
func init() {
	updateCheck = func(context.Context, string, string) (update.Result, error) {
		return update.Result{}, errors.New("no network in tests")
	}
}

// fakeUpdates answers checks with res and counts them.
func fakeUpdates(t *testing.T, res update.Result, err error) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	old := updateCheck
	updateCheck = func(_ context.Context, channel, running string) (update.Result, error) {
		n.Add(1)
		r := res
		r.Channel, r.Running, r.CheckedAt = channel, running, time.Now().UTC()
		return r, err
	}
	t.Cleanup(func() { updateCheck = old })
	return &n
}

type updateView struct {
	Version string       `json:"version"`
	Channel string       `json:"channel"`
	Auto    bool         `json:"auto"`
	Time    string       `json:"time"`
	Last    *checkResult `json:"last"`
	Updater updaterView  `json:"updater"`
	System  systemInfo   `json:"system"`
}

func TestUpdateIsForAdmins(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	for _, r := range [][2]string{{"GET", "/api/admin/update"}, {"POST", "/api/admin/update/check"}} {
		if res, _ := e.do(t, anna, r[0], r[1], "{}", sameSite); res.StatusCode != 403 {
			t.Errorf("%s %s = %d, want 403", r[0], r[1], res.StatusCode)
		}
	}
}

func TestUpdateCheck(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	n := fakeUpdates(t, update.Result{Available: true, Target: "1.1.0", Releases: []update.Release{{Version: "1.1.0", URL: "https://example/v1.1.0"}}}, nil)

	res, body := e.do(t, admin, "GET", "/api/admin/update", "")
	var v updateView
	if err := json.Unmarshal([]byte(body), &v); res.StatusCode != 200 || err != nil {
		t.Fatalf("GET = %d %s", res.StatusCode, body)
	}
	if v.Version != appVersion || v.Channel != "stable" || !v.Auto || v.Time != "04:00" || v.Last != nil || v.Updater.Present {
		t.Errorf("before a check: %+v", v)
	}
	if v.System.Schema == 0 || v.System.Schema != v.System.SchemaMax || v.System.Go == "" || v.System.DataDir == "" {
		t.Errorf("system: %+v", v.System)
	}

	res, body = e.do(t, admin, "POST", "/api/admin/update/check", "{}", sameSite)
	json.Unmarshal([]byte(body), &v)
	if res.StatusCode != 200 || v.Last == nil || !v.Last.Available || v.Last.Target != "1.1.0" || n.Load() != 1 {
		t.Fatalf("check = %d %s", res.StatusCode, body)
	}
	// Check now, at most every 30 seconds.
	if res, _ := e.do(t, admin, "POST", "/api/admin/update/check", "{}", sameSite); res.StatusCode != 429 || n.Load() != 1 {
		t.Errorf("again at once = %d, %d checks", res.StatusCode, n.Load())
	}

	// Another channel: the old answer says nothing about it.
	if r, b := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"update_channel":"edge"}}`, sameSite); r.StatusCode != 204 {
		t.Fatalf("channel = %d %s", r.StatusCode, b)
	}
	_, body = e.do(t, admin, "GET", "/api/admin/update", "")
	v = updateView{}
	json.Unmarshal([]byte(body), &v)
	if v.Channel != "edge" || v.Last != nil {
		t.Errorf("after switching: %s", body)
	}
	if r, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"update_channel":"beta"}}`, sameSite); r.StatusCode != 400 {
		t.Errorf("channel beta = %d", r.StatusCode)
	}
}

func TestUpdateCheckFails(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	a := e.app(t)
	fakeUpdates(t, update.Result{Available: true, Target: "1.1.0"}, nil)
	a.checkUpdates(ctx)
	// GitHub fails next time: the last good answer stays, with the error.
	fakeUpdates(t, update.Result{}, update.ErrRateLimit)
	a.checkUpdates(ctx)
	last := a.lastCheck(ctx)
	if last == nil || last.Error == "" || !last.Available || last.Target != "1.1.0" {
		t.Fatalf("last = %+v", last)
	}
}

func TestUpdateDue(t *testing.T) {
	now := time.Now()
	fresh := &checkResult{Result: update.Result{CheckedAt: now.Add(-time.Hour), Channel: "stable", Running: appVersion}}
	if updateDue(fresh, "stable", now) {
		t.Error("checked an hour ago")
	}
	if !updateDue(nil, "stable", now) || !updateDue(fresh, "edge", now) || !updateDue(fresh, "stable", now.Add(24*time.Hour)) {
		t.Error("not due")
	}
	other := *fresh
	other.Running = "0.9.0"
	if !updateDue(&other, "stable", now) {
		t.Error("a check for another version")
	}
}

func TestUpdateMailOncePerVersion(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	f := withMail(t, e)
	e.verified(t, "patrick", "patrick@example.org")
	a := e.app(t)
	fakeUpdates(t, update.Result{Available: true, Target: "1.1.0", Releases: []update.Release{{Version: "1.1.0", URL: "https://example/v1.1.0"}}}, nil)

	// Off by default.
	a.checkUpdates(ctx)
	time.Sleep(50 * time.Millisecond)
	if m := f.to("patrick@example.org"); len(m) != 0 {
		t.Fatalf("mailed with update_mail off: %+v", m)
	}
	if err := e.runner.settings.Save(ctx, map[string]string{"update_mail": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	a.checkUpdates(ctx)
	m := f.to("patrick@example.org")
	if len(m) != 1 || !strings.Contains(m[0].Subject, "1.1.0") || !strings.Contains(m[0].Body, "https://example/v1.1.0") || !strings.Contains(m[0].Body, "#settings=update") {
		t.Fatalf("mail = %+v", m)
	}
	// The same version again: no second mail.
	a.checkUpdates(ctx)
	time.Sleep(50 * time.Millisecond)
	if got := f.to("patrick@example.org"); len(got) != 1 {
		t.Errorf("%d mails for one version", len(got))
	}
}

// app is an app on e's database and settings, for calling the update
// check directly.
func (e *testEnv) app(t *testing.T) *app {
	t.Helper()
	a := &app{runner: e.runner, store: e.store, updates: newUpdates()}
	a.startMail()
	return a
}
