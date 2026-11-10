package lifts

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

var now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

func TestClean(t *testing.T) {
	good, err := Input{Lift: "  Power   Clean ", Kg: 82.499, Date: "2026-11-10", Note: " felt\tfast "}.Clean(now)
	if err != nil || good.Lift != "Power Clean" || good.Kg != 82.5 || good.Reps != 1 || good.Note != "felt fast" {
		t.Fatalf("clean = %+v, %v", good, err)
	}
	ok := Input{Lift: "Split Jerk", Kg: 70, Reps: 3, Date: "2026-11-11"} // tomorrow in UTC: today somewhere east
	if _, err := ok.Clean(now); err != nil {
		t.Errorf("tomorrow: %v", err)
	}
	long := ""
	for range MaxName + 1 {
		long += "x"
	}
	for name, in := range map[string]Input{
		"no name":    {Kg: 60, Date: "2026-11-01"},
		"long name":  {Lift: long, Kg: 60, Date: "2026-11-01"},
		"control":    {Lift: "Clean\x00", Kg: 60, Date: "2026-11-01"},
		"no weight":  {Lift: "Clean", Date: "2026-11-01"},
		"negative":   {Lift: "Clean", Kg: -5, Date: "2026-11-01"},
		"too heavy":  {Lift: "Clean", Kg: MaxKg + 1, Date: "2026-11-01"},
		"many reps":  {Lift: "Clean", Kg: 60, Reps: MaxReps + 1, Date: "2026-11-01"},
		"minus reps": {Lift: "Clean", Kg: 60, Reps: -1, Date: "2026-11-01"},
		"bad date":   {Lift: "Clean", Kg: 60, Date: "1.11.2026"},
		"future":     {Lift: "Clean", Kg: 60, Date: "2026-11-12"},
		"long ago":   {Lift: "Clean", Kg: 60, Date: "1900-01-01"},
	} {
		if _, err := in.Clean(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
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

	if list, err := s.List(ctx); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty list = %#v, %v", list, err)
	}
	first, err := s.Add(ctx, Input{Lift: "Strict Press", Kg: 55, Reps: 1, Date: "2026-10-01"}, anna.ID)
	if err != nil || first.ID == 0 || first.CreatedBy != "anna" || !first.CreatedAt.Equal(now) {
		t.Fatalf("add = %+v, %v", first, err)
	}
	s.Add(ctx, Input{Lift: "back squat", Kg: 120, Reps: 1, Date: "2026-09-01"}, anna.ID)
	s.Add(ctx, Input{Lift: "Strict Press", Kg: 50, Reps: 1, Date: "2026-08-01"}, anna.ID)

	clock = now.Add(time.Hour)
	changed, err := s.Update(ctx, first.ID, Input{Lift: "Strict Press", Kg: 57.5, Reps: 1, Date: "2026-10-01", Note: "PB"}, bob.ID)
	if err != nil || changed.Kg != 57.5 || changed.Note != "PB" || changed.CreatedBy != "anna" || changed.UpdatedBy != "bob" || !changed.UpdatedAt.Equal(clock) {
		t.Fatalf("update = %+v, %v", changed, err)
	}
	if _, err := s.Update(ctx, 999, Input{Lift: "x", Kg: 1, Reps: 1, Date: "2026-10-01"}, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of nothing = %v", err)
	}

	// By lift, ignoring case, then by date.
	list, _ := s.List(ctx)
	var order []string
	for _, e := range list {
		order = append(order, e.Lift+" "+e.Date)
	}
	if want := "back squat 2026-09-01,Strict Press 2026-08-01,Strict Press 2026-10-01"; strings.Join(order, ",") != want {
		t.Errorf("order = %s", strings.Join(order, ","))
	}

	gone, err := s.Delete(ctx, first.ID)
	if err != nil || gone.Kg != 57.5 {
		t.Fatalf("delete = %+v, %v", gone, err)
	}
	if _, err := s.Delete(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 2 {
		t.Errorf("after delete: %d entries", len(list))
	}
}
