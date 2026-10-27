package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/site"
)

const testToken = "s3cret-token"

// testServer serves a tiny site and public dir; syncs block until release
// is closed.
func testServer(t *testing.T, token string) (*httptest.Server, *runner, chan struct{}, chan config) {
	t.Helper()
	dir := t.TempDir()
	site, public := filepath.Join(dir, "site"), filepath.Join(dir, "public")
	for path, body := range map[string]string{
		filepath.Join(site, "index.html"):          "<h1>hi</h1>",
		filepath.Join(site, "fonts", "f.woff2"):    "font",
		filepath.Join(public, "stats.json"):        `{"days":[]}`,
		filepath.Join(public, "photos", "abc.jpg"): "jpg",
		filepath.Join(public, "routes", "r1.json"): "[]",
		filepath.Join(public, "days", "y1.json"):   `{"days":[]}`,
	} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	release, calls := make(chan struct{}), make(chan config, 10)
	r := newRunner(config{outPath: filepath.Join(public, "stats.json")}, time.Hour)
	r.sync = func(ctx context.Context, cfg config) error {
		calls <- cfg
		<-release
		return nil
	}
	srv := httptest.NewServer(newHandler(r, os.DirFS(site), public, token))
	t.Cleanup(srv.Close)
	return srv, r, release, calls
}

func do(t *testing.T, method, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestServeFiles(t *testing.T) {
	srv, _, _, _ := testServer(t, testToken)
	for _, c := range []struct {
		path  string
		code  int
		cache string
	}{
		{"/", 200, "no-cache"},
		{"/stats.json", 200, "no-cache"},
		{"/photos/abc.jpg", 200, "immutable"},
		{"/routes/r1.json", 200, "immutable"},
		{"/days/y1.json", 200, "immutable"},
		{"/fonts/f.woff2", 200, "max-age=604800"},
		{"/photos/", 404, ""},    // no directory listing
		{"/fonts/", 404, ""},     // same for the site
		{"/missing.js", 404, ""}, // and nothing outside the two folders
		{"/../public/stats.json", 404, ""},
	} {
		res := do(t, "GET", srv.URL+c.path, "")
		if res.StatusCode != c.code {
			t.Errorf("GET %s = %d, want %d", c.path, res.StatusCode, c.code)
		}
		if c.cache != "" && !strings.Contains(res.Header.Get("Cache-Control"), c.cache) {
			t.Errorf("GET %s Cache-Control = %q, want %q", c.path, res.Header.Get("Cache-Control"), c.cache)
		}
		if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("GET %s has no CSP header", c.path)
		}
	}
}

func TestServeAPIAuth(t *testing.T) {
	srv, _, release, calls := testServer(t, testToken)
	defer close(release)

	if res := do(t, "GET", srv.URL+"/api/status", ""); res.StatusCode != 401 {
		t.Errorf("status without token = %d, want 401", res.StatusCode)
	}
	if res := do(t, "POST", srv.URL+"/api/sync", "wrong"); res.StatusCode != 401 {
		t.Errorf("sync with wrong token = %d, want 401", res.StatusCode)
	}
	if len(calls) != 0 {
		t.Fatal("a rejected request started a sync")
	}
	if res := do(t, "POST", srv.URL+"/api/sync?all=1", testToken); res.StatusCode != 202 {
		t.Fatalf("sync = %d, want 202", res.StatusCode)
	}
	if cfg := <-calls; !cfg.refreshAll {
		t.Error("?all=1 did not request a full refresh")
	}
	if res := do(t, "POST", srv.URL+"/api/sync", testToken); res.StatusCode != 409 {
		t.Errorf("second sync while running = %d, want 409", res.StatusCode)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var st syncStatus
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil || !st.Running || !st.All {
		t.Errorf("status = %+v, %v; want a running full refresh", st, err)
	}
}

func TestServeAPILockout(t *testing.T) {
	srv, _, release, _ := testServer(t, testToken)
	defer close(release)
	for range maxFailures {
		do(t, "GET", srv.URL+"/api/status", "guess")
	}
	if res := do(t, "GET", srv.URL+"/api/status", testToken); res.StatusCode != 429 {
		t.Errorf("after %d wrong tokens = %d, want 429 even for the right one", maxFailures, res.StatusCode)
	}
}

func TestServeAPIDisabledWithoutToken(t *testing.T) {
	srv, _, release, calls := testServer(t, "")
	defer close(release)
	if res := do(t, "POST", srv.URL+"/api/sync", ""); res.StatusCode < 400 {
		t.Errorf("sync without ADMIN_TOKEN = %d, want an error", res.StatusCode)
	}
	if res := do(t, "GET", srv.URL+"/api/status", ""); res.StatusCode != 404 {
		t.Errorf("status without ADMIN_TOKEN = %d, want 404", res.StatusCode)
	}
	if len(calls) != 0 {
		t.Fatal("sync started without ADMIN_TOKEN")
	}
}

func TestHealthy(t *testing.T) {
	r := newRunner(config{}, 30*time.Minute)
	if !r.healthy(r.started.Add(time.Hour)) {
		t.Error("unhealthy while still catching up after start")
	}
	if r.healthy(r.started.Add(2 * time.Hour)) {
		t.Error("healthy although no sync succeeded for 4 intervals")
	}
	r.lastSuccess = r.started.Add(100 * time.Minute)
	if !r.healthy(r.started.Add(2 * time.Hour)) {
		t.Error("unhealthy right after a successful sync")
	}
}

func TestServeCompiledInPages(t *testing.T) {
	public := t.TempDir()
	_ = os.WriteFile(filepath.Join(public, "stats.json"), []byte(`{"days":[]}`), 0o644)
	r := newRunner(config{outPath: filepath.Join(public, "stats.json")}, time.Hour)
	srv := httptest.NewServer(newHandler(r, site.Files, public, ""))
	t.Cleanup(srv.Close)

	for _, p := range []string{"/", "/js/main.js", "/stats.json"} {
		if res := do(t, "GET", srv.URL+p, ""); res.StatusCode != 200 {
			t.Errorf("GET %s = %d", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/embed.go", "/embed_test.go"} {
		if res := do(t, "GET", srv.URL+p, ""); res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", p, res.StatusCode)
		}
	}
	// Compiled-in files have no modification time: an ETag lets browsers
	// revalidate them.
	res := do(t, "GET", srv.URL+"/style.css", "")
	tag := res.Header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag for a compiled-in file")
	}
	req, _ := http.NewRequest("GET", srv.URL+"/style.css", nil)
	req.Header.Set("If-None-Match", tag)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", res2.StatusCode)
	}
}
