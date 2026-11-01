package notes

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/picture"
)

func TestNotes(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	accounts := auth.NewStore(d)
	anna, err := accounts.CreateUser(ctx, "anna", "a viewer with a long password", auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := accounts.CreateUser(ctx, "bob", "another long enough password", auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s := &Store{DB: d, Now: func() time.Time { return now }}

	n, added, err := s.Put(ctx, "w1", "# Warm-Up", anna.ID)
	if err != nil || !added || n.CreatedBy != "anna" || n.UpdatedBy != "anna" || !n.CreatedAt.Equal(now) {
		t.Fatalf("first put = %+v, %v, %v", n, added, err)
	}

	// Someone else's change keeps who wrote it and when.
	later := now.Add(time.Hour)
	now = later
	n, added, err = s.Put(ctx, "w1", "# WOD", bob.ID)
	if err != nil || added || n.Markdown != "# WOD" || n.CreatedBy != "anna" || n.UpdatedBy != "bob" ||
		!n.CreatedAt.Equal(later.Add(-time.Hour)) || !n.UpdatedAt.Equal(later) {
		t.Fatalf("second put = %+v, %v, %v", n, added, err)
	}

	if _, _, err := s.Put(ctx, "w2", "second", bob.ID); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].WorkoutID != "w1" || list[1].WorkoutID != "w2" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if err := s.Delete(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "w1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it again = %v", err)
	}
}

func TestImages(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	anna, err := auth.NewStore(d).CreateUser(ctx, "anna", "a viewer with a long password", auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: d, Now: time.Now}

	// Transparent pixels land on white, and the result is a JPEG.
	var src bytes.Buffer
	if err := png.Encode(&src, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	save := func() string {
		name, err := SaveImage(dir, src.Bytes())
		if err != nil || !ValidImageName(name) {
			t.Fatalf("save = %q, %v", name, err)
		}
		return name
	}
	used, stale, recent := save(), save(), save()
	f, err := os.Open(filepath.Join(dir, used))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(1, 1).RGBA(); r < 0xf000 {
		t.Errorf("a transparent pixel = %v", img.At(1, 1))
	}

	if _, err := SaveImage(dir, []byte("<svg onload=alert(1)>")); !errors.Is(err, picture.ErrBad) {
		t.Errorf("not an image: %v", err)
	}

	// Pruning leaves what a note uses and what may still be used.
	if _, _, err := s.Put(ctx, "w1", "![](note-images/"+used+")", anna.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, name := range []string{used, stale} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(foreign, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(foreign, old, old)
	n, err := s.PruneImages(ctx, dir, time.Now().Add(-7*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	for name, want := range map[string]bool{used: true, stale: false, recent: true, "keep.txt": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s: exists = %v, want %v", name, err == nil, want)
		}
	}
	if n, err := s.PruneImages(ctx, filepath.Join(dir, "none"), time.Now()); n != 0 || err != nil {
		t.Errorf("no folder yet: %d, %v", n, err)
	}
}
