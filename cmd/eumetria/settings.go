package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"eumetria/internal/google"
	"eumetria/internal/settings"
)

// The admin settings (PLAN M8). Every route here is adminOnly; what changes
// something needs a recent sign-in too. A change applies at once: the running
// stats get the new title and time zone, the scheduler the new interval, and
// each sync reads the rest when it starts.

// Replaced in tests: these talk to Yazio and Google.
var (
	testYazio = func(ctx context.Context, v settings.Values) error {
		_, err := login(ctx, v)
		return err
	}
	exchangeGoogle = func(ctx context.Context, cfg google.Config, code string) (*google.Token, error) {
		return cfg.Exchange(ctx, &http.Client{Timeout: 30 * time.Second}, code)
	}
	revokeGoogle = func(ctx context.Context, tok *google.Token) error {
		return google.Revoke(ctx, &http.Client{Timeout: 10 * time.Second}, tok)
	}
)

// siteScheme is how the browser reaches the site, as in relyingParty: behind
// nginx the request itself is plain http and can't tell.
func siteScheme(req *http.Request) string {
	host := req.Host
	if h, _, err := net.SplitHostPort(req.Host); err == nil {
		host = h
	}
	if req.TLS == nil && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
		return "http"
	}
	return "https"
}

// googleCallback is the address to register as the OAuth client's redirect
// URI.
func googleCallback(req *http.Request) string {
	return siteScheme(req) + "://" + req.Host + "/api/google/callback"
}

type googleStatus struct {
	Connected bool       `json:"connected"`
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	Missing   []string   `json:"missing,omitempty"` // scopes not granted
	Callback  string     `json:"callback"`
}

func (a *app) settingsGet(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	view, err := a.runner.settings.View(ctx)
	if err != nil {
		a.failed(w, "settings", err)
		return
	}
	gs := googleStatus{Callback: googleCallback(req)}
	if tok, err := google.LoadToken(a.runner.cfg.googleToken); err == nil {
		gs.Connected, gs.Missing = true, missingScopes(tok.Scope)
		if !tok.IssuedAt.IsZero() {
			gs.IssuedAt = &tok.IssuedAt
		}
	}
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		a.failed(w, "settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":    view,
		"can_encrypt": a.runner.settings.CanEncrypt(),
		"zone":        map[string]string{"name": v.ZoneName, "env": os.Getenv("TZ")},
		"google":      gs,
		"status":      a.runner.status(),
	})
}

func (a *app) settingsSave(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Values map[string]string `json:"values"`
		Reset  []string          `json:"reset"`
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	ctx := req.Context()
	err := a.runner.settings.Save(ctx, in.Values, in.Reset)
	var bad settings.BadValue
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_value", "key": bad.Key})
		return
	case errors.Is(err, settings.ErrNoKey):
		writeError(w, http.StatusConflict, "no_key")
		return
	case errors.Is(err, settings.ErrUnknown):
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	case err != nil:
		a.failed(w, "settings", err)
		return
	}
	// The names only: values may be secrets.
	keys := slices.Concat(in.Reset)
	for k := range in.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	a.audit_(ctx, p.user, "", "settings_changed", strings.Join(keys, " "), clientIP(req))
	if err := a.applySettings(ctx); err != nil {
		a.failed(w, "settings", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applySettings hands the new values to what is already running.
func (a *app) applySettings(ctx context.Context) error {
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		return err
	}
	cfg := a.runner.reconfigure(v)
	a.data.setOptions(baseOptions(cfg))
	return nil
}

// settingsTestYazio signs in with the saved values.
func (a *app) settingsTestYazio(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	v, err := a.runner.settings.Values(ctx)
	if err == nil {
		err = testYazio(ctx, v)
	}
	if err != nil {
		log.Printf("yazio test: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// reviewsRewrite has every stored review written again, with what the server
// holds, one at a time. It costs money, so the page asks first.
func (a *app) reviewsRewrite(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	code := http.StatusAccepted
	if a.runner.startRewrite(context.WithoutCancel(req.Context())) {
		a.audit_(req.Context(), p.user, "", "reviews_rewritten", "", clientIP(req))
	} else {
		code = http.StatusConflict
	}
	writeJSON(w, code, a.runner.status())
}

/* ---------- Connect Google ---------- */

// googleConnect starts the consent. The browser goes to Google and comes back
// to googleCallbackRoute, which only goes on with this state, for this admin,
// and with the verifier that only this server has (PKCE).
func (a *app) googleConnect(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	v, err := a.runner.settings.Values(req.Context())
	if err != nil {
		a.failed(w, "google", err)
		return
	}
	if v.GoogleClientID == "" || v.GoogleSecret == "" {
		writeError(w, http.StatusConflict, "no_client")
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		a.failed(w, "google", err)
		return
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	state := a.pending.put("", &pending{user: p.user, secret: verifier, name: googleCallback(req)})
	cfg := google.Config{ClientID: v.GoogleClientID, ClientSecret: v.GoogleSecret, RedirectURL: googleCallback(req), Verifier: verifier}
	writeJSON(w, http.StatusOK, map[string]string{"url": cfg.AuthURL(state)})
}

// googleCallbackRoute is where Google sends the browser back. The answer goes
// back to the settings, which say how it went.
func (a *app) googleCallbackRoute(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	done := func(result string) {
		http.Redirect(w, req, "/#settings=google&google="+result, http.StatusSeeOther)
	}
	q := req.URL.Query()
	saved := a.pending.take(q.Get("state"))
	if saved == nil || saved.user.ID != p.user.ID || saved.secret == "" {
		a.audit_(ctx, p.user, "", "google_failed", "state", clientIP(req))
		done("expired")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		done("denied")
		return
	}
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		a.failed(w, "google", err)
		return
	}
	cfg := google.Config{ClientID: v.GoogleClientID, ClientSecret: v.GoogleSecret, RedirectURL: saved.name, Verifier: saved.secret}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tok, err := exchangeGoogle(ctx, cfg, q.Get("code"))
	if err == nil {
		err = google.SaveToken(a.runner.cfg.googleToken, tok)
	}
	if err != nil {
		log.Printf("google: %v", err)
		a.audit_(ctx, p.user, "", "google_failed", "exchange", clientIP(req))
		done("failed")
		return
	}
	a.audit_(ctx, p.user, "", "google_connected", "", clientIP(req))
	done("connected")
}

// googleDisconnect forgets the token and tells Google, as far as it answers.
func (a *app) googleDisconnect(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	path := a.runner.cfg.googleToken
	tok, err := google.LoadToken(path)
	if errors.Is(err, google.ErrNotAuthorized) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "google", err)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	if err := revokeGoogle(ctx, tok); err != nil {
		log.Printf("google: revoking the token: %v", err)
	}
	if err := os.Remove(path); err != nil {
		a.failed(w, "google", err)
		return
	}
	a.audit_(ctx, p.user, "", "google_disconnected", "", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
