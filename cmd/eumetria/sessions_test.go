package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"eumetria/internal/lifts"
	"eumetria/internal/notes"
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
		`{"date":"2026-10-20","sets":[{"lift":"","reps":1,"kg":1}]}`:                             400,
		`{"date":"2026-10-20","workouts":[{"source":"garmin","id":"w1"}]}`:                       400,
		`{"date":"2026-10-20","sets":[{"lift":"A","reps":1,"kg":1}],"workouts":[{"id":"nope"}]}`: 400,
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

// A note, a lift and a benchmark result for the same workout land in one
// training (PLAN 14a); the workout can't start a second one.
func TestTrainingHub(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	trainer := e.writer(t)

	res, out := e.do(t, athlete, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"reps":1,"date":"2026-10-01","fitbit_workout":"w1"}`, jsonBody...)
	var lift lifts.Entry
	json.Unmarshal([]byte(out), &lift)
	if res.StatusCode != 201 || lift.SessionID == nil || lift.FitbitWorkout != "w1" || lift.Date != "2026-10-20" {
		t.Fatalf("linked lift = %d %s", res.StatusCode, out)
	}
	hub := *lift.SessionID
	res, out = e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"Whiteboard"}`, jsonBody...)
	if res.StatusCode != 201 || !strings.Contains(out, fmt.Sprintf(`"session_id":%d`, hub)) {
		t.Fatalf("note = %d %s", res.StatusCode, out)
	}
	_, out = e.do(t, athlete, "GET", "/api/training/sessions", "")
	var list []sessions.Session
	json.Unmarshal([]byte(out), &list)
	if len(list) != 1 || list[0].ID != hub || len(list[0].Sets) != 0 || len(list[0].Workouts) != 1 || list[0].Workouts[0].ID != "w1" {
		t.Fatalf("trainings = %s", out)
	}

	// The workout has its training: a new one can't take it.
	sets := `"sets":[{"lift":"Back Squat","reps":5,"kg":100}]`
	if res, out := e.do(t, athlete, "POST", "/api/training/sessions", `{"date":"2026-10-20","workouts":[{"id":"w1"}],`+sets+`}`, jsonBody...); res.StatusCode != 409 || !strings.Contains(out, "workout_taken") {
		t.Errorf("second training for w1 = %d %s", res.StatusCode, out)
	}
	// Sets go into the one it has.
	path := fmt.Sprintf("/api/training/sessions/%d", hub)
	if res, out := e.do(t, athlete, "PUT", path, `{"date":"2026-10-20","workouts":[{"id":"w1"}],`+sets+`}`, jsonBody...); res.StatusCode != 200 || !strings.Contains(out, `"Back Squat"`) {
		t.Errorf("sets into it = %d %s", res.StatusCode, out)
	}

	// A lift unlinked from the workout leaves the training; one naming a
	// training that isn't there is refused.
	unlinked := fmt.Sprintf(`{"lift":"Snatch","kg":60,"reps":1,"date":"2026-10-20"}`)
	if res, out := e.do(t, athlete, "PUT", fmt.Sprintf("/api/lifts/%d", lift.ID), unlinked, jsonBody...); res.StatusCode != 200 || strings.Contains(out, "session_id") {
		t.Errorf("unlinked = %d %s", res.StatusCode, out)
	}
	if res, _ := e.do(t, athlete, "POST", "/api/lifts", `{"lift":"Snatch","kg":60,"reps":1,"date":"2026-10-20","session_id":999}`, jsonBody...); res.StatusCode != 400 {
		t.Errorf("unknown training = %d", res.StatusCode)
	}
	if res, out := e.do(t, athlete, "POST", "/api/lifts", fmt.Sprintf(`{"lift":"Clean","kg":80,"reps":1,"date":"2026-10-20","session_id":%d}`, hub), jsonBody...); res.StatusCode != 201 || !strings.Contains(out, `"fitbit_workout":"w1"`) {
		t.Errorf("lift into the training = %d %s", res.StatusCode, out)
	}
}

// A note reached by its training (PLAN 14b): one without workouts has no
// other key, and a form not saved yet uploads its images as "new".
func TestNotesByTraining(t *testing.T) {
	e := newTestEnv(t, true)
	athlete := e.lifter(t)
	trainer := e.writer(t)
	anna := e.signIn(t, "anna", viewerPassword, false)

	res, out := e.do(t, athlete, "POST", "/api/training/sessions", `{"date":"2026-10-18","workouts":[],"sets":[]}`, jsonBody...)
	var x sessions.Session
	json.Unmarshal([]byte(out), &x)
	if res.StatusCode != 201 || x.ID == 0 || len(x.Sets) != 0 {
		t.Fatalf("empty training = %d %s", res.StatusCode, out)
	}
	key := fmt.Sprintf("/api/notes/training-%d", x.ID)
	res, out = e.do(t, trainer, "PUT", key, `{"markdown":"Open gym, no watch"}`, jsonBody...)
	if res.StatusCode != 201 || !strings.Contains(out, fmt.Sprintf(`"session_id":%d`, x.ID)) || !strings.Contains(out, `"date":"2026-10-18"`) {
		t.Fatalf("note by training = %d %s", res.StatusCode, out)
	}
	if _, out := e.do(t, anna, "GET", "/api/notes", ""); !strings.Contains(out, "Open gym") {
		t.Errorf("a viewer's notes = %s", out)
	}

	// The training of a workout is reached by both keys.
	res, out = e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"Whiteboard"}`, jsonBody...)
	var n notes.Note
	json.Unmarshal([]byte(out), &n)
	if res.StatusCode != 201 {
		t.Fatalf("note by workout = %d %s", res.StatusCode, out)
	}
	if res, out := e.do(t, trainer, "PUT", fmt.Sprintf("/api/notes/training-%d", n.SessionID), `{"markdown":"Changed"}`, jsonBody...); res.StatusCode != 200 || !strings.Contains(out, `"Changed"`) {
		t.Errorf("same note by training = %d %s", res.StatusCode, out)
	}

	// Keys that name nothing; "new" takes images and nothing else.
	for _, path := range []string{"/api/notes/training-999", "/api/notes/training-0", "/api/notes/training-x", "/api/notes/new"} {
		if res, _ := e.do(t, trainer, "PUT", path, `{"markdown":"x"}`, jsonBody...); res.StatusCode != 404 {
			t.Errorf("PUT %s = %d, want 404", path, res.StatusCode)
		}
	}
	img := `{"image":"` + uploadImage(t, "") + `"}`
	if res, out := e.do(t, trainer, "POST", "/api/notes/new/images", img, jsonBody...); res.StatusCode != 201 {
		t.Errorf("image for a new training = %d %s", res.StatusCode, out)
	}
	if res, _ := e.do(t, trainer, "POST", "/api/notes/training-999/images", img, jsonBody...); res.StatusCode != 404 {
		t.Errorf("image for no training = %d", res.StatusCode)
	}

	// Deleted by its training; then it's gone.
	if res, _ := e.do(t, trainer, "DELETE", key, "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if res, _ := e.do(t, trainer, "DELETE", key, "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("delete again = %d", res.StatusCode)
	}
}
