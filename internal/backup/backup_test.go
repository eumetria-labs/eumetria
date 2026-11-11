package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const pass = "a long enough passphrase"

func seal(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, pass, "2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	// In odd pieces: chunks don't depend on how it is written.
	for len(data) > 0 {
		n := min(70001, len(data))
		if _, err := w.Write(data[:n]); err != nil {
			t.Fatal(err)
		}
		data = data[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestEncryption(t *testing.T) {
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 2 * chunkSize, 5*chunkSize + 123} {
		plain := make([]byte, size)
		rand.Read(plain)
		var out bytes.Buffer
		if err := Decrypt(bytes.NewReader(seal(t, plain)), &out, pass); err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}
		if !bytes.Equal(out.Bytes(), plain) {
			t.Errorf("%d bytes came back different", size)
		}
	}
}

func TestDamageIsNoticed(t *testing.T) {
	plain := bytes.Repeat([]byte("the secret text "), 300000) // over 4 chunks
	sealed := seal(t, plain)
	if bytes.Contains(sealed, []byte("secret")) {
		t.Fatal("the text is readable")
	}
	open := func(b []byte, p string) error { return Decrypt(bytes.NewReader(b), io.Discard, p) }

	if err := open(sealed, "the wrong passphrase"); !errors.Is(err, ErrPassphrase) {
		t.Errorf("wrong passphrase: %v", err)
	}
	if err := open([]byte("just some file, long enough to hold a whole header of an archive"), pass); !errors.Is(err, ErrNotBackup) {
		t.Errorf("another file: %v", err)
	}

	// Anywhere it is changed or cut, it fails.
	hdr := len(magic) + saltSize + 13 + len("2026-11-01") + prefixSize
	for _, at := range []int{20, hdr - 1, hdr + 10, len(sealed) / 2, len(sealed) - 1} {
		bad := bytes.Clone(sealed)
		bad[at] ^= 1
		if err := open(bad, pass); err == nil {
			t.Errorf("a changed byte at %d went unnoticed", at)
		}
	}
	for _, at := range []int{hdr, hdr + 2, hdr + 4 + chunkSize + 16, hdr + 2*(4+chunkSize+16), len(sealed) - 1} {
		if err := open(sealed[:at], pass); err == nil {
			t.Errorf("cut at %d went unnoticed", at)
		}
	}

	// Chunks can't change places.
	one := 4 + chunkSize + 16
	swapped := bytes.Clone(sealed)
	copy(swapped[hdr:], sealed[hdr+one:hdr+2*one])
	copy(swapped[hdr+one:], sealed[hdr:hdr+one])
	if err := open(swapped, pass); !errors.Is(err, ErrPassphrase) {
		t.Errorf("swapped chunks: %v", err)
	}

	// A header can't demand all the memory there is.
	huge := bytes.Clone(sealed)
	huge[len(magic)+saltSize+4] = 0xff
	if err := open(huge, pass); !errors.Is(err, ErrNotBackup) {
		t.Errorf("a huge memory demand: %v", err)
	}

	if g, err := Generation(bytes.NewReader(sealed)); err != nil || g != "2026-11-01" {
		t.Errorf("generation = %q, %v", g, err)
	}
}

func options(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	write := func(p, text string, mode os.FileMode) {
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "history.json"), `{"days":{}}`, 0o600)
	write(filepath.Join(dir, "google-token.json"), `{"refresh_token":"tok"}`, 0o600)
	write(filepath.Join(dir, "photos", "a.jpg"), "photo", 0o644)
	write(filepath.Join(dir, "photos", ".upload-1"), "half", 0o600)
	write(filepath.Join(dir, "avatars", "1.jpg"), "face", 0o600)
	return Options{
		Dir: filepath.Join(dir, "backups"),
		Now: time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC),
		Files: map[string]string{
			"history.json": filepath.Join(dir, "history.json"), "google-token.json": filepath.Join(dir, "google-token.json"),
			"photos": filepath.Join(dir, "photos"), "avatars": filepath.Join(dir, "avatars"), "note-images": filepath.Join(dir, "note-images"),
		},
		Snapshot: func(_ context.Context, f string) error { return os.WriteFile(f, []byte("database"), 0o600) },
	}
}

func unpack(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func names(m map[string]string) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestArchive(t *testing.T) {
	o := options(t)
	res, err := Write(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Path) != "eumetria-2026-11-01-0330.tar.gz" || res.Encrypted || res.Size == 0 {
		t.Fatalf("result = %+v", res)
	}
	info, _ := os.Stat(res.Path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	f, _ := os.Open(res.Path)
	defer f.Close()
	got := unpack(t, f)
	want := "data/app.db data/avatars/1.jpg data/google-token.json data/history.json data/photos/a.jpg"
	if names(got) != want || got["data/app.db"] != "database" {
		t.Errorf("archive = %q", names(got))
	}

	o.Passphrase, o.Generation = pass, "2026-11-01"
	res, err = Write(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Path) != "eumetria-2026-11-01-0330.tar.gz.enc" || !res.Encrypted {
		t.Fatalf("result = %+v", res)
	}
	raw, _ := os.ReadFile(res.Path)
	if bytes.Contains(raw, []byte("refresh_token")) {
		t.Error("the encrypted archive holds readable text")
	}
	var plain bytes.Buffer
	if err := Decrypt(bytes.NewReader(raw), &plain, pass); err != nil {
		t.Fatal(err)
	}
	if names(unpack(t, &plain)) != want {
		t.Error("the encrypted archive holds something else")
	}
}

func TestAFailedRunLeavesNothing(t *testing.T) {
	o := options(t)
	o.Snapshot = func(context.Context, string) error { return errors.New("disk full") }
	if _, err := Write(context.Background(), o); err == nil {
		t.Fatal("no error")
	}
	if left, _ := os.ReadDir(o.Dir); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC)
	touch := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, nil, 0o600)
		os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	day := 24 * time.Hour
	touch("eumetria-2026-10-31-0330.tar.gz.enc", day)
	touch("eumetria-2026-10-01-0330.tar.gz.enc", 31*day)
	touch("eumetria-2026-09-01-0330.tar.gz", 61*day)
	touch("data-2026-09-01.tar.gz", 61*day) // the old cron's, or anything else
	touch("notes.txt", 61*day)
	touch(".part-old", 61*day)
	touch(".part-new", time.Minute)

	if n, err := Prune(dir, now.Add(-14*day)); err != nil || n != 2 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	for name, want := range map[string]bool{
		"eumetria-2026-10-31-0330.tar.gz.enc": true, "eumetria-2026-10-01-0330.tar.gz.enc": false, "eumetria-2026-09-01-0330.tar.gz": false,
		"data-2026-09-01.tar.gz": true, "notes.txt": true, ".part-old": false, ".part-new": true,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s: there = %v, want %v", name, err == nil, want)
		}
	}

	// The newest stays, however old: with nothing newer it's all there is.
	if n, _ := Prune(dir, now.Add(day)); n != 0 {
		t.Errorf("the last one was pruned (%d)", n)
	}
}

func TestDue(t *testing.T) {
	zone := time.FixedZone("x", 3600)
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, zone) }
	for _, c := range []struct {
		now, last time.Time
		time      string
		want      bool
	}{
		{at(2, 3, 29), at(1, 3, 30), "03:30", false}, // not yet
		{at(2, 3, 30), at(1, 3, 30), "03:30", true},  // on time
		{at(2, 3, 31), at(2, 3, 30), "03:30", false}, // done
		{at(2, 9, 0), at(1, 3, 30), "03:30", true},   // the server was down
		{at(2, 9, 0), at(2, 8, 0), "03:30", false},   // a backup by hand counts
		{at(2, 2, 0), at(2, 1, 0), "03:30", false},   // one before the time
		{at(2, 3, 30), time.Time{}, "03:30", true},   // never ran
		{at(2, 3, 30), time.Time{}, "nope", false},
	} {
		if got := Due(c.now, c.time, c.last); got != c.want {
			t.Errorf("Due(%v, %s, %v) = %v", c.now, c.time, c.last, got)
		}
	}
}

// Skip leaves files out by their name in the archive.
func TestArchiveSkip(t *testing.T) {
	o := options(t)
	images := o.Files["note-images"]
	os.MkdirAll(images, 0o700)
	os.WriteFile(filepath.Join(images, "used.jpg"), []byte("u"), 0o600)
	os.WriteFile(filepath.Join(images, "orphan.jpg"), []byte("o"), 0o600)
	o.Skip = func(name string) bool { return name == "data/note-images/orphan.jpg" }
	res, err := Write(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(res.Path)
	defer f.Close()
	got := names(unpack(t, f))
	if !strings.Contains(got, "data/note-images/used.jpg") || strings.Contains(got, "orphan") {
		t.Errorf("archive = %s", got)
	}
}
