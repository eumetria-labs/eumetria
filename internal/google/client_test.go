package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeGoogle answers the token endpoint and one list endpoint with two pages.
func fakeGoogle(t *testing.T, rotate bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "rt-1" || r.FormValue("client_secret") != "secret" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
				return
			}
			resp := map[string]any{"access_token": "at-1", "expires_in": 3599}
			if rotate {
				resp["refresh_token"] = "rt-2"
			}
			json.NewEncoder(w).Encode(resp)
		case "/users/me/dataTypes/exercise/dataPoints":
			if r.Header.Get("Authorization") != "Bearer at-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("pageToken") == "" {
				w.Write([]byte(`{"dataPoints":[{"name":"a"}],"nextPageToken":"p2"}`))
			} else {
				w.Write([]byte(`{"dataPoints":[{"name":"b"}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := tokenURL
	tokenURL = srv.URL + "/token"
	t.Cleanup(func() { tokenURL = old })
	return srv
}

var testConfig = Config{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://stats.example.com"}

func TestListFromTokenFile(t *testing.T) {
	srv := fakeGoogle(t, true)
	path := filepath.Join(t.TempDir(), "google-token.json")
	if err := SaveToken(path, &Token{RefreshToken: "rt-1"}); err != nil {
		t.Fatal(err)
	}
	c, err := New(testConfig, path)
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	points, err := c.List(context.Background(), "exercise", "", "", 0)
	if err != nil || len(points) != 2 {
		t.Fatalf("List = %d points, %v; want 2 across both pages", len(points), err)
	}
	saved, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "at-1" || saved.RefreshToken != "rt-2" {
		t.Errorf("token file not updated: %+v", saved)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRevokedRefreshToken(t *testing.T) {
	srv := fakeGoogle(t, false)
	path := filepath.Join(t.TempDir(), "google-token.json")
	if err := SaveToken(path, &Token{RefreshToken: "revoked"}); err != nil {
		t.Fatal(err)
	}
	c, err := New(testConfig, path)
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	_, err = c.List(context.Background(), "exercise", "", "", 0)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("err = %v, want ErrNotAuthorized", err)
	}
}

func TestNoToken(t *testing.T) {
	if _, err := New(testConfig, filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("err = %v, want ErrNotAuthorized", err)
	}
}
