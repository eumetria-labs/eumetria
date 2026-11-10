package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eumetria/internal/auth"
	"eumetria/internal/lifts"
	"eumetria/site"
)

// A role that may write lifts.
func (e *testEnv) lifter(t *testing.T) *http.Client {
	t.Helper()
	ctx := context.Background()
	if err := e.store.SaveRole(ctx, "Athlete", []string{auth.ViewHealth, auth.EditLifts}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateUser(ctx, "athlete", coachPassword, "Athlete"); err != nil {
		t.Fatal(err)
	}
	return e.signIn(t, "athlete", coachPassword, false)
}

func TestLiftsFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false) // no view.health

	res, body := e.do(t, athlete, "POST", "/api/lifts", `{"lift":" Power  Clean","kg":82.5,"reps":1,"date":"2026-10-20","note":"finally"}`, sameSite)
	if res.StatusCode != 201 {
		t.Fatalf("new entry = %d %s", res.StatusCode, body)
	}
	var added lifts.Entry
	json.Unmarshal([]byte(body), &added)
	if added.ID == 0 || added.Lift != "Power Clean" || added.Kg != 82.5 || added.CreatedBy != "athlete" {
		t.Errorf("entry = %+v", added)
	}
	path := fmt.Sprintf("/api/lifts/%d", added.ID)
	if res, body := e.do(t, athlete, "PUT", path, `{"lift":"Power Clean","kg":85,"date":"2026-10-21"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("change = %d %s", res.StatusCode, body)
	}

	// A viewer reads it and can't change it; without view.health it's refused.
	res, body = e.do(t, anna, "GET", "/api/lifts", "")
	var list []lifts.Entry
	if json.Unmarshal([]byte(body), &list); res.StatusCode != 200 || len(list) != 1 || list[0].Kg != 85 || list[0].Reps != 1 {
		t.Errorf("anna's lifts = %d %s", res.StatusCode, body)
	}
	for _, c := range []struct {
		who          *http.Client
		method, path string
		body         string
		want         int
	}{
		{anna, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`, 403},
		{anna, "PUT", path, `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`, 403},
		{anna, "DELETE", path, "", 403},
		{coach, "GET", "/api/lifts", "", 403},
		{athlete, "POST", "/api/lifts", `{"lift":"","kg":60,"date":"2026-10-20"}`, 400},
		{athlete, "POST", "/api/lifts", `{"lift":"Snatch","kg":0,"date":"2026-10-20"}`, 400},
		{athlete, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"date":"2999-01-01"}`, 400},
		{athlete, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"date":"2026-10-20","extra":1}`, 400},
		{athlete, "PUT", "/api/lifts/999", `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`, 404},
		{athlete, "PUT", "/api/lifts/abc", `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`, 404},
		{athlete, "DELETE", "/api/lifts/999", "", 404},
	} {
		if res, body := e.do(t, c.who, c.method, c.path, c.body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("%s %s %s = %d %s, want %d", c.method, c.path, c.body, res.StatusCode, body, c.want)
		}
	}
	// From another site, even with the scope.
	if res, _ := e.do(t, athlete, "DELETE", path, "", header("Content-Type", "application/json")); res.StatusCode != 403 {
		t.Errorf("cross-site delete = %d", res.StatusCode)
	}

	if res, _ := e.do(t, athlete, "DELETE", path, "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if _, body := e.do(t, anna, "GET", "/api/lifts", ""); body != "[]\n" && body != "[]" {
		t.Errorf("after delete = %q", body)
	}

	// The audit log names each change.
	actions, _ := e.store.AuditLog(context.Background(), 50)
	seen := map[string]bool{}
	for _, a := range actions {
		seen[a.Action] = true
	}
	for _, want := range []string{"lift_added", "lift_changed", "lift_deleted"} {
		if !seen[want] {
			t.Errorf("no %s in the audit log", want)
		}
	}
}

// With -demo an admin without a second factor may write lifts, as the demo
// account has none; admin rights still need one.
func TestDemoWritesLiftsWithoutSecondFactor(t *testing.T) {
	e := newTestEnv(t, true)
	e.srv.Close()
	e.runner.cfg.demo = true
	dir := t.TempDir()
	e.srv = httptest.NewTLSServer(newHandler(e.runner, site.Files, e.data, dir, dir, dir, e.store))
	t.Cleanup(e.srv.Close)
	newbie := e.signIn(t, "newbie", adminPassword+"!", false)
	if res, body := e.do(t, newbie, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`, sameSite); res.StatusCode != 201 {
		t.Errorf("demo lift = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, newbie, "GET", "/api/me", ""); !strings.Contains(body, `"demo":true`) {
		t.Errorf("me = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, newbie, "GET", "/api/admin/users", ""); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("admin without a factor = %d %s", res.StatusCode, body)
	}
}
