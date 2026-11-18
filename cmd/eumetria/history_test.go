package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/db"
	"eumetria/internal/demo"
	"eumetria/internal/store"
)

func TestHistoryGoesToTheDatabaseToo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config{historyPath: filepath.Join(dir, "history.json"), dbPath: filepath.Join(dir, "app.db")}

	// Without a database (a one-off sync) only history.json is written.
	h := demo.History(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 30)
	if err := saveHistory(ctx, cfg, h); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if ok, _ := store.HasSynced(ctx, d.DB); ok {
		t.Fatal("a database appeared without one being there")
	}

	// With one, each save writes it too, and -check-store agrees.
	useDatabase(cfg.dbPath, d)
	if err := saveHistory(ctx, cfg, h); err != nil {
		t.Fatal(err)
	}
	if err := checkStore(ctx, cfg); err != nil {
		t.Fatalf("check after a save: %v", err)
	}

	// A change only in history.json is found, by record.
	var day string
	for k := range h.Days {
		day = k
		break
	}
	h.Days[day].Steps++
	if err := h.Save(cfg.historyPath); err != nil {
		t.Fatal(err)
	}
	err = checkStore(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "1 differences") {
		t.Fatalf("check after a change only in history.json = %v", err)
	}
	if diffs := compareHistories(h, mustLoadDB(t, d)); len(diffs) != 1 || diffs[0] != "day "+day+": differs" {
		t.Errorf("differences = %q", diffs)
	}
}

func mustLoadDB(t *testing.T, d *db.DB) *store.History {
	t.Helper()
	h, err := store.LoadDB(context.Background(), d.DB)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
