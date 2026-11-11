package notes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"eumetria/internal/picture"
)

// Images in notes: files in one folder of the data, with random names, which
// a note embeds as ![](note-images/<name>). Only such images are shown, so a
// note can't load anything from elsewhere. An upload is decoded and written
// anew as a JPEG, which leaves its metadata, and anything hidden in the
// file, behind. They are saved before the note that uses them, so images no
// note ended up using are removed later (PruneImages).

var imageName = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// ValidImageName reports whether name can be an image's file name.
func ValidImageName(name string) bool { return imageName.MatchString(name) }

// SaveImage writes raw anew into dir and returns its file name.
func SaveImage(dir string, raw []byte) (string, error) {
	clean, err := picture.Clean(raw, picture.NotePixels, nil)
	if err != nil {
		return "", err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	name := hex.EncodeToString(id) + ".jpg"
	return name, picture.Save(dir, name, clean)
}

// PruneImages deletes the images in dir that no note uses and that are older
// than before, which leaves time for the note being written. A note whose
// workout is gone keeps its images. It returns how many it deleted.
func (s *Store) PruneImages(ctx context.Context, dir string, before time.Time) (int, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	used, err := s.UsedImages(ctx)
	if err != nil {
		return 0, err
	}
	gone := 0
	for _, f := range files {
		if !ValidImageName(f.Name()) || used(f.Name()) {
			continue
		}
		info, err := f.Info()
		if err != nil || !info.ModTime().Before(before) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, f.Name())); err != nil {
			return gone, err
		}
		gone++
	}
	// The record of who uploaded them goes with them.
	_, err = s.DB.ExecContext(ctx, "DELETE FROM note_uploads WHERE created_at < ?", before.Unix())
	return gone, err
}

// UsedImages returns whether a note embeds the image named name.
func (s *Store) UsedImages(ctx context.Context) (func(name string) bool, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, n := range list {
		b.WriteString(n.Markdown)
	}
	all := b.String()
	return func(name string) bool { return strings.Contains(all, name) }, nil
}

// RecordUpload notes that user uploaded the image name.
func (s *Store) RecordUpload(ctx context.Context, name string, user int64) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO note_uploads (name, user_id, created_at) VALUES (?, ?, ?)", name, user, s.Now().Unix())
	return err
}

// PendingUploads counts user's uploads no note embeds (yet).
func (s *Store) PendingUploads(ctx context.Context, user int64) (int, error) {
	used, err := s.UsedImages(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT name FROM note_uploads WHERE user_id = ?", user)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, err
		}
		if !used(name) {
			n++
		}
	}
	return n, rows.Err()
}

// DirSize is how many bytes the files in dir take, 0 for a missing dir.
func DirSize(dir string) (int64, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	for _, f := range files {
		if info, err := f.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
	}
	return n, nil
}
