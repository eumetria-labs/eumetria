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
	img, err := picture.Decode(raw)
	if err != nil {
		return "", err
	}
	clean, err := picture.JPEG(img)
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
	list, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	var used strings.Builder
	for _, n := range list {
		used.WriteString(n.Markdown)
	}
	all := used.String()
	gone := 0
	for _, f := range files {
		if !ValidImageName(f.Name()) || strings.Contains(all, f.Name()) {
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
	return gone, nil
}
