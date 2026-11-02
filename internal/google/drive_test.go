package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDrive keeps files in memory, as far as the client needs.
type fakeDrive struct {
	mu     sync.Mutex
	files  map[string]DriveFile
	bodies map[string]string
	parent map[string]string
	n      int
	folder int
}

func (f *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/files":
		q := r.URL.Query().Get("q")
		var out []DriveFile
		for id, file := range f.files {
			if strings.Contains(q, "in parents") && strings.Contains(q, "'"+f.parent[id]+"'") && f.parent[id] != "" {
				out = append(out, file)
			} else if strings.Contains(q, "mimeType") && strings.Contains(q, "name = '"+strings.ReplaceAll(file.Name, "'", `\'`)+"'") && f.parent[id] == "" {
				out = append(out, file)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"files": out})
	case r.Method == "POST" && r.URL.Path == "/files":
		var meta struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&meta)
		f.folder++
		f.n++
		id := "folder" + string(rune('0'+f.n))
		f.files[id] = DriveFile{ID: id, Name: meta.Name}
		json.NewEncoder(w).Encode(map[string]string{"id": id})
	case r.Method == "POST" && r.URL.Path == "/upload/files":
		var meta struct {
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		json.NewDecoder(r.Body).Decode(&meta)
		f.n++
		id := "file" + string(rune('0'+f.n))
		f.files[id], f.parent[id] = DriveFile{ID: id, Name: meta.Name, Created: time.Now()}, meta.Parents[0]
		w.Header().Set("Location", "http://"+r.Host+"/session/"+id)
	case r.Method == "PUT":
		id := strings.TrimPrefix(r.URL.Path, "/session/")
		b, _ := io.ReadAll(r.Body)
		f.bodies[id] = string(b)
		json.NewEncoder(w).Encode(map[string]string{"id": id})
	case r.Method == "DELETE":
		id := strings.TrimPrefix(r.URL.Path, "/files/")
		delete(f.files, id)
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

func TestDrive(t *testing.T) {
	fake := &fakeDrive{files: map[string]DriveFile{}, bodies: map[string]string{}, parent: map[string]string{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, &Token{RefreshToken: "r", AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{}, path)
	if err != nil {
		t.Fatal(err)
	}
	d := c.Drive()
	d.API, d.Upload = srv.URL, srv.URL+"/upload"
	ctx := context.Background()

	folder, err := d.Folder(ctx, "Patrick's backups")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := d.Folder(ctx, "Patrick's backups"); err != nil || again != folder || fake.folder != 1 {
		t.Fatalf("second time: %s, %v, %d made", again, err, fake.folder)
	}
	id, err := d.Put(ctx, folder, "a.enc", strings.NewReader("archive"), 7)
	if err != nil || fake.bodies[id] != "archive" {
		t.Fatalf("put = %s, %v, %q", id, err, fake.bodies[id])
	}
	files, err := d.Files(ctx, folder)
	if err != nil || len(files) != 1 || files[0].Name != "a.enc" {
		t.Fatalf("files = %+v, %v", files, err)
	}
	if err := d.Test(ctx, folder); err != nil {
		t.Fatal(err)
	}
	if files, _ := d.Files(ctx, folder); len(files) != 1 {
		t.Errorf("the test left a file: %+v", files)
	}
	if err := d.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if files, _ := d.Files(ctx, folder); len(files) != 0 {
		t.Errorf("after deleting: %+v", files)
	}

	// Without the scope the error says what to do.
	d.c.tok.AccessToken = "other"
	if _, err := d.Folder(ctx, "x"); err == nil || !strings.Contains(err.Error(), "Connect again") {
		t.Errorf("error = %v", err)
	}
}

func TestExtraScopes(t *testing.T) {
	if u := (Config{Extra: []string{DriveScope}}).AuthURL("s"); !strings.Contains(u, "drive.file") {
		t.Errorf("no drive scope: %s", u)
	}
	if u := (Config{}).AuthURL("s"); strings.Contains(u, "drive") {
		t.Errorf("a drive scope nobody asked for: %s", u)
	}
}
