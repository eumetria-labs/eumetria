// Package db is the app's own database: one SQLite file (app.db in the data
// folder) for what people write through the app, such as users, sessions,
// notes and settings. The synced data stays in history.json (internal/store).
//
// The schema lives in migrations/NNNN_name.sql, compiled into the binary and
// applied in order when the database is opened. SQLite's user_version holds
// the number of the last one applied. A released migration is never edited;
// a change is a new file.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure Go: no cgo, so the image stays static
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB is the open database.
type DB struct {
	*sql.DB
}

// Open opens the database at file, creating it if needed, and applies the
// migrations it doesn't have yet.
func Open(ctx context.Context, file string) (*DB, error) {
	return open(ctx, file, migrations)
}

func open(ctx context.Context, file string, scripts fs.FS) (*DB, error) {
	// Every connection gets these. WAL lets the page read while a write is
	// going on; SQLite leaves foreign keys off unless asked; busy_timeout
	// makes a second writer wait instead of failing at once; immediate
	// transactions take the write lock up front, so two of them can't
	// deadlock upgrading from a read.
	dsn := "file:" + file + "?_txlock=immediate" +
		"&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d := &DB{sqldb}
	if err := d.migrate(ctx, scripts); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("database %s: %w", file, err)
	}
	return d, nil
}

type migration struct {
	version int
	name    string
}

// list returns the migrations in scripts, ordered by their number, and
// fails on a gap or a number used twice.
func list(scripts fs.FS) ([]migration, error) {
	names, err := fs.Glob(scripts, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, name := range names {
		num, _, _ := strings.Cut(path.Base(name), "_")
		v, err := strconv.Atoi(num)
		if err != nil || v < 1 {
			return nil, fmt.Errorf("migration %s: name must start with its number, e.g. 0001_", name)
		}
		ms = append(ms, migration{v, name})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected number %d", m.name, i+1)
		}
	}
	return ms, nil
}

// migrate applies the missing migrations, each in a transaction of its own
// together with the new user_version, so a failed one leaves the database
// at the version before it.
func (d *DB) migrate(ctx context.Context, scripts fs.FS) error {
	ms, err := list(scripts)
	if err != nil {
		return err
	}
	have, err := d.Version(ctx)
	if err != nil {
		return err
	}
	if have > len(ms) {
		return fmt.Errorf("schema version %d is newer than this program knows (%d); use a newer image", have, len(ms))
	}
	for _, m := range ms[have:] {
		script, err := fs.ReadFile(scripts, m.name)
		if err != nil {
			return err
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		// A pragma takes no parameters; the version is a number we made.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// Version is the number of the last migration applied.
func (d *DB) Version(ctx context.Context) (int, error) {
	var v int
	err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// Snapshot writes a consistent copy of the database to file, which must not
// exist yet. Unlike copying app.db, it includes what is still in the WAL and
// can run while the app writes.
func (d *DB) Snapshot(ctx context.Context, file string) error {
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("snapshot %s: file exists", file)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_, err := d.ExecContext(ctx, "VACUUM INTO ?", file)
	return err
}
