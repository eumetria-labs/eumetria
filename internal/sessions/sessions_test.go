package sessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/lifts"
)

var ctx = context.Background()
var now = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func set(lift, typ string, reps int, kg float64) Set {
	return Set{Lift: lift, Type: typ, Reps: reps, Kg: kg}
}

func TestClean(t *testing.T) {
	in, err := Input{Date: "2026-11-18", Title: "  Squat   day ", Sets: []Set{set(" Back  Squat", "", 5, 100.004), set("Pull-up", Warmup, 8, 0)}, Bests: []int{0, 0}}.Clean(now)
	if err != nil {
		t.Fatal(err)
	}
	if in.Title != "Squat day" || in.Sets[0].Lift != "Back Squat" || in.Sets[0].Type != Work || in.Sets[0].Kg != 100 || len(in.Bests) != 1 {
		t.Errorf("clean = %+v", in)
	}
	r := 11
	for _, bad := range []Input{
		{Date: "2026-11-18", Workouts: []Ref{{Source: "garmin", ID: "1"}}}, // no such source yet
		{Date: "2026-11-18", Workouts: []Ref{{ID: "a b"}}},
		{Date: "2026-11-25", Sets: []Set{set("A", "", 1, 1)}},
		{Date: "2026-11-18", Sets: []Set{set("A", "", 0, 1)}},
		{Date: "2026-11-18", Sets: []Set{set("A", "", 1, -1)}},
		{Date: "2026-11-18", Sets: []Set{set("", "", 1, 1)}},
		{Date: "2026-11-18", RPE: &r, Sets: []Set{set("A", "", 1, 1)}},
		{Date: "2026-11-18", Sets: []Set{set("A", Warmup, 1, 1)}, Bests: []int{0}}, // a warm-up isn't a best
		{Date: "2026-11-18", Sets: []Set{set("A", "", 1, 0)}, Bests: []int{0}},     // nor bodyweight
		{Date: "2026-11-18", Sets: []Set{set("A", "", 1, 1)}, Bests: []int{1}},
	} {
		if _, err := bad.Clean(now); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestStore(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u, _ := auth.NewStore(d).CreateUser(ctx, "anna", "a long enough password", auth.RoleAdmin)
	s := &Store{DB: d, Now: func() time.Time { return now }}
	ls := &lifts.Store{DB: d, Now: s.Now}
	in, _ := Input{Date: "2026-11-18", Sets: []Set{set("Back Squat", Warmup, 5, 60), set("Back Squat", "", 5, 100), set("Back Squat", "", 3, 110)}, Bests: []int{2}}.Clean(now)
	x, err := s.Add(ctx, in, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Sets) != 3 || x.Sets[2].Kg != 110 || len(x.Bests) != 1 || x.CreatedBy != "anna" {
		t.Fatalf("added = %+v", x)
	}
	if v := x.Volumes()["Back Squat"]; v.Sets != 2 || v.Reps != 8 || v.Kg != 830 {
		t.Errorf("volume = %+v", v)
	}
	all, _ := ls.List(ctx)
	if len(all) != 1 || all[0].Kg != 110 || all[0].Reps != 3 || all[0].Date != "2026-11-18" {
		t.Errorf("the best = %+v", all)
	}
	// A change replaces the sets; the best stays.
	in.Sets, in.Bests = in.Sets[:2], nil
	if x, err = s.Update(ctx, x.ID, in, u.ID); err != nil || len(x.Sets) != 2 || len(x.Bests) != 1 {
		t.Errorf("updated = %+v, %v", x, err)
	}
	// Deleting the session leaves the best, without the link.
	if _, err := s.Delete(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	all, _ = ls.List(ctx)
	var link any
	d.QueryRowContext(ctx, `SELECT session_id FROM lifts`).Scan(&link)
	if len(all) != 1 || link != nil {
		t.Errorf("after delete: %+v, link %v", all, link)
	}
	var sets int
	d.QueryRowContext(ctx, `SELECT count(*) FROM training_sets`).Scan(&sets)
	if sets != 0 {
		t.Errorf("%d sets left", sets)
	}
	if _, err := s.Delete(ctx, x.ID); err != ErrNotFound {
		t.Errorf("delete again = %v", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 0 {
		t.Errorf("list = %+v", list)
	}
}

// Workouts: one training each; Move merges one into another; Resolve finds
// or makes the training of a workout.
func TestWorkouts(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u, _ := auth.NewStore(d).CreateUser(ctx, "anna", "a long enough password", auth.RoleAdmin)
	s := &Store{DB: d, Now: func() time.Time { return now }}
	fb := func(id string) Ref { return Ref{Source: Fitbit, ID: id} }

	// Nothing to add: an empty training is fine.
	empty, err := Input{Date: "2026-11-18", Workouts: []Ref{{ID: "run"}, {ID: "run"}}}.Clean(now)
	if err != nil || len(empty.Workouts) != 1 || empty.Workouts[0] != fb("run") {
		t.Fatalf("clean = %+v, %v", empty, err)
	}
	a, err := s.Add(ctx, empty, u.ID)
	if err != nil || len(a.Sets) != 0 || len(a.Workouts) != 1 {
		t.Fatalf("empty training = %+v, %v", a, err)
	}
	in, _ := Input{Date: "2026-11-18", Workouts: []Ref{fb("lift"), fb("run")}, Sets: []Set{set("Deadlift", "", 5, 100)}}.Clean(now)
	if _, err := s.Add(ctx, in, u.ID); err != ErrTaken {
		t.Errorf("taking run = %v", err)
	}
	in.Move = true
	b, err := s.Add(ctx, in, u.ID)
	if err != nil || len(b.Workouts) != 2 || b.Workouts[0] != fb("lift") {
		t.Fatalf("merged = %+v, %v", b, err)
	}
	if a, _ = s.get(ctx, a.ID); len(a.Workouts) != 0 {
		t.Errorf("run still in the first: %+v", a)
	}
	var old string
	d.QueryRowContext(ctx, `SELECT fitbit_workout FROM training_sessions WHERE id = ?`, b.ID).Scan(&old)
	if old != "lift" {
		t.Errorf("old column = %q", old)
	}

	// Resolve: the training it has; else one without workouts preferred;
	// else a new one.
	if id, err := s.Resolve(ctx, fb("run"), "2026-11-18", nil, u.ID); err != nil || id != b.ID {
		t.Errorf("resolve run = %d, %v", id, err)
	}
	if id, err := s.Resolve(ctx, fb("swim"), "2026-11-19", &a.ID, u.ID); err != nil || id != a.ID {
		t.Errorf("resolve swim into the empty one = %d, %v", id, err)
	}
	if id, err := s.Resolve(ctx, fb("bike"), "2026-11-19", &b.ID, u.ID); err != nil || id == a.ID || id == b.ID {
		t.Errorf("resolve bike = %d, %v", id, err)
	}
	if id, has, err := s.Lookup(ctx, fb("swim")); !has || id != a.ID || err != nil {
		t.Errorf("lookup swim = %d %v %v", id, has, err)
	}
	if _, has, _ := s.Lookup(ctx, fb("none")); has {
		t.Error("lookup of none")
	}
	// Deleting a training frees its workouts.
	if _, err := s.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, has, _ := s.Lookup(ctx, fb("run")); has {
		t.Error("run still taken")
	}
}
