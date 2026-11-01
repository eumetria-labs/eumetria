package notes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // what a pasted screenshot may be
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Images in notes: files in one folder of the data, with random names, which
// a note embeds as ![](note-images/<name>). Only such images are shown, so a
// note can't load anything from elsewhere. An upload is decoded and written
// anew as a JPEG, which leaves its metadata, and anything hidden in the
// file, behind. They are saved before the note that uses them, so images no
// note ended up using are removed later (PruneImages).

// MaxImageSide is the longest side the server takes; the page scales images
// down to less. It also keeps a small file from decoding into a huge image.
const MaxImageSide = 4096

var ErrBadImage = errors.New("not a usable image")

var imageName = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// ValidImageName reports whether name can be an image's file name.
func ValidImageName(name string) bool { return imageName.MatchString(name) }

// cleanImage decodes a JPEG or PNG and encodes its pixels as a new JPEG, on
// white where it is transparent.
func cleanImage(raw []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, ErrBadImage
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return nil, ErrBadImage
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrBadImage
	}
	flat := image.NewRGBA(src.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, src.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// SaveImage cleans raw, stores it in dir and returns its file name.
func SaveImage(dir string, raw []byte) (string, error) {
	clean, err := cleanImage(raw)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	name := hex.EncodeToString(id) + ".jpg"
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(clean); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	return name, os.Rename(tmp.Name(), filepath.Join(dir, name))
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
