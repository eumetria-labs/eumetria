// Package backup writes the archives of a site's data: the synced history,
// photos, pictures, images in notes, the Google token and a consistent copy of
// the database. With a passphrase an archive is encrypted (crypt.go); what
// leaves the server always is. SETTINGS_KEY is never in one, so the secrets in
// the database need it after a restore.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	plainExt     = ".tar.gz"
	encryptedExt = ".tar.gz.enc"
)

var archiveName = regexp.MustCompile(`^eumetria-\d{4}-\d{2}-\d{2}-\d{4}\.tar\.gz(\.enc)?$`)

// IsArchive reports whether name is a file this package wrote.
func IsArchive(name string) bool { return archiveName.MatchString(name) }

// Options say what goes where.
type Options struct {
	Dir        string            // where archives go
	Now        time.Time         // names the archive, in the site's time zone
	Passphrase string            // "": a plain archive, for this server only
	Generation string            // the day the passphrase was set
	Files      map[string]string // name in the archive → file or folder; missing ones are left out
	// Snapshot writes a consistent copy of the database to a new file.
	Snapshot func(ctx context.Context, file string) error
}

// Result is an archive on disk.
type Result struct {
	Path      string
	Size      int64
	Encrypted bool
}

// Write makes an archive, which appears under its name only once complete.
func Write(ctx context.Context, o Options) (res Result, err error) {
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return res, err
	}
	base := "eumetria-" + o.Now.Format("2006-01-02-1504")
	name := base + plainExt
	if o.Passphrase != "" {
		name = base + encryptedExt
	}
	tmp, err := os.CreateTemp(o.Dir, ".part-*")
	if err != nil {
		return res, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	db := tmp.Name() + ".db"
	defer os.Remove(db)
	if err := o.Snapshot(ctx, db); err != nil {
		return res, fmt.Errorf("copying the database: %w", err)
	}
	var out io.Writer = tmp
	var enc io.WriteCloser
	if o.Passphrase != "" {
		if enc, err = NewWriter(tmp, o.Passphrase, o.Generation); err != nil {
			return res, err
		}
		out = enc
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := addFile(ctx, tw, db, "data/app.db"); err != nil {
		return res, err
	}
	names := make([]string, 0, len(o.Files))
	for n := range o.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := addTree(ctx, tw, o.Files[n], "data/"+n); err != nil {
			return res, fmt.Errorf("%s: %w", n, err)
		}
	}
	if err := tw.Close(); err != nil {
		return res, err
	}
	if err := gz.Close(); err != nil {
		return res, err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return res, err
		}
	}
	if err := tmp.Sync(); err != nil {
		return res, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return res, err
	}
	if err := tmp.Close(); err != nil {
		return res, err
	}
	path := filepath.Join(o.Dir, name)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return res, err
	}
	return Result{Path: path, Size: info.Size(), Encrypted: enc != nil}, nil
}

func addTree(ctx context.Context, tw *tar.Writer, root, name string) error {
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return addFile(ctx, tw, root, name)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil // folders come with their files; temporary files stay out
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return addFile(ctx, tw, p, filepath.ToSlash(filepath.Join(name, rel)))
	})
}

func addFile(ctx context.Context, tw *tar.Writer, path, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, info.Size())
	return err
}

// Prune deletes archives older than before, never the newest, and what a
// crashed run left behind. It only touches files this package wrote. It
// returns how many archives it deleted.
func Prune(dir string, before time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type file struct {
		name string
		at   time.Time
	}
	var files []file
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.HasPrefix(e.Name(), ".part-") && info.ModTime().Before(before) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
		if e.Type().IsRegular() && IsArchive(e.Name()) {
			files = append(files, file{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
	gone := 0
	for i, f := range files {
		if i == 0 || !f.at.Before(before) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err != nil {
			return gone, err
		}
		gone++
	}
	return gone, nil
}

/* ---------- the log ---------- */

// Run is one backup, as the settings show it.
type Run struct {
	ID         int64     `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	File       string    `json:"file,omitempty"`
	Size       int64     `json:"size,omitempty"`
	Encrypted  bool      `json:"encrypted,omitempty"`
	Drive      string    `json:"drive,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Begin notes that a backup started.
func Begin(ctx context.Context, d *sql.DB, at time.Time) (int64, error) {
	res, err := d.ExecContext(ctx, "INSERT INTO backup_runs (started_at) VALUES (?)", at.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Finish notes how a backup ended. It keeps the last 100.
func Finish(ctx context.Context, d *sql.DB, id int64, at time.Time, r Run) error {
	_, err := d.ExecContext(ctx, "UPDATE backup_runs SET finished_at = ?, file = ?, size = ?, encrypted = ?, drive = ?, error = ? WHERE id = ?",
		at.Unix(), r.File, r.Size, r.Encrypted, r.Drive, r.Error, id)
	if err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, "DELETE FROM backup_runs WHERE id NOT IN (SELECT id FROM backup_runs ORDER BY id DESC LIMIT 100)")
	return err
}

// Recent lists the last runs, the newest first.
func Recent(ctx context.Context, d *sql.DB, limit int) ([]Run, error) {
	rows, err := d.QueryContext(ctx, "SELECT id, started_at, finished_at, file, size, encrypted, drive, error FROM backup_runs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var started, finished int64
		if err := rows.Scan(&r.ID, &started, &finished, &r.File, &r.Size, &r.Encrypted, &r.Drive, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0).UTC()
		if finished > 0 {
			r.FinishedAt = time.Unix(finished, 0).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Due reports whether the day's backup should start: the time of day has come
// and no backup started since. So a server that was down catches up when it
// comes back, once. at is "HH:MM" in now's zone.
func Due(now time.Time, at string, last time.Time) bool {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	return !now.Before(today) && last.Before(today)
}
