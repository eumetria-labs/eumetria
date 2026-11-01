package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eumetria/internal/backup"
	"eumetria/internal/db"
	"eumetria/internal/google"
	"eumetria/internal/settings"
)

// memDrive is a Drive that remembers.
type memDrive struct {
	mu    sync.Mutex
	files []google.DriveFile
	fail  error
	tests int
}

func (d *memDrive) Folder(context.Context, string) (string, error) { return "f", d.fail }
func (d *memDrive) Files(context.Context, string) ([]google.DriveFile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]google.DriveFile{}, d.files...), nil
}
func (d *memDrive) PutFile(_ context.Context, _, _, name string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files = append(d.files, google.DriveFile{ID: name, Name: name, Created: time.Now()})
	return name, nil
}
func (d *memDrive) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, f := range d.files {
		if f.ID == id {
			d.files = append(d.files[:i], d.files[i+1:]...)
		}
	}
	return nil
}
func (d *memDrive) Test(context.Context, string) error { d.tests++; return d.fail }

func useDrive(t *testing.T, d driveAPI, err error) {
	t.Helper()
	old := driveFor
	t.Cleanup(func() { driveFor = old })
	driveFor = func(config) (driveAPI, error) { return d, err }
}

func (e *testEnv) lastBackup(t *testing.T) backup.Run {
	t.Helper()
	e.runner.wait()
	runs, err := backup.Recent(context.Background(), e.runner.settings.DB.DB, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	return runs[0]
}

func archiveFiles(t *testing.T, r io.Reader) []string {
	t.Helper()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

func TestBackupByHand(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	anna := e.signIn(t, "anna", viewerPassword, false)
	for _, r := range [][2]string{{"GET", "/api/admin/backups"}, {"POST", "/api/admin/backups"}, {"POST", "/api/admin/backups/test-drive"}} {
		if res, _ := e.do(t, anna, r[0], r[1], "{}", sameSite); res.StatusCode != 403 {
			t.Errorf("a viewer: %s %s = %d", r[0], r[1], res.StatusCode)
		}
	}
	if res, _ := e.do(t, admin, "POST", "/api/admin/backups", "{}", sameSite); res.StatusCode != 202 {
		t.Fatalf("backup = %d", res.StatusCode)
	}
	run := e.lastBackup(t)
	if run.Error != "" || run.Encrypted || !strings.HasSuffix(run.File, ".tar.gz") || run.Size == 0 || run.FinishedAt.IsZero() || run.Drive != "" {
		t.Fatalf("run = %+v", run)
	}
	f, err := os.Open(filepath.Join(e.runner.cfg.backupDir, run.File))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	files := strings.Join(archiveFiles(t, f), " ")
	for _, want := range []string{"data/app.db", "data/photos/abc.jpg"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s isn't in %s", want, files)
		}
	}
	_, body := e.do(t, admin, "GET", "/api/admin/backups", "")
	if !strings.Contains(body, run.File) {
		t.Errorf("status = %s", body)
	}
}

func TestABackupWaitsForTheSync(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	gate := make(chan struct{})
	e.runner.backup = func(context.Context, config, func(context.Context, string) error, *backup.Run) error {
		<-gate
		return nil
	}
	if !e.runner.startBackup(context.Background()) {
		t.Fatal("no backup")
	}
	if e.runner.start(context.Background(), false) {
		t.Error("a sync started during the backup")
	}
	if res, _ := e.do(t, admin, "POST", "/api/admin/backups", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("a second backup = %d", res.StatusCode)
	}
	close(gate)
	e.runner.wait()
}

func TestAFailedBackupIsLogged(t *testing.T) {
	e := newTestEnv(t, true)
	e.runner.backup = func(context.Context, config, func(context.Context, string) error, *backup.Run) error {
		return errors.New("disk full")
	}
	e.runner.startBackup(context.Background())
	if run := e.lastBackup(t); run.Error != "disk full" || run.FinishedAt.IsZero() {
		t.Errorf("run = %+v", run)
	}
	if st := e.runner.status(); !strings.Contains(st.JobError, "disk full") {
		t.Errorf("status = %+v", st)
	}
}

func TestDriveNeedsAPassphrase(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	mem := &memDrive{}
	useDrive(t, mem, nil)

	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"backup_drive":"true"}}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_passphrase") {
		t.Fatalf("drive without a passphrase = %d %s", res.StatusCode, body)
	}
	// Both in one change is fine.
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"backup_drive":"true","backup_passphrase":"a long enough passphrase"}}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("with one = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"reset":["backup_passphrase"]}`, sameSite); res.StatusCode != 409 {
		t.Errorf("removing it while Drive is on = %d", res.StatusCode)
	}

	e.runner.startBackup(context.Background())
	run := e.lastBackup(t)
	if run.Error != "" || !run.Encrypted || run.Drive != "ok" || !strings.HasSuffix(run.File, ".enc") {
		t.Fatalf("run = %+v", run)
	}
	if len(mem.files) != 1 || mem.files[0].Name != run.File {
		t.Fatalf("drive = %+v", mem.files)
	}
	if res, body := e.do(t, admin, "POST", "/api/admin/backups/test-drive", "{}", sameSite); res.StatusCode != 200 || !strings.Contains(body, `"ok":true`) || mem.tests != 1 {
		t.Errorf("test = %d %s", res.StatusCode, body)
	}
}

func TestAFailedUploadKeepsTheArchive(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	useDrive(t, nil, errors.New("Google isn't set up"))
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"backup_drive":"true","backup_passphrase":"a long enough passphrase"}}`, sameSite)
	e.runner.startBackup(context.Background())
	run := e.lastBackup(t)
	if run.Error != "" || !strings.Contains(run.Drive, "isn't set up") {
		t.Fatalf("run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(e.runner.cfg.backupDir, run.File)); err != nil {
		t.Error(err)
	}
}

func TestAPlainArchiveNeverLeaves(t *testing.T) {
	mem := &memDrive{}
	useDrive(t, mem, nil)
	if err := uploadBackup(context.Background(), config{}, backup.Result{Path: "x.tar.gz"}); err == nil {
		t.Error("no error")
	}
	if len(mem.files) != 0 {
		t.Errorf("uploaded: %+v", mem.files)
	}
}

func TestDriveRetention(t *testing.T) {
	now := time.Now()
	mem := &memDrive{files: []google.DriveFile{
		{ID: "new", Name: "eumetria-2026-10-31-0330.tar.gz.enc", Created: now.Add(-24 * time.Hour)},
		{ID: "old", Name: "eumetria-2026-09-01-0330.tar.gz.enc", Created: now.Add(-60 * 24 * time.Hour)},
		{ID: "other", Name: "holiday.jpg", Created: now.Add(-60 * 24 * time.Hour)},
	}}
	if err := pruneDrive(context.Background(), mem, "f", now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, f := range mem.files {
		left = append(left, f.ID)
	}
	if strings.Join(left, " ") != "new other" {
		t.Errorf("left = %v", left)
	}
	// The newest stays, however old.
	mem.files = mem.files[:1]
	pruneDrive(context.Background(), mem, "f", now.AddDate(1, 0, 0))
	if len(mem.files) != 1 {
		t.Error("the last archive was deleted")
	}
}

func TestConnectAsksForDriveOnlyWhenOn(t *testing.T) {
	e := newTestEnv(t, true)
	fakeGoogle(t, nil)
	admin := e.signIn(t, "patrick", adminPassword, false)
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"google_client_id":"id","google_client_secret":"s"}}`, sameSite)
	if q := e.connect(t, admin); strings.Contains(q.Get("scope"), "drive") {
		t.Errorf("scope = %s", q.Get("scope"))
	}
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"backup_drive":"true","backup_passphrase":"a long enough passphrase"}}`, sameSite)
	if q := e.connect(t, admin); !strings.Contains(q.Get("scope"), "drive.file") {
		t.Errorf("scope = %s", q.Get("scope"))
	}
}

// The whole way, as the README tells it: back up with the real program, then
// decrypt with the real command, and find a database that opens.
func TestRestoreFromTheCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "eumetria")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('title', 'Restored')"); err != nil {
		t.Fatal(err)
	}
	const pass = "a long enough passphrase"
	out := filepath.Join(dir, "backups")
	res, err := backup.Write(ctx, backup.Options{Dir: out, Now: time.Now(), Passphrase: pass, Generation: "2026-11-01", Snapshot: d.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	run := func(input string) (string, error) {
		c := exec.Command(bin, "-decrypt-backup", res.Path)
		c.Stdin = strings.NewReader(input)
		o, err := c.CombinedOutput()
		return string(o), err
	}
	if o, err := run("the wrong passphrase\n"); err == nil || !strings.Contains(o, "wrong passphrase") {
		t.Fatalf("wrong passphrase: %v\n%s", err, o)
	}
	plain := strings.TrimSuffix(res.Path, ".enc")
	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Fatalf("a failed try left files: %v", entries)
	}
	o, err := run(pass + "\n")
	if err != nil || !strings.Contains(o, "2026-11-01") {
		t.Fatalf("decrypting: %v\n%s", err, o)
	}
	if o, err := run(pass + "\n"); err == nil || !strings.Contains(o, "exists") {
		t.Errorf("overwriting: %v\n%s", err, o)
	}
	f, err := os.Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	if h, err := tr.Next(); err != nil || h.Name != "data/app.db" {
		t.Fatalf("first file = %+v, %v", h, err)
	}
	restored := filepath.Join(dir, "restored.db")
	w, _ := os.Create(restored)
	io.Copy(w, tr)
	w.Close()
	r, err := db.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, _ := settings.New(r, "")
	if v, err := s.Values(ctx); err != nil || v.Title != "Restored" {
		t.Errorf("restored settings = %q, %v", v.Title, err)
	}
}
