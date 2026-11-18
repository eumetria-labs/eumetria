package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/demo"
	"eumetria/internal/store"
)

func historyConfig(t *testing.T) (config, string) {
	dir := t.TempDir()
	return config{dataDir: dir, dbPath: filepath.Join(dir, "app.db")}, dir
}

func sameHistory(t *testing.T, got, want *store.History) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("history differs (%d vs %d bytes)", len(a), len(b))
	}
}

func imported(t *testing.T, dir string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "history.json.imported-*"))
	if len(m) != 1 {
		t.Fatalf("renamed files = %v, want one", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
		t.Fatal("history.json is still there")
	}
	return m[0]
}

func TestHistoryLivesInTheDatabase(t *testing.T) {
	ctx := context.Background()
	cfg, dir := historyConfig(t)

	// A first sync makes the database and reads back what it saved.
	h := demo.History(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 30)
	if err := saveHistory(ctx, cfg, h); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
		t.Error("a save wrote history.json")
	}
	got, err := loadHistory(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sameHistory(t, got, h)
}

func TestHistoryFileIsImportedOnce(t *testing.T) {
	ctx := context.Background()
	cfg, dir := historyConfig(t)
	h := demo.History(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 30)
	if err := h.Save(filepath.Join(dir, "history.json")); err != nil {
		t.Fatal(err)
	}
	got, err := loadHistory(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sameHistory(t, got, h)
	imported(t, dir)
	// The next start reads the database alone.
	again, err := loadHistory(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sameHistory(t, again, h)
}

func TestOlderHistoryFileIsNotImported(t *testing.T) {
	ctx := context.Background()
	cfg, dir := historyConfig(t)
	newer := demo.History(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 30)
	if err := saveHistory(ctx, cfg, newer); err != nil {
		t.Fatal(err)
	}
	// A stray old copy (saved before the database's last save) doesn't
	// turn the synced data back; it is only renamed.
	old := demo.History(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 10)
	path := filepath.Join(dir, "history.json")
	if err := old.Save(path); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	os.Chtimes(path, past, past)
	got, err := loadHistory(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sameHistory(t, got, newer)
	if !strings.Contains(imported(t, dir), "history.json.imported-") {
		t.Error("not renamed")
	}
}
