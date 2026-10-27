package yazio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestYazioImage(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://images.yazio-cdn.com/process/plain/app/meal/x.webp": true,
		"https://yazio-cdn.com/x.jpg":                                true,
		"http://images.yazio-cdn.com/x.jpg":                          false, // not HTTPS
		"https://images.yazio-cdn.com.evil.example/x.jpg":            false,
		"https://evilyazio-cdn.com/x.jpg":                            false,
		"https://169.254.169.254/latest/meta-data":                   false,
		"file:///etc/passwd":                                         false,
	} {
		u, _ := url.Parse(raw)
		if got := YazioImage(u); got != want {
			t.Errorf("YazioImage(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestDownloadRefusesOtherHosts(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	}))
	defer srv.Close()
	ctx := context.Background()

	c := New("")
	if _, _, err := c.Download(ctx, srv.URL+"/a.jpg"); err == nil || hits != 0 {
		t.Fatalf("default policy must refuse non-CDN URLs without contacting them (err %v, hits %d)", err, hits)
	}

	// Allow the test server itself, but not where it redirects to.
	c.AllowImage = func(u *url.URL) bool { return u.Host == strings.TrimPrefix(srv.URL, "http://") }
	if _, _, err := c.Download(ctx, srv.URL+"/a.jpg"); err != nil {
		t.Fatalf("allowed URL: %v", err)
	}
	if _, _, err := c.Download(ctx, srv.URL+"/redirect"); err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("redirect off the allowed host must fail, got %v", err)
	}
}

func TestLoginNeedsClientCredentials(t *testing.T) {
	if err := New("http://127.0.0.1:1").Login(context.Background(), "a", "b"); err == nil || !strings.Contains(err.Error(), "client ID") {
		t.Fatalf("got %v", err)
	}
}
