package store_test

import (
	"testing"
	"time"

	"eumetria/internal/store"
)

func TestQueries(t *testing.T) {
	d := openDB(t)
	h := sample()
	m := &store.Mirror{DB: d.DB}
	before := time.Now().Add(-time.Second)
	if _, err := m.Save(ctx, h); err != nil {
		t.Fatal(err)
	}
	if at, err := store.SavedAt(ctx, d.DB); err != nil || at.Before(before) {
		t.Errorf("SavedAt = %v, %v", at, err)
	}

	days, err := store.DailyMetrics(ctx, d.DB, "2026-11-01", "2026-11-17")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 17 || days[0].Date != "2026-11-01" || days[16].Date != "2026-11-17" {
		t.Fatalf("days = %d, from %v", len(days), days[0].Date)
	}
	// Each day's numbers are the history's.
	var workouts int
	for _, m := range days {
		y, hd := h.Days[m.Date], h.Health[m.Date]
		if y != nil && (m.Energy != y.Total.Energy || m.Steps != y.Steps || (y.Weight == nil) != (m.Weight == nil)) {
			t.Errorf("%s: energy %v steps %v, want %v %v", m.Date, m.Energy, m.Steps, y.Total.Energy, y.Steps)
		}
		if hd != nil {
			if m.RestingHR != hd.RestingHR || m.Workouts != len(hd.Workouts) {
				t.Errorf("%s: resting HR %v workouts %d, want %v %d", m.Date, m.RestingHR, m.Workouts, hd.RestingHR, len(hd.Workouts))
			}
			if hd.Sleep != nil && m.Asleep != hd.Sleep.Asleep {
				t.Errorf("%s: asleep %v, want %v", m.Date, m.Asleep, hd.Sleep.Asleep)
			}
			workouts += len(hd.Workouts)
		}
	}

	ws, err := store.Workouts(ctx, d.DB, "2026-11-01", "2026-11-17")
	if err != nil || len(ws) != workouts || workouts == 0 {
		t.Fatalf("workouts = %d, %v; want %d", len(ws), err, workouts)
	}
	for i := 1; i < len(ws); i++ {
		if ws[i].Start < ws[i-1].Start {
			t.Fatal("workouts not by start")
		}
	}

	day := h.Days["2026-11-17"]
	es, err := store.Entries(ctx, d.DB, "2026-11-17", "2026-11-17", "")
	if err != nil || len(es) != len(day.Items) {
		t.Fatalf("entries = %d, %v; want %d", len(es), err, len(day.Items))
	}
	for i, e := range es {
		it := day.Items[i]
		if e.Name != it.Name || e.Energy != it.Energy || (e.Fiber == nil) != (it.Fiber == nil) {
			t.Errorf("entry %d = %+v, want %+v", i, e, it)
		}
	}
	name := day.Items[0].Name
	one, _ := store.Entries(ctx, d.DB, "2026-10-01", "2026-11-17", name)
	for _, e := range one {
		if e.Name != name {
			t.Fatalf("entries of %q include %q", name, e.Name)
		}
	}
	if len(one) == 0 {
		t.Error("no entries of a food that was eaten")
	}
}
