package db

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

var ctx = context.Background()

func scripts(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, sql := range files {
		m["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	return m
}

func version(t *testing.T, d *DB) int {
	t.Helper()
	v, err := d.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenAppliesTheEmbeddedMigrations(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.db")
	d, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := list(migrations)
	if v := version(t, d); v != len(ms) || v == 0 {
		t.Errorf("version = %d, want %d", v, len(ms))
	}
	if _, err := d.Exec("INSERT INTO settings (key, value) VALUES ('title', 'Eumetria')"); err != nil {
		t.Fatal(err)
	}
	var mode string
	var fk int
	d.QueryRow("PRAGMA journal_mode").Scan(&mode)
	d.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if mode != "wal" || fk != 1 {
		t.Errorf("journal_mode = %q, foreign_keys = %d; want wal, 1", mode, fk)
	}
	d.Close()

	// Opening again applies nothing twice and keeps the data.
	d, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var title string
	if err := d.QueryRow("SELECT value FROM settings WHERE key = 'title'").Scan(&title); err != nil || title != "Eumetria" {
		t.Errorf("after reopening: %q, %v", title, err)
	}
}

func TestMigrationsRunInOrderAndOnlyOnce(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.db")
	first := scripts(map[string]string{
		"0002_b.sql": "ALTER TABLE a ADD COLUMN b TEXT;",
		"0001_a.sql": "CREATE TABLE a (id INTEGER PRIMARY KEY);",
	})
	d, err := open(ctx, file, first)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, d); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
	d.Close()

	// A later release adds a third; the first two must not run again (the
	// ALTER would fail on the existing column).
	first["migrations/0003_c.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE c (x TEXT); INSERT INTO c VALUES ('one'), ('two');")}
	d, err = open(ctx, file, first)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	d.QueryRow("SELECT count(*) FROM c").Scan(&n)
	if v := version(t, d); v != 3 || n != 2 {
		t.Errorf("version = %d, rows = %d; want 3, 2", v, n)
	}
}

func TestAFailedMigrationChangesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.db")
	good := scripts(map[string]string{"0001_a.sql": "CREATE TABLE a (id INTEGER PRIMARY KEY);"})
	d, err := open(ctx, file, good)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()

	// The second statement fails: the table of the first must not stay.
	bad := scripts(map[string]string{
		"0001_a.sql": "CREATE TABLE a (id INTEGER PRIMARY KEY);",
		"0002_b.sql": "CREATE TABLE b (id INTEGER); INSERT INTO nowhere VALUES (1);",
	})
	if _, err := open(ctx, file, bad); err == nil || !strings.Contains(err.Error(), "0002_b.sql") {
		t.Fatalf("err = %v, want one naming 0002_b.sql", err)
	}
	d, err = open(ctx, file, good)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	d.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'b'").Scan(&n)
	if v := version(t, d); v != 1 || n != 0 {
		t.Errorf("version = %d, table b exists = %d; want 1, 0", v, n)
	}
}

func TestANewerDatabaseIsRefused(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app.db")
	d, err := open(ctx, file, scripts(map[string]string{
		"0001_a.sql": "CREATE TABLE a (id INTEGER);",
		"0002_b.sql": "CREATE TABLE b (id INTEGER);",
	}))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	// An older image must not run on a database a newer one has changed.
	if _, err := open(ctx, file, scripts(map[string]string{"0001_a.sql": "CREATE TABLE a (id INTEGER);"})); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestMigrationNamesAreChecked(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no number": {"0001_a.sql": "", "init.sql": ""},
		"a gap":     {"0001_a.sql": "", "0003_c.sql": ""},
		"twice":     {"0001_a.sql": "", "0001_b.sql": ""},
	} {
		if _, err := list(scripts(files)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSnapshotIsAConsistentCopy(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO settings (key, value) VALUES ('k', 'v')"); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snapshot.db")
	if err := d.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if err := d.Snapshot(ctx, snap); err == nil {
		t.Error("a second snapshot overwrote the first")
	}
	s, err := Open(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v string
	if err := s.QueryRow("SELECT value FROM settings WHERE key = 'k'").Scan(&v); err != nil || v != "v" {
		t.Errorf("snapshot has %q, %v", v, err)
	}
}

func TestWritersWaitForEachOther(t *testing.T) {
	d, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Read-then-write transactions from many goroutines: without immediate
	// transactions and busy_timeout some would fail with "database is locked".
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			tx, err := d.Begin()
			if err != nil {
				errs <- err
				return
			}
			var n int
			tx.QueryRow("SELECT count(*) FROM settings").Scan(&n)
			if _, err := tx.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", "k"+string(rune('a'+i)), "v"); err != nil {
				tx.Rollback()
				errs <- err
				return
			}
			errs <- tx.Commit()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	d.QueryRow("SELECT count(*) FROM settings").Scan(&n)
	if n != 20 {
		t.Errorf("%d rows, want 20", n)
	}
}
