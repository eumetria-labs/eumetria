package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/lifts"
	"eumetria/internal/review"
	"eumetria/internal/wods"
)

func TestWodsFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false) // no view.health

	res, body := e.do(t, athlete, "POST", "/api/wods", `{"name":" Fran ","scoring":"time","description":"21-15-9"}`, sameSite)
	var fran wods.Workout
	if json.Unmarshal([]byte(body), &fran); res.StatusCode != 201 || fran.ID == 0 || fran.Name != "Fran" {
		t.Fatalf("new workout = %d %s", res.StatusCode, body)
	}
	wodPath := fmt.Sprintf("/api/wods/%d", fran.ID)
	res, body = e.do(t, athlete, "POST", wodPath+"/results", `{"value":252,"scaled":true,"date":"2026-10-20","note":"banded"}`, sameSite)
	var r wods.Result
	if json.Unmarshal([]byte(body), &r); res.StatusCode != 201 || r.Workout != fran.ID || r.Value != 252 || !r.Scaled {
		t.Fatalf("new result = %d %s", res.StatusCode, body)
	}
	resultPath := fmt.Sprintf("/api/wod-results/%d", r.ID)
	if res, body := e.do(t, athlete, "PUT", resultPath, `{"value":245,"date":"2026-10-20"}`, sameSite); res.StatusCode != 200 || !strings.Contains(body, `"value":245`) {
		t.Errorf("change result = %d %s", res.StatusCode, body)
	}

	// A viewer reads both; without view.health they're refused.
	res, body = e.do(t, anna, "GET", "/api/wods", "")
	var all struct {
		Workouts []wods.Workout `json:"workouts"`
		Results  []wods.Result  `json:"results"`
	}
	if json.Unmarshal([]byte(body), &all); res.StatusCode != 200 || len(all.Workouts) != 1 || len(all.Results) != 1 || all.Results[0].Value != 245 {
		t.Errorf("anna's wods = %d %s", res.StatusCode, body)
	}
	type call struct {
		method, path, body string
		want               int
	}
	run := func(who string, c call) {
		client := athlete
		switch who {
		case "anna":
			client = anna
		case "coach":
			client = coach
		}
		if res, body := e.do(t, client, c.method, c.path, c.body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("%s: %s %s %s = %d %s, want %d", who, c.method, c.path, c.body, res.StatusCode, body, c.want)
		}
	}
	run("anna", call{"POST", "/api/wods", `{"name":"Grace","scoring":"time"}`, 403})
	run("anna", call{"PUT", wodPath, `{"name":"Fran","scoring":"time"}`, 403})
	run("anna", call{"DELETE", resultPath, "", 403})
	run("anna", call{"POST", wodPath + "/results", `{"value":250,"date":"2026-10-20"}`, 403})
	run("coach", call{"GET", "/api/wods", "", 403})
	run("athlete", call{"POST", "/api/wods", `{"name":"Grace","scoring":"points"}`, 400})
	run("athlete", call{"POST", "/api/wods", `{"name":"fran","scoring":"time"}`, 409})
	run("athlete", call{"PUT", wodPath, `{"name":"Fran","scoring":"reps"}`, 409}) // it has results
	run("athlete", call{"PUT", "/api/wods/999", `{"name":"Fran","scoring":"time"}`, 404})
	run("athlete", call{"POST", wodPath + "/results", `{"value":12.5,"date":"2026-10-20"}`, 400})
	run("athlete", call{"POST", "/api/wods/999/results", `{"value":250,"date":"2026-10-20"}`, 404})
	run("athlete", call{"PUT", "/api/wod-results/999", `{"value":250,"date":"2026-10-20"}`, 404})
	run("athlete", call{"DELETE", "/api/wod-results/abc", "", 404})

	run("athlete", call{"DELETE", resultPath, "", 204})
	run("athlete", call{"DELETE", wodPath, "", 204})
	if _, body := e.do(t, anna, "GET", "/api/wods", ""); !strings.Contains(body, `"workouts":[]`) || !strings.Contains(body, `"results":[]`) {
		t.Errorf("after delete = %s", body)
	}

	actions, _ := e.store.AuditLog(context.Background(), 50)
	seen := map[string]bool{}
	for _, a := range actions {
		seen[a.Action] = true
	}
	for _, want := range []string{"wod_added", "wod_deleted", "wod_result_added", "wod_result_changed", "wod_result_deleted"} {
		if !seen[want] {
			t.Errorf("no %s in the audit log", want)
		}
	}
}

// The weekly review reads lifts and benchmark workouts from app.db; without
// one (plain files) there are none.
func TestLoadNoted(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	if n := loadNoted(ctx, path); n.lifts != nil || n.workouts != nil {
		t.Errorf("no database = %+v", n)
	}
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := auth.NewStore(d).CreateUser(ctx, "anna", viewerPassword, auth.RoleViewer)
	(&lifts.Store{DB: d, Now: time.Now}).Add(ctx, lifts.Input{Lift: "Snatch", Kg: 60, Reps: 1, Date: "2026-11-02"}, u.ID)
	ws := &wods.Store{DB: d, Now: time.Now}
	fran, _ := ws.AddWorkout(ctx, wods.WorkoutInput{Name: "Fran", Scoring: wods.Time}, u.ID)
	ws.AddResult(ctx, fran.ID, wods.ResultInput{Value: 300, Date: "2026-11-03"}, u.ID)
	d.Close()

	n := loadNoted(ctx, path)
	if len(n.lifts) != 1 || len(n.workouts) != 1 || len(n.results) != 1 {
		t.Fatalf("noted = %+v", n)
	}
	w := review.Week{From: "2026-11-02", To: "2026-11-08", Days: []review.Day{{Date: "2026-11-08"}}}
	n.addTo(&w)
	if len(w.Lifts) != 1 || len(w.Benchmarks) != 1 || w.Benchmarks[0].Result != "5:00" {
		t.Errorf("week = %+v %+v", w.Lifts, w.Benchmarks)
	}
}

// A result linked to a Fitbit workout takes its date; an unknown workout is
// refused, and the link can be dropped again.
func TestWodResultsLinkToWorkouts(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	res, body := e.do(t, athlete, "POST", "/api/wods", `{"name":"Fran","scoring":"time"}`, sameSite)
	var fran wods.Workout
	if json.Unmarshal([]byte(body), &fran); res.StatusCode != 201 {
		t.Fatalf("workout = %d %s", res.StatusCode, body)
	}
	path := fmt.Sprintf("/api/wods/%d/results", fran.ID)
	res, body = e.do(t, athlete, "POST", path, `{"value":300,"date":"2026-10-01","fitbit_workout":"w1"}`, sameSite)
	var r wods.Result
	if json.Unmarshal([]byte(body), &r); res.StatusCode != 201 || r.FitbitWorkout != "w1" || r.Date != "2026-10-20" {
		t.Fatalf("linked = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, athlete, "POST", path, `{"value":300,"date":"2026-10-01","fitbit_workout":"nope"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "bad_workout") {
		t.Errorf("unknown workout = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, athlete, "POST", path, `{"value":300,"date":"2026-10-01","fitbit_workout":"a b"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "bad_result") {
		t.Errorf("odd id = %d %s", res.StatusCode, body)
	}
	res, body = e.do(t, athlete, "PUT", fmt.Sprintf("/api/wod-results/%d", r.ID), `{"value":290,"date":"2026-10-21"}`, sameSite)
	var after wods.Result
	if json.Unmarshal([]byte(body), &after); res.StatusCode != 200 || after.FitbitWorkout != "" || after.Date != "2026-10-21" {
		t.Errorf("unlinked = %d %s", res.StatusCode, body)
	}
}
