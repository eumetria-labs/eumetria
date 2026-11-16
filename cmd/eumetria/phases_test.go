package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"eumetria/internal/phases"
)

func TestPhasesFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	anna := e.signIn(t, "anna", viewerPassword, false)

	res, body := e.do(t, athlete, "POST", "/api/phases", `{"kind":"cut","label":" Winter  cut","start":"2026-10-01"}`, sameSite)
	if res.StatusCode != 201 {
		t.Fatalf("new phase = %d %s", res.StatusCode, body)
	}
	var added phases.Phase
	json.Unmarshal([]byte(body), &added)
	if added.ID == 0 || added.Label != "Winter cut" || added.End != "" || added.CreatedBy != "athlete" {
		t.Errorf("phase = %+v", added)
	}
	path := fmt.Sprintf("/api/phases/%d", added.ID)
	if res, body := e.do(t, athlete, "PUT", path, `{"kind":"cut","label":"Winter cut","start":"2026-10-01","end":"2026-10-31"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("change = %d %s", res.StatusCode, body)
	}

	// A viewer reads them and can't change them.
	res, body = e.do(t, anna, "GET", "/api/phases", "")
	var list []phases.Phase
	if json.Unmarshal([]byte(body), &list); res.StatusCode != 200 || len(list) != 1 || list[0].End != "2026-10-31" {
		t.Errorf("anna's phases = %d %s", res.StatusCode, body)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/phases", `{"kind":"bulk","start":"2026-10-01"}`, 403},
		{"DELETE", path, "", 403},
	} {
		if res, body := e.do(t, anna, c.method, c.path, c.body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("anna %s %s = %d %s, want %d", c.method, c.path, res.StatusCode, body, c.want)
		}
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/phases", `{"kind":"event","start":"2026-10-01"}`, 400},
		{"POST", "/api/phases", `{"kind":"cut","start":"2999-01-01"}`, 400},
		{"POST", "/api/phases", `{"kind":"cut","start":"2026-10-01","extra":1}`, 400},
		{"PUT", "/api/phases/999", `{"kind":"cut","start":"2026-10-01"}`, 404},
		{"DELETE", "/api/phases/abc", "", 404},
	} {
		if res, body := e.do(t, athlete, c.method, c.path, c.body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("%s %s %s = %d %s, want %d", c.method, c.path, c.body, res.StatusCode, body, c.want)
		}
	}

	if res, _ := e.do(t, athlete, "DELETE", path, "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if _, body := e.do(t, anna, "GET", "/api/phases", ""); body != "[]\n" && body != "[]" {
		t.Errorf("after delete = %q", body)
	}
	actions, _ := e.store.AuditLog(context.Background(), 50)
	seen := map[string]bool{}
	for _, a := range actions {
		seen[a.Action] = true
	}
	for _, want := range []string{"phase_added", "phase_changed", "phase_deleted"} {
		if !seen[want] {
			t.Errorf("no %s in the audit log", want)
		}
	}
}
