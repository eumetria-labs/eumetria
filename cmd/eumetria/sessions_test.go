package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"eumetria/internal/lifts"
	"eumetria/internal/sessions"
)

func TestSessionsFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false) // no view.health

	body := `{"date":"2026-10-20","title":"Squat day","sets":[
		{"lift":"Back Squat","type":"warmup","reps":5,"kg":60},
		{"lift":"Back Squat","reps":5,"kg":100},
		{"lift":"Back Squat","reps":2,"kg":120,"rpe":9}],"bests":[2]}`
	res, out := e.do(t, athlete, "POST", "/api/training/sessions", body, jsonBody...)
	if res.StatusCode != 201 {
		t.Fatalf("new session = %d %s", res.StatusCode, out)
	}
	var x sessions.Session
	json.Unmarshal([]byte(out), &x)
	if len(x.Sets) != 3 || x.Sets[0].Type != "warmup" || *x.Sets[2].RPE != 9 || len(x.Bests) != 1 || x.CreatedBy != "athlete" {
		t.Errorf("session = %+v", x)
	}
	// The best is a lift entry, linked to the session.
	_, out = e.do(t, anna, "GET", "/api/lifts", "")
	var bests []lifts.Entry
	json.Unmarshal([]byte(out), &bests)
	if len(bests) != 1 || bests[0].Kg != 120 || bests[0].Reps != 2 || bests[0].SessionID == nil || *bests[0].SessionID != x.ID {
		t.Errorf("lifts = %s", out)
	}
	path := fmt.Sprintf("/api/training/sessions/%d", x.ID)
	for _, c := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"anna reads", "GET", "/api/training/sessions", "", 200},
		{"anna writes", "POST", "/api/training/sessions", body, 403},
		{"anna deletes", "DELETE", path, "", 403},
	} {
		if res, out := e.do(t, anna, c.method, c.path, c.body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("%s = %d %s", c.name, res.StatusCode, out)
		}
	}
	if res, _ := e.do(t, coach, "GET", "/api/training/sessions", ""); res.StatusCode != 403 {
		t.Errorf("coach reads = %d", res.StatusCode)
	}
	for in, want := range map[string]int{
		`{"date":"2026-10-20","sets":[]}`: 400,
		`{"date":"2026-10-20","sets":[{"lift":"A","reps":1,"kg":1}],"fitbit_workout":"nope"}`: 400,
	} {
		if res, out := e.do(t, athlete, "POST", "/api/training/sessions", in, jsonBody...); res.StatusCode != want {
			t.Errorf("%s = %d %s", in, res.StatusCode, out)
		}
	}
	if res, _ := e.do(t, athlete, "PUT", "/api/training/sessions/999", body, jsonBody...); res.StatusCode != 404 {
		t.Errorf("PUT unknown = %d", res.StatusCode)
	}
	// Deleting the session leaves the best, without the link.
	if res, _ := e.do(t, athlete, "DELETE", path, "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d", res.StatusCode)
	}
	_, out = e.do(t, anna, "GET", "/api/lifts", "")
	if !strings.Contains(out, `"kg":120`) || strings.Contains(out, "session_id") {
		t.Errorf("lifts after delete = %s", out)
	}
}
