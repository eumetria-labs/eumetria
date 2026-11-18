package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"eumetria/internal/db"
	"eumetria/internal/demo"
	"eumetria/internal/store"
)

var ctx = context.Background()

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// sample is a history with every kind of record: the demo's, plus what it
// lacks (a stale recipe entry, Open Food Facts, sync states).
func sample() *store.History {
	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	h := demo.History(today, 60)
	h.Devices = demo.Devices(today)
	h.HealthFrom = "2026-09-01"
	h.HealthSync = &store.SyncState{LastSuccess: today, LastAttempt: today}
	h.ReviewState = &store.ReviewState{LastAttempt: today, Error: "credit", Billing: true}
	h.OFF["4001234"] = &store.OFFProduct{Name: "Skyr", NutriScore: "a", NOVA: 3, FetchedAt: today}
	h.OFF["4009999"] = &store.OFFProduct{NotFound: true, FetchedAt: today}
	for _, d := range h.Days {
		if len(d.Items) > 0 {
			d.Items[0].RecipeStale = true
			break
		}
	}
	return h
}

func same(t *testing.T, got, want *store.History) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("the history read back differs from the one saved (%d vs %d bytes)", len(a), len(b))
	}
}

func TestMirrorRoundTrip(t *testing.T) {
	d := openDB(t)
	h := sample()
	m := &store.Mirror{DB: d.DB}
	c, err := m.Save(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if c.Written == 0 || c.Deleted != 0 {
		t.Fatalf("first save: %v", c)
	}
	got, err := store.LoadDB(ctx, d.DB)
	if err != nil {
		t.Fatal(err)
	}
	same(t, got, h)
	if ok, _ := store.HasSynced(ctx, d.DB); !ok {
		t.Error("HasSynced = false after a save")
	}

	// The index tables follow the days.
	var entries, workouts int
	d.QueryRow(`SELECT count(*) FROM food_entries`).Scan(&entries)
	d.QueryRow(`SELECT count(*) FROM workouts`).Scan(&workouts)
	wantEntries, wantWorkouts := 0, 0
	for _, day := range h.Days {
		wantEntries += len(day.Items)
	}
	for _, hd := range h.Health {
		wantWorkouts += len(hd.Workouts)
	}
	if entries != wantEntries || workouts != wantWorkouts || wantEntries == 0 || wantWorkouts == 0 {
		t.Errorf("food_entries %d (want %d), workouts %d (want %d)", entries, wantEntries, workouts, wantWorkouts)
	}
}

func TestMirrorWritesOnlyChanges(t *testing.T) {
	d := openDB(t)
	h := sample()
	m := &store.Mirror{DB: d.DB}
	if _, err := m.Save(ctx, h); err != nil {
		t.Fatal(err)
	}
	// Nothing changed: nothing written.
	if c, err := m.Save(ctx, h); err != nil || c.Written != 0 || c.Deleted != 0 {
		t.Fatalf("second save = %v, %v; want nothing written", c, err)
	}
	// One day changed, one food gone, one review new.
	var day string
	for k := range h.Days {
		day = k
		break
	}
	h.Days[day].Steps = 12345
	var food string
	for k := range h.Foods {
		food = k
		break
	}
	delete(h.Foods, food)
	if h.Reviews == nil {
		h.Reviews = map[string]*store.Review{}
	}
	h.Reviews["2026-11-09"] = &store.Review{Week: "2026-11-09", Text: "new", Model: "m", CreatedAt: time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)}
	c, err := m.Save(ctx, h)
	if err != nil || c.Written != 2 || c.Deleted != 1 {
		t.Fatalf("after changes = %v, %v; want 2 written, 1 deleted", c, err)
	}
	var steps float64
	d.QueryRow(`SELECT steps FROM yazio_days WHERE date = ?`, day).Scan(&steps)
	if steps != 12345 {
		t.Errorf("steps column = %v", steps)
	}
	got, _ := store.LoadDB(ctx, d.DB)
	same(t, got, h)

	// A new mirror (a restart) learns what is there and writes nothing.
	m2 := &store.Mirror{DB: d.DB}
	if c, err := m2.Save(ctx, h); err != nil || c.Written != 0 || c.Deleted != 0 {
		t.Fatalf("after a restart = %v, %v; want nothing written", c, err)
	}
}

func TestMirrorDeletesADayWithItsEntries(t *testing.T) {
	d := openDB(t)
	h := sample()
	m := &store.Mirror{DB: d.DB}
	m.Save(ctx, h)
	var day string
	for k, v := range h.Days {
		if len(v.Items) > 0 {
			day = k
			break
		}
	}
	delete(h.Days, day)
	if _, err := m.Save(ctx, h); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM food_entries WHERE date = ?`, day).Scan(&n)
	if n != 0 {
		t.Errorf("%d food entries left of a deleted day", n)
	}
}

func TestLoadDBEmpty(t *testing.T) {
	d := openDB(t)
	h, err := store.LoadDB(ctx, d.DB)
	if err != nil || len(h.Days) != 0 || h.Days == nil || h.Foods == nil || h.Health == nil || h.OFF == nil {
		t.Fatalf("empty = %+v, %v", h, err)
	}
	if ok, _ := store.HasSynced(ctx, d.DB); ok {
		t.Error("HasSynced = true on an empty database")
	}
}
