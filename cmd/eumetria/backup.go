package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"eumetria/internal/backup"
	"eumetria/internal/google"
	"eumetria/internal/notes"
	"eumetria/internal/settings"
)

// Backups (PLAN M9). The runner runs them, so no sync writes while one reads:
// on the schedule in the settings, or from the settings by hand. Anything that
// leaves the server is encrypted, so Drive needs the passphrase.

const backupTimeout = time.Hour

// driveFor returns the Drive API, or why there is none.
var driveFor = func(cfg config) (driveAPI, error) {
	c, err := googleClient(cfg.googleToken, cfg.values)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errors.New("Google isn't set up")
	}
	return c.Drive(), nil
}

// driveAPI is what a backup needs of Drive; tests replace it.
type driveAPI interface {
	Folder(ctx context.Context, name string) (string, error)
	Files(ctx context.Context, folder string) ([]google.DriveFile, error)
	PutFile(ctx context.Context, folder, path, name string) (string, error)
	Delete(ctx context.Context, id string) error
	Test(ctx context.Context, folder string) error
}

// writeBackup makes the archive and, when set up, uploads it. A failed upload
// leaves the local archive: run.Drive says what went wrong.
func writeBackup(ctx context.Context, cfg config, snapshot func(context.Context, string) error, run *backup.Run) error {
	v := cfg.values
	now := cfg.now()
	res, err := backup.Write(ctx, backup.Options{
		Dir: cfg.backupDir, Now: now, Passphrase: v.BackupPassphrase, Generation: v.BackupPassphraseAt, Snapshot: snapshot,
		Skip: skipUnusedNoteImages(cfg.noteImageUsed),
		Files: map[string]string{
			"history.json": cfg.historyPath, "google-token.json": cfg.googleToken, "photos": cfg.photoDir,
			"avatars": filepath.Join(cfg.dataDir, "avatars"), "note-images": filepath.Join(cfg.dataDir, "note-images"),
		},
	})
	if err != nil {
		return err
	}
	run.File, run.Size, run.Encrypted = filepath.Base(res.Path), res.Size, res.Encrypted
	log.Printf("backup: wrote %s, %d bytes", run.File, run.Size)
	if n, err := backup.Prune(cfg.backupDir, now.AddDate(0, 0, -v.BackupKeepDays)); err != nil {
		log.Printf("backup: removing old archives: %v", err)
	} else if n > 0 {
		log.Printf("backup: removed %d old archives", n)
	}
	if v.BackupDrive {
		run.Drive = "ok"
		if err := uploadBackup(ctx, cfg, res); err != nil {
			log.Printf("backup: google drive: %v", err)
			run.Drive = err.Error()
		}
	}
	return nil
}

func uploadBackup(ctx context.Context, cfg config, res backup.Result) error {
	// Writing the archive already follows this rule; a passphrase removed
	// mid-run must not send a plain file out either.
	if !res.Encrypted {
		return errors.New("not uploaded: set a backup passphrase first")
	}
	d, err := driveFor(cfg)
	if err != nil {
		return err
	}
	folder, err := d.Folder(ctx, cfg.values.BackupFolder)
	if err != nil {
		return err
	}
	if _, err := d.PutFile(ctx, folder, res.Path, filepath.Base(res.Path)); err != nil {
		return err
	}
	return pruneDrive(ctx, d, folder, cfg.now().AddDate(0, 0, -cfg.values.BackupDriveKeepDays))
}

// pruneDrive deletes archives older than before, never the newest. A failure
// is only logged: the backup itself is safe.
func pruneDrive(ctx context.Context, d driveAPI, folder string, before time.Time) error {
	files, err := d.Files(ctx, folder)
	if err != nil {
		log.Printf("backup: listing google drive: %v", err)
		return nil
	}
	var ours []google.DriveFile
	for _, f := range files {
		if backup.IsArchive(f.Name) {
			ours = append(ours, f)
		}
	}
	sort.Slice(ours, func(i, j int) bool { return ours[i].Created.After(ours[j].Created) })
	for i, f := range ours {
		if i > 0 && f.Created.Before(before) {
			if err := d.Delete(ctx, f.ID); err != nil {
				log.Printf("backup: deleting %s on google drive: %v", f.Name, err)
			}
		}
	}
	return nil
}

// startBackup runs a backup in the background, and reports false if a job is
// running.
func (r *runner) startBackup(ctx context.Context) bool {
	return r.startJob(ctx, "backup", backupTimeout, func(ctx context.Context, cfg config, _ func(done, total int)) error {
		database := r.settings.DB
		started := time.Now()
		id, err := backup.Begin(ctx, database.DB, started)
		if err != nil {
			return err
		}
		var run backup.Run
		// Images no note embeds stay out (pentest F21): they are uploads
		// waiting for their note, or junk, and go within a day anyway.
		if used, err := (&notes.Store{DB: database, Now: time.Now}).UsedImages(ctx); err == nil {
			cfg.noteImageUsed = used
		}
		err = r.backup(ctx, cfg, database.Snapshot, &run)
		if err != nil {
			run.Error = err.Error()
			log.Printf("backup failed: %v", err)
		}
		// However the run ended, the log says so, also after a timeout.
		if ferr := backup.Finish(context.WithoutCancel(ctx), database.DB, id, time.Now(), run); ferr != nil {
			log.Printf("backup: %v", ferr)
		}
		return err
	})
}

// backupLoop starts the day's backup when it is due. It looks every minute,
// so a run a sync is in the way of just starts a minute later, and a new time
// in the settings needs no restart.
func (r *runner) backupLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := r.current(ctx)
		if !cfg.values.Backups {
			continue
		}
		runs, err := backup.Recent(ctx, r.settings.DB.DB, 1)
		if err != nil {
			log.Printf("backup: %v", err)
			continue
		}
		var last time.Time
		if len(runs) > 0 {
			last = runs[0].StartedAt
		}
		if backup.Due(cfg.now(), cfg.values.BackupTime, last) {
			r.startBackup(ctx)
		}
	}
}

/* ---------- the API ---------- */

func (a *app) backups(w http.ResponseWriter, req *http.Request, _ *principal) {
	runs, err := backup.Recent(req.Context(), a.runner.settings.DB.DB, 10)
	if err != nil {
		a.failed(w, "backups", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "dir": a.runner.cfg.backupDir, "status": a.runner.status()})
}

func (a *app) backupNow(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	code := http.StatusAccepted
	if a.runner.startBackup(context.WithoutCancel(req.Context())) {
		a.audit_(req.Context(), p.user, "", "backup_started", "", clientIP(req))
	} else {
		code = http.StatusConflict
	}
	writeJSON(w, code, a.runner.status())
}

// backupTestDrive uploads and deletes a small file.
func (a *app) backupTestDrive(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	cfg := a.runner.current(ctx)
	d, err := driveFor(cfg)
	if err == nil {
		var folder string
		if folder, err = d.Folder(ctx, cfg.values.BackupFolder); err == nil {
			err = d.Test(ctx, folder)
		}
	}
	if err != nil {
		log.Printf("drive test: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// driveNeedsPassphrase refuses a change that would turn Drive on without a
// passphrase, or take the passphrase from a Drive that is on.
func driveNeedsPassphrase(v settings.Values, values map[string]string, reset []string) bool {
	drive, pass := v.BackupDrive, v.BackupPassphrase != ""
	if s, ok := values["backup_drive"]; ok {
		drive = s == "true"
	}
	if _, ok := values["backup_passphrase"]; ok {
		pass = true
	}
	for _, k := range reset {
		if k == "backup_passphrase" {
			pass = false
		}
		if k == "backup_drive" {
			drive = false
		}
	}
	return drive && !pass
}

/* ---------- restoring ---------- */

// decryptBackup writes the archive next to the encrypted file. It appears only
// once all of it checked out, so a wrong passphrase or a damaged file leaves
// nothing behind. It never needs the server or the settings.
func decryptBackup(path string) error {
	if !strings.HasSuffix(path, ".enc") {
		return errors.New("that's not an encrypted backup: its name ends in .enc")
	}
	out := strings.TrimSuffix(path, ".enc")
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	gen, err := backup.Generation(in)
	if err != nil {
		return err
	}
	if _, err := in.Seek(0, 0); err != nil {
		return err
	}
	if gen != "" {
		fmt.Fprintf(os.Stderr, "This backup uses the passphrase set on %s.\n", gen)
	}
	pass, err := readSecret(bufio.NewReader(os.Stdin), "Passphrase: ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := backup.Decrypt(in, tmp, pass); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return err
	}
	fmt.Printf("Wrote %s. It isn't encrypted: delete it once you've restored.\n", out)
	return nil
}

// skipUnusedNoteImages leaves the note images out of a backup that no note
// embeds, by used (nil: none are left out).
func skipUnusedNoteImages(used func(string) bool) func(string) bool {
	if used == nil {
		return nil
	}
	return func(name string) bool {
		rest, ok := strings.CutPrefix(name, "data/note-images/")
		return ok && !used(rest)
	}
}
