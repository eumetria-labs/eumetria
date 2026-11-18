package main

// The synced data (Yazio, Google Health, Open Food Facts, the weekly
// reviews) lives in the app's database: internal/store, migration
// 0024_synced.sql. Up to phase 4 of PLAN.md it lived in history.json; such a
// file, left from then or restored with an older backup, is imported once
// and renamed.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"eumetria/internal/db"
	"eumetria/internal/store"
)

// One mirror per database file, with its connection, for the whole run:
// it remembers what it wrote, so a save writes only what changed.
var mirrors = struct {
	sync.Mutex
	byPath map[string]*store.Mirror
}{byPath: map[string]*store.Mirror{}}

// useDatabase lets the history use a connection that is open anyway (serve
// mode) instead of opening its own.
func useDatabase(path string, d *db.DB) {
	mirrors.Lock()
	defer mirrors.Unlock()
	if mirrors.byPath[path] == nil {
		mirrors.byPath[path] = &store.Mirror{DB: d.DB}
	}
}

// mirrorFor is the mirror of cfg's database, which it makes when there is
// none yet (a first one-off sync).
func mirrorFor(ctx context.Context, cfg config) (*store.Mirror, error) {
	mirrors.Lock()
	defer mirrors.Unlock()
	if m := mirrors.byPath[cfg.dbPath]; m != nil {
		return m, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o700); err != nil {
		return nil, err
	}
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		return nil, err
	}
	m := &store.Mirror{DB: d.DB} // open for the rest of the run
	mirrors.byPath[cfg.dbPath] = m
	return m, nil
}

// loadHistory reads the synced data from the database, after importing a
// history.json the data folder still has.
func loadHistory(ctx context.Context, cfg config) (*store.History, error) {
	m, err := mirrorFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := importHistoryFile(ctx, cfg, m); err != nil {
		return nil, err
	}
	return store.LoadDB(ctx, m.DB)
}

// saveHistory writes the records of h that changed to the database.
func saveHistory(ctx context.Context, cfg config, h *store.History) error {
	m, err := mirrorFor(ctx, cfg)
	if err != nil {
		return err
	}
	c, err := m.Save(ctx, h)
	if err != nil {
		return err
	}
	if c.Written+c.Deleted > 0 {
		log.Printf("database: synced data %v", c)
	}
	return nil
}

// importHistoryFile copies <data>/history.json into the database and renames
// it to history.json.imported-<date>. Only a file newer than the database's
// last save is copied (the one phase 3 kept next to it, or one restored
// with an older backup); an older one is only renamed, so a stray copy
// can't turn back what was synced since.
func importHistoryFile(ctx context.Context, cfg config, m *store.Mirror) error {
	path := filepath.Join(filepath.Dir(cfg.dbPath), "history.json")
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	saved, err := store.SavedAt(ctx, m.DB)
	if err != nil {
		return err
	}
	if has, err := store.HasSynced(ctx, m.DB); err != nil {
		return err
	} else if !has || info.ModTime().After(saved) {
		h, err := store.Load(path)
		if err != nil {
			return fmt.Errorf("importing %s: %w", path, err)
		}
		c, err := m.Save(ctx, h)
		if err != nil {
			return fmt.Errorf("importing %s: %w", path, err)
		}
		log.Printf("database: imported %s (%d days, %d health days; %v)", path, len(h.Days), len(h.Health), c)
	} else {
		log.Printf("database: %s is older than the synced data (saved %s), not imported", path, saved.Format(time.DateTime))
	}
	to := path + ".imported-" + time.Now().Format("2006-01-02")
	if _, err := os.Stat(to); err == nil {
		to = path + ".imported-" + time.Now().Format("2006-01-02-150405")
	}
	if err := os.Rename(path, to); err != nil {
		return err
	}
	log.Printf("database: renamed it to %s; delete it once all is well", filepath.Base(to))
	return nil
}
