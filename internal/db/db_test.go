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

// 0004 moves the two fixed roles into the roles table without losing the
// users' sessions (dropping a column must not cascade).
func TestRolesMigrationKeepsUsersAndSessions(t *testing.T) {
	first := fstest.MapFS{}
	for _, name := range []string{"0001_settings.sql", "0002_accounts.sql", "0003_second_factors.sql"} {
		data, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		first["migrations/"+name] = &fstest.MapFile{Data: data}
	}
	file := filepath.Join(t.TempDir(), "app.db")
	d, err := open(ctx, file, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO users (id, name, password_hash, role, created_at, password_at) VALUES (1, 'patrick', 'h', 'admin', 0, 0), (2, 'anna', 'h', 'viewer', 0, 0)",
		"INSERT INTO sessions (user_id, token_hash, remember, created_at, last_seen_at, expires_at) VALUES (1, 't1', 1, 0, 0, 0), (2, 't2', 1, 0, 0, 0)",
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	d, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got := map[string]string{}
	rows, err := d.Query("SELECT u.name, r.name FROM users u JOIN roles r ON r.id = u.role_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var u, r string
		rows.Scan(&u, &r)
		got[u] = r
	}
	rows.Close()
	if got["patrick"] != "admin" || got["anna"] != "viewer" {
		t.Errorf("roles after the migration = %v", got)
	}
	var n int
	if d.QueryRow("SELECT count(*) FROM sessions").Scan(&n); n != 2 {
		t.Errorf("%d sessions left, want 2", n)
	}
}

// 0033 puts everything noted for a Fitbit workout into one training: the
// session linked to it, else a new one; notes, lifts and benchmark results
// follow, a session's plain note joins the workout note, and two sessions
// on one workout leave the second without it.
func TestTrainingsMigration(t *testing.T) {
	first := fstest.MapFS{}
	all, err := list(migrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.version >= 33 {
			continue
		}
		data, err := migrations.ReadFile(m.name)
		if err != nil {
			t.Fatal(err)
		}
		first[m.name] = &fstest.MapFile{Data: data}
	}
	file := filepath.Join(t.TempDir(), "app.db")
	d, err := open(ctx, file, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO health_days (date, data) VALUES ('2026-11-10', '{}')",
		"INSERT INTO workouts (id, date) VALUES ('cf', '2026-11-10')",
		// Two sessions on cf; one with a plain note.
		"INSERT INTO training_sessions (id, date, fitbit_workout, note, created_at, updated_at) VALUES (1, '2026-11-10', 'cf', 'Felt heavy', 10, 10), (2, '2026-11-10', 'cf', '', 20, 20), (3, '2026-11-12', '', 'no watch', 30, 30)",
		"INSERT INTO notes (workout_id, markdown, created_at, updated_at) VALUES ('cf', '# WOD', 5, 5), ('gone', 'old', 1, 1)",
		"INSERT INTO lifts (id, lift, kg, reps, date, fitbit_workout, created_at, updated_at) VALUES (1, 'Snatch', 60, 1, '2026-11-10', 'cf', 0, 0), (2, 'Clean', 90, 1, '2026-11-11', 'run', 0, 0), (3, 'Jerk', 90, 1, '2026-11-01', '', 0, 0)",
		"INSERT INTO wods (id, name, scoring, created_at, updated_at) VALUES (1, 'Fran', 'time', 0, 0)",
		"INSERT INTO wod_results (id, wod_id, value, date, fitbit_workout, created_at, updated_at) VALUES (1, 1, 300, '2026-11-11', 'run', 0, 0)",
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	d.Close()

	d, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	of := func(workout string) (id int64) {
		d.QueryRow("SELECT session_id FROM training_workouts WHERE source = 'fitbit' AND workout_id = ?", workout).Scan(&id)
		return id
	}
	if of("cf") != 1 || of("run") == 0 || of("gone") == 0 || of("run") == of("gone") {
		t.Fatalf("trainings: cf %d, run %d, gone %d", of("cf"), of("run"), of("gone"))
	}
	var n int
	if d.QueryRow("SELECT count(*) FROM training_workouts WHERE session_id = 2").Scan(&n); n != 0 {
		t.Errorf("the second session on cf has %d workouts", n)
	}
	var date string
	d.QueryRow("SELECT date FROM training_sessions WHERE id = ?", of("gone")).Scan(&date)
	if date != "1970-01-01" {
		t.Errorf("a gone workout's training day = %q", date)
	}
	d.QueryRow("SELECT date FROM training_sessions WHERE id = ?", of("run")).Scan(&date)
	if date != "2026-11-11" {
		t.Errorf("run's training day = %q", date)
	}
	links := map[int64]int64{}
	rows, _ := d.Query("SELECT id, coalesce(session_id, 0) FROM lifts")
	for rows.Next() {
		var id, s int64
		rows.Scan(&id, &s)
		links[id] = s
	}
	rows.Close()
	if links[1] != 1 || links[2] != of("run") || links[3] != 0 {
		t.Errorf("lift links = %v", links)
	}
	var result int64
	if d.QueryRow("SELECT session_id FROM wod_results WHERE id = 1").Scan(&result); result != of("run") {
		t.Errorf("result link = %d", result)
	}
	notes := map[int64]string{}
	rows, _ = d.Query("SELECT session_id, markdown FROM training_notes")
	for rows.Next() {
		var id int64
		var md string
		rows.Scan(&id, &md)
		notes[id] = md
	}
	rows.Close()
	if len(notes) != 3 || notes[1] != "# WOD\n\nFelt heavy" || notes[of("gone")] != "old" || notes[3] != "no watch" {
		t.Errorf("notes = %v", notes)
	}
}
