package site

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// generated may be written by eumetria next to the page files (locally,
// for the smoke test) and are private data.
var generated = []string{"stats.json", "photos", "routes", "days"}

func isGenerated(p string) bool {
	for _, g := range generated {
		if p == g || strings.HasPrefix(p, g+"/") {
			return true
		}
	}
	return strings.HasSuffix(p, ".tmp")
}

func TestNoPrivateDataEmbedded(t *testing.T) {
	err := fs.WalkDir(Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if isGenerated(p) || path.Ext(p) == ".go" {
			t.Errorf("%s is embedded", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(Files, "index.html"); err != nil {
		t.Error("index.html is not embedded")
	}
}

// Every page file on disk is embedded, so a new file or folder can't be
// forgotten in the //go:embed list.
func TestEveryPageFileEmbedded(t *testing.T) {
	err := fs.WalkDir(os.DirFS("."), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if isGenerated(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || path.Ext(p) == ".go" {
			return nil
		}
		if _, err := fs.Stat(Files, p); err != nil {
			t.Errorf("%s is not embedded; add it to the //go:embed list in embed.go", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
