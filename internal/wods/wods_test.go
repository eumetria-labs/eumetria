package wods

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestCleanWorkout(t *testing.T) {
	in, err := WorkoutInput{Name: "  Fran ", Scoring: Time, Description: "\n 21-15-9 \r\n\n\n Thrusters 43 kg\nPull-ups  \n\n"}.Clean()
	if err != nil || in.Name != "Fran" || in.Description != "21-15-9\n\nThrusters 43 kg\nPull-ups" {
		t.Errorf("clean = %q %q, %v", in.Name, in.Description, err)
	}
	for _, bad := range []WorkoutInput{
		{Name: " ", Scoring: Time},
		{Name: "Fran", Scoring: "points"},
		{Name: strings.Repeat("x", MaxName+1), Scoring: Time},
		{Name: "Fran", Scoring: Time, Description: "tab\there"},
		{Name: "Fran", Scoring: Time, Description: strings.Repeat("x", MaxDescription+1)},
	} {
		if _, err := bad.Clean(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}

func TestCleanResult(t *testing.T) {
	ok := []struct {
		scoring string
		in      ResultInput
		value   float64
		reps    int
	}{
		{Time, ResultInput{Value: 252, Reps: 4, Date: "2026-11-10"}, 252, 0}, // reps only count for rounds
		{Rounds, ResultInput{Value: 12, Reps: 5, Date: "2026-11-10"}, 12, 5},
		{Rounds, ResultInput{Value: 0, Reps: 25, Date: "2026-11-10"}, 0, 25},
		{Reps, ResultInput{Value: 150, Date: "2026-11-10"}, 150, 0},
		{Load, ResultInput{Value: 80.004, Date: "2026-11-11"}, 80, 0},
	}
	for _, c := range ok {
		got, err := c.in.Clean(c.scoring, now)
		if err != nil || got.Value != c.value || got.Reps != c.reps {
			t.Errorf("%s %+v = %+v, %v", c.scoring, c.in, got, err)
		}
	}
	bad := []struct {
		scoring string
		in      ResultInput
	}{
		{Time, ResultInput{Value: 12.5, Date: "2026-11-10"}},
		{Time, ResultInput{Value: 0, Date: "2026-11-10"}},
		{Time, ResultInput{Value: MaxSeconds + 1, Date: "2026-11-10"}},
		{Rounds, ResultInput{Value: 0, Reps: 0, Date: "2026-11-10"}},
		{Rounds, ResultInput{Value: 3, Reps: -1, Date: "2026-11-10"}},
		{Reps, ResultInput{Value: 0, Date: "2026-11-10"}},
		{Load, ResultInput{Value: MaxKg + 1, Date: "2026-11-10"}},
		{Load, ResultInput{Value: 80, Date: "2026-11-13"}},
		{Load, ResultInput{Value: 80, Date: "10.11.2026"}},
		{"points", ResultInput{Value: 1, Date: "2026-11-10"}},
	}
	for _, c := range bad {
		if _, err := c.in.Clean(c.scoring, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %+v: err = %v", c.scoring, c.in, err)
		}
	}
}

func TestBetterAndFormat(t *testing.T) {
	r := func(v float64, reps int) Result { return Result{Value: v, Reps: reps} }
	if !Better(Time, r(240, 0), r(252, 0)) || Better(Time, r(252, 0), r(240, 0)) {
		t.Error("time: fewer seconds are better")
	}
	if !Better(Rounds, r(12, 5), r(12, 3)) || !Better(Rounds, r(13, 0), r(12, 9)) || Better(Rounds, r(12, 3), r(12, 3)) {
		t.Error("rounds: rounds first, then reps")
	}
	if !Better(Load, r(82.5, 0), r(80, 0)) || !Better(Reps, r(151, 0), r(150, 0)) {
		t.Error("load and reps: more is better")
	}
	for want, got := range map[string]string{
		"4:12":               Format(Time, r(252, 0)),
		"1:02:05":            Format(Time, r(3725, 0)),
		"12 rounds + 5 reps": Format(Rounds, r(12, 5)),
		"20 rounds":          Format(Rounds, r(20, 0)),
		"150 reps":           Format(Reps, r(150, 0)),
		"82.5 kg":            Format(Load, r(82.5, 0)),
	} {
		if got != want {
			t.Errorf("format = %q, want %q", got, want)
		}
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	accounts := auth.NewStore(d)
	anna, _ := accounts.CreateUser(ctx, "anna", "a viewer with a long password", auth.RoleViewer)
	bob, _ := accounts.CreateUser(ctx, "bob", "another long enough password", auth.RoleViewer)
	clock := now
	s := &Store{DB: d, Now: func() time.Time { return clock }}

	if ws, err := s.Workouts(ctx); err != nil || ws == nil || len(ws) != 0 {
		t.Fatalf("no workouts = %#v, %v", ws, err)
	}
	if rs, err := s.Results(ctx); err != nil || rs == nil || len(rs) != 0 {
		t.Fatalf("no results = %#v, %v", rs, err)
	}
	fran, err := s.AddWorkout(ctx, WorkoutInput{Name: "Fran", Scoring: Time, Description: "21-15-9"}, anna.ID)
	if err != nil || fran.ID == 0 || fran.CreatedBy != "anna" {
		t.Fatalf("add workout = %+v, %v", fran, err)
	}
	if _, err := s.AddWorkout(ctx, WorkoutInput{Name: "FRAN", Scoring: Reps}, anna.ID); !errors.Is(err, ErrNameTaken) {
		t.Errorf("same name: err = %v", err)
	}
	cindy, _ := s.AddWorkout(ctx, WorkoutInput{Name: "Cindy", Scoring: Reps}, anna.ID)

	// No results yet: the scoring may still change.
	clock = now.Add(time.Hour)
	cindy, err = s.UpdateWorkout(ctx, cindy.ID, WorkoutInput{Name: "Cindy", Scoring: Rounds, Description: "20 min AMRAP"}, bob.ID)
	if err != nil || cindy.Scoring != Rounds || cindy.UpdatedBy != "bob" || !cindy.UpdatedAt.Equal(clock) {
		t.Fatalf("update workout = %+v, %v", cindy, err)
	}
	if _, err := s.UpdateWorkout(ctx, cindy.ID, WorkoutInput{Name: "Fran", Scoring: Rounds}, bob.ID); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename to a taken name: err = %v", err)
	}
	if _, err := s.UpdateWorkout(ctx, 999, WorkoutInput{Name: "X", Scoring: Time}, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("update a missing workout: err = %v", err)
	}

	r1, err := s.AddResult(ctx, cindy.ID, ResultInput{Value: 18, Reps: 7, Scaled: true, Date: "2026-11-01", Note: "ring rows"}, anna.ID)
	if err != nil || r1.Workout != cindy.ID || !r1.Scaled || r1.Reps != 7 || r1.CreatedBy != "anna" {
		t.Fatalf("add result = %+v, %v", r1, err)
	}
	if _, err := s.AddResult(ctx, 999, ResultInput{Value: 1, Date: "2026-11-01"}, anna.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("result of a missing workout: err = %v", err)
	}
	s.AddResult(ctx, fran.ID, ResultInput{Value: 300, Date: "2026-10-01"}, anna.ID)
	if _, err := s.UpdateWorkout(ctx, cindy.ID, WorkoutInput{Name: "Cindy", Scoring: Reps}, bob.ID); !errors.Is(err, ErrHasResults) {
		t.Errorf("scoring of a workout with results: err = %v", err)
	}
	if w, err := s.UpdateWorkout(ctx, cindy.ID, WorkoutInput{Name: "Cindy (20 min)", Scoring: Rounds}, bob.ID); err != nil || w.Name != "Cindy (20 min)" {
		t.Errorf("rename with results = %+v, %v", w, err)
	}
	if w, err := s.ResultWorkout(ctx, r1.ID); err != nil || w.ID != cindy.ID {
		t.Errorf("result's workout = %+v, %v", w, err)
	}

	changed, err := s.UpdateResult(ctx, r1.ID, ResultInput{Value: 19, Reps: 0, Date: "2026-11-02"}, bob.ID)
	if err != nil || changed.Value != 19 || changed.Scaled || changed.Note != "" || changed.UpdatedBy != "bob" {
		t.Errorf("update result = %+v, %v", changed, err)
	}
	if _, err := s.UpdateResult(ctx, 999, ResultInput{Value: 1, Date: "2026-11-01"}, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("update a missing result: err = %v", err)
	}
	rs, _ := s.Results(ctx)
	if len(rs) != 2 || rs[0].Workout != fran.ID || rs[1].ID != r1.ID {
		t.Errorf("results, the oldest first = %+v", rs)
	}
	if gone, err := s.DeleteResult(ctx, r1.ID); err != nil || gone.ID != r1.ID {
		t.Errorf("delete result = %+v, %v", gone, err)
	}
	if _, err := s.DeleteResult(ctx, r1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete it again: err = %v", err)
	}

	// Deleting a workout takes its results along.
	w, n, err := s.DeleteWorkout(ctx, fran.ID)
	if err != nil || w.Name != "Fran" || n != 1 {
		t.Errorf("delete workout = %+v %d, %v", w, n, err)
	}
	if rs, _ := s.Results(ctx); len(rs) != 0 {
		t.Errorf("results left = %+v", rs)
	}
	if ws, _ := s.Workouts(ctx); len(ws) != 1 || ws[0].ID != cindy.ID {
		t.Errorf("workouts left = %+v", ws)
	}
	if _, _, err := s.DeleteWorkout(ctx, fran.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete it again: err = %v", err)
	}
}

func TestTimeCaps(t *testing.T) {
	// A cap belongs to workouts for time; capped results count reps.
	if in, err := (WorkoutInput{Name: "Chipper", Scoring: Time, TimeCap: 1200}).Clean(); err != nil || in.TimeCap != 1200 {
		t.Errorf("cap = %+v, %v", in, err)
	}
	if in, _ := (WorkoutInput{Name: "Cindy", Scoring: Rounds, TimeCap: 1200}).Clean(); in.TimeCap != 0 {
		t.Errorf("a cap on rounds = %d", in.TimeCap)
	}
	if _, err := (WorkoutInput{Name: "X", Scoring: Time, TimeCap: -1}).Clean(); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative cap: err = %v", err)
	}
	if in, err := (ResultInput{Value: 180, Capped: true, Date: "2026-11-10"}).Clean(Time, now); err != nil || !in.Capped || in.Value != 180 {
		t.Errorf("capped = %+v, %v", in, err)
	}
	if in, _ := (ResultInput{Value: 12, Capped: true, Date: "2026-11-10"}).Clean(Rounds, now); in.Capped {
		t.Error("only results for time are capped")
	}
	if _, err := (ResultInput{Value: 1.5, Capped: true, Date: "2026-11-10"}).Clean(Time, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("half a rep: err = %v", err)
	}
	fin, c180, c200 := Result{Value: 1500}, Result{Value: 180, Capped: true}, Result{Value: 200, Capped: true}
	if !Better(Time, fin, c200) || Better(Time, c200, fin) || !Better(Time, c200, c180) || Better(Time, c180, c200) {
		t.Error("a finished time beats capped results; capped ones count reps")
	}
	if got := Format(Time, c180); got != "capped, 180 reps" {
		t.Errorf("format = %q", got)
	}

	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	anna, _ := auth.NewStore(d).CreateUser(ctx, "anna", "a viewer with a long password", auth.RoleViewer)
	s := &Store{DB: d, Now: func() time.Time { return now }}
	w, err := s.AddWorkout(ctx, WorkoutInput{Name: "Chipper", Scoring: Time, TimeCap: 1200}, anna.ID)
	if err != nil || w.TimeCap != 1200 {
		t.Fatalf("stored cap = %+v, %v", w, err)
	}
	r, err := s.AddResult(ctx, w.ID, ResultInput{Value: 180, Capped: true, Date: "2026-11-10"}, anna.ID)
	if err != nil || !r.Capped {
		t.Fatalf("stored capped = %+v, %v", r, err)
	}
	if r, _ = s.UpdateResult(ctx, r.ID, ResultInput{Value: 1150, Date: "2026-11-10"}, anna.ID); r.Capped || r.Value != 1150 {
		t.Errorf("finished after all = %+v", r)
	}
}
