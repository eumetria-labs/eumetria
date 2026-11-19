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
		{Date: "2026-11-18"}, // no sets
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
