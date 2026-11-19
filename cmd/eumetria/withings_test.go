package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"eumetria/internal/settings"
	"eumetria/internal/store"
	"eumetria/internal/withings"
)

// fakeWithings answers like Withings' API (the documented shapes): a
// nonce, tokens that rotate, and one page of measurements.
func fakeWithings(t *testing.T, expired *bool) *int {
	issued := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/v2/signature":
			fmt.Fprint(w, `{"status":0,"body":{"nonce":"n"}}`)
		case "/v2/oauth2":
			if *expired {
				fmt.Fprint(w, `{"status":401,"error":"invalid refresh_token"}`)
				return
			}
			issued++
			fmt.Fprintf(w, `{"status":0,"body":{"userid":777,"access_token":"a%d","refresh_token":"r%d","expires_in":10800}}`, issued, issued)
		case "/measure":
			fmt.Fprint(w, `{"status":0,"body":{"updatetime":1763535600,"timezone":"Europe/Berlin","more":0,"measuregrps":[
				{"grpid":5,"date":1763442000,"modified":1763442100,"category":1,"measures":[{"value":78420,"type":1,"unit":-3},{"value":182,"type":6,"unit":-1}]},
				{"grpid":6,"date":1763442000,"modified":1763442100,"category":2,"measures":[{"value":75,"type":1,"unit":0}]}]}}`)
		}
	}))
	t.Cleanup(srv.Close)
	old := withings.APIBase
	withings.APIBase = srv.URL
	t.Cleanup(func() { withings.APIBase = old })
	return &issued
}

// memSecrets is a secretStore in memory.
type memSecrets map[string]string

func (m memSecrets) Internal(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memSecrets) SaveInternal(_ context.Context, k, v string) error {
	if v == "" {
		delete(m, k)
	} else {
		m[k] = v
	}
	return nil
}

func TestWithingsSource(t *testing.T) {
	expired := false
	fakeWithings(t, &expired)
	ctx := context.Background()
	secrets := memSecrets{}
	vals := settings.Values{WithingsClientID: "cid", WithingsSecret: "sec"}
	h := &store.History{}
	now := time.Unix(1763535600, 0)
	src := withingsSource{values: vals, secrets: secrets, zone: time.UTC}

	// Not connected: nothing happens.
	if err := src.Sync(ctx, h, window{now: now}); err != nil || h.Body != nil || h.BodySync != nil {
		t.Fatalf("not connected: %v %+v", err, h)
	}
	saveWithingsToken(ctx, secrets, withings.Token{UserID: "777", AccessToken: "old", RefreshToken: "r0", Expiry: now.Add(-time.Hour)})
	if err := src.Sync(ctx, h, window{now: now}); err != nil {
		t.Fatal(err)
	}
	b := h.Body["5"]
	if len(h.Body) != 1 || b == nil || b.Date != "2025-11-18" || b.Measures["weight"] != 78.42 || b.Measures["fat_ratio"] != 18.2 || b.TZ != "Europe/Berlin" {
		t.Errorf("body = %+v", h.Body)
	}
	if h.BodyUpdate != 1763535600 || h.BodySync == nil || h.BodySync.Error != "" {
		t.Errorf("update %d, sync %+v", h.BodyUpdate, h.BodySync)
	}
	// The refreshed token was kept, rotated.
	tok, _ := loadWithingsToken(ctx, secrets)
	if tok.RefreshToken != "r1" || tok.UserID != "777" {
		t.Errorf("token = %+v", tok)
	}
	// A refresh Withings refuses asks to connect again; the data stays.
	expired = true
	sec := memSecrets{}
	saveWithingsToken(ctx, sec, withings.Token{RefreshToken: "gone", Expiry: now.Add(-time.Hour)})
	src.secrets = sec
	if err := src.Sync(ctx, h, window{now: now}); err != nil || !h.BodySync.Auth || len(h.Body) != 1 {
		t.Errorf("expired: %v %+v", err, h.BodySync)
	}
}

func TestConnectWithings(t *testing.T) {
	e := newTestEnv(t, true)
	expired := false
	fakeWithings(t, &expired)
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "POST", "/api/withings/connect", "{}", sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_client") {
		t.Errorf("without a client = %d %s", res.StatusCode, body)
	}
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"withings_client_id":"cid","withings_client_secret":"client secret"}}`, sameSite)
	res, body := e.do(t, admin, "POST", "/api/withings/connect", "{}", sameSite)
	var ans struct{ URL string }
	json.Unmarshal([]byte(body), &ans)
	u, _ := url.Parse(ans.URL)
	q := u.Query()
	if res.StatusCode != 200 || q.Get("scope") != "user.metrics" || !strings.HasSuffix(q.Get("redirect_uri"), "/api/withings/callback") || strings.Contains(ans.URL, "secret") {
		t.Fatalf("connect = %d %s", res.StatusCode, body)
	}
	noFollow := *admin
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, _ = e.do(t, &noFollow, "GET", "/api/withings/callback?state="+url.QueryEscape(q.Get("state"))+"&code=c", "")
	if res.StatusCode != 303 || res.Header.Get("Location") != "/#settings=withings&withings=connected" {
		t.Fatalf("callback = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// The token is sealed in the settings, never shown.
	_, raw := e.do(t, admin, "GET", "/api/admin/settings", "")
	if !strings.Contains(raw, `"withings":{"connected":true,"user_id":"777"`) || strings.Contains(raw, `"a1"`) || strings.Contains(raw, "r1") {
		t.Errorf("settings = %s", raw)
	}
	var stored string
	e.store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'withings_token'`).Scan(&stored)
	if stored == "" || strings.Contains(stored, "refresh_token") || !strings.HasPrefix(stored, "v1:") {
		t.Errorf("stored token = %q", stored)
	}
	// The same state works once; then disconnect.
	res, _ = e.do(t, &noFollow, "GET", "/api/withings/callback?state="+url.QueryEscape(q.Get("state"))+"&code=c", "")
	if !strings.HasSuffix(res.Header.Get("Location"), "withings=expired") {
		t.Errorf("again = %s", res.Header.Get("Location"))
	}
	if res, _ := e.do(t, admin, "DELETE", "/api/withings", "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("disconnect = %d", res.StatusCode)
	}
	if _, raw := e.do(t, admin, "GET", "/api/admin/settings", ""); !strings.Contains(raw, `"withings":{"connected":false`) {
		t.Errorf("after disconnect = %s", raw)
	}
	// Only admins.
	anna := e.signIn(t, "anna", viewerPassword, false)
	if res, _ := e.do(t, anna, "POST", "/api/withings/connect", "{}", sameSite); res.StatusCode != 403 {
		t.Errorf("anna = %d", res.StatusCode)
	}
}
