package main

// The synced data is moving from history.json into app.db (PLAN.md, phases
// 3 and 4). For now history.json is what is read; every save of it is also
// written to the database (internal/store, Mirror), and -check-store
// compares the two.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"

	"eumetria/internal/db"
	"eumetria/internal/store"
)

// One mirror per database file, with its connection, for the whole run:
// it remembers what it wrote, so a save writes only what changed.
var mirrors = struct {
	sync.Mutex
	byPath map[string]*store.Mirror
}{byPath: map[string]*store.Mirror{}}

// useDatabase lets the history's mirror use a connection that is open
// anyway (serve mode) instead of opening its own.
func useDatabase(path string, d *db.DB) {
	mirrors.Lock()
	defer mirrors.Unlock()
	if mirrors.byPath[path] == nil {
		mirrors.byPath[path] = &store.Mirror{DB: d.DB}
	}
}

// mirrorFor is the mirror of cfg's database, or nil when the data folder
// has none yet: a one-off sync doesn't make one.
func mirrorFor(ctx context.Context, cfg config) (*store.Mirror, error) {
	mirrors.Lock()
	defer mirrors.Unlock()
	if m := mirrors.byPath[cfg.dbPath]; m != nil {
		return m, nil
	}
	if cfg.dbPath == "" {
		return nil, nil
	}
	if _, err := os.Stat(cfg.dbPath); err != nil {
		return nil, nil
	}
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		return nil, err
	}
	m := &store.Mirror{DB: d.DB} // open for the rest of the run
	mirrors.byPath[cfg.dbPath] = m
	return m, nil
}

// saveHistory writes h to history.json, then the records that changed to
// the database. history.json is what counts for now: a failure with the
// database is logged, it doesn't fail the sync.
func saveHistory(ctx context.Context, cfg config, h *store.History) error {
	if err := h.Save(cfg.historyPath); err != nil {
		return err
	}
	mirrorHistory(ctx, cfg, h)
	return nil
}

func mirrorHistory(ctx context.Context, cfg config, h *store.History) {
	m, err := mirrorFor(ctx, cfg)
	if err == nil && m != nil {
		var c store.Counts
		if c, err = m.Save(ctx, h); err == nil && c.Written+c.Deleted > 0 {
			log.Printf("database: synced data %v", c)
		}
	}
	if err != nil {
		log.Printf("database: writing the synced data failed (history.json has it): %v", err)
	}
}

// checkStore compares history.json with the synced data in the database,
// record by record, and fails when they differ (-check-store).
func checkStore(ctx context.Context, cfg config) error {
	file, err := store.Load(cfg.historyPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", cfg.historyPath, err)
	}
	if _, err := os.Stat(cfg.dbPath); err != nil {
		return fmt.Errorf("no database at %s", cfg.dbPath)
	}
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer d.Close()
	saved, err := store.LoadDB(ctx, d.DB)
	if err != nil {
		return fmt.Errorf("reading the database: %w", err)
	}
	diffs := compareHistories(file, saved)
	sizes := fmt.Sprintf("%d days, %d foods, %d health days, %d Open Food Facts products, %d reviews",
		len(file.Days), len(file.Foods), len(file.Health), len(file.OFF), len(file.Reviews))
	if len(diffs) == 0 {
		fmt.Printf("history.json and the database hold the same: %s\n", sizes)
		return nil
	}
	for _, d := range diffs {
		fmt.Println(d)
	}
	return fmt.Errorf("%d differences between history.json (%s) and the database", len(diffs), sizes)
}

// compareHistories lists what differs between a and b, by part and key:
// "day 2026-11-17: differs", "food recipe:x: only in history.json".
func compareHistories(a, b *store.History) []string {
	var out []string
	part := func(name string, x, y map[string]json.RawMessage) {
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		list := make([]string, 0, len(keys))
		for k := range keys {
			list = append(list, k)
		}
		sort.Strings(list)
		for _, k := range list {
			p, inX := x[k]
			q, inY := y[k]
			switch {
			case !inY:
				out = append(out, fmt.Sprintf("%s %s: only in history.json", name, k))
			case !inX:
				out = append(out, fmt.Sprintf("%s %s: only in the database", name, k))
			case string(p) != string(q):
				out = append(out, fmt.Sprintf("%s %s: differs", name, k))
			}
		}
	}
	part("day", raw(a.Days), raw(b.Days))
	part("food", raw(a.Foods), raw(b.Foods))
	part("health", raw(a.Health), raw(b.Health))
	part("off", raw(a.OFF), raw(b.OFF))
	part("review", raw(a.Reviews), raw(b.Reviews))
	part("meta", raw(map[string]any{"health_from": a.HealthFrom, "devices": a.Devices, "health_sync": a.HealthSync, "review_state": a.ReviewState}),
		raw(map[string]any{"health_from": b.HealthFrom, "devices": b.Devices, "health_sync": b.HealthSync, "review_state": b.ReviewState}))
	return out
}

func raw[V any](m map[string]V) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}
