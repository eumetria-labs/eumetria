package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"eumetria/internal/settings"
	"eumetria/internal/store"
	"eumetria/internal/withings"
)

// Withings (a body composition scale): the app's client is in the settings
// (Data sources → Withings), Connect Withings gets the token, which is kept
// sealed in the settings table (it rotates with every refresh), and each
// sync asks for what changed since the last one. Pre-staged: see
// internal/withings and PLAN.md, phase 10.

const withingsTokenKey = "withings_token"

// secretStore keeps the server's own secrets: settings.Store.
type secretStore interface {
	Internal(ctx context.Context, key string) (string, error)
	SaveInternal(ctx context.Context, key, plain string) error
}

func loadWithingsToken(ctx context.Context, s secretStore) (*withings.Token, error) {
	if s == nil {
		return nil, nil
	}
	raw, err := s.Internal(ctx, withingsTokenKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var tok withings.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveWithingsToken(ctx context.Context, s secretStore, tok withings.Token) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return s.SaveInternal(ctx, withingsTokenKey, string(raw))
}

// withingsSource syncs the scale's weigh-ins, when it is connected. Like
// Google Health it never fails the run: a problem is recorded for the
// page's warning.
type withingsSource struct {
	values  settings.Values
	secrets secretStore
	zone    *time.Location
}

func (withingsSource) Name() string { return "withings" }

func (s withingsSource) Sync(ctx context.Context, h *store.History, w window) error {
	if s.values.WithingsClientID == "" || s.values.WithingsSecret == "" {
		return nil
	}
	tok, err := loadWithingsToken(ctx, s.secrets)
	if tok == nil && err == nil {
		return nil // not connected
	}
	if err == nil {
		cfg := withings.Config{ClientID: s.values.WithingsClientID, ClientSecret: s.values.WithingsSecret}
		c := withings.New(cfg, *tok, func(t withings.Token) error { return saveWithingsToken(ctx, s.secrets, t) })
		var groups []withings.Group
		var next int64
		groups, next, err = c.Measurements(ctx, h.BodyUpdate)
		n := addWeighIns(h, groups, s.zone)
		if err == nil {
			h.BodyUpdate = next
			log.Printf("withings: %d weigh-ins new or changed, %d in history", n, len(h.Body))
		}
	}
	if err != nil {
		log.Printf("WARNING: withings: %v", err)
	}
	recordBodySync(h, err, w.now)
	return nil
}

// addWeighIns puts the real measurements of groups into the history (goals
// and other categories left out) and answers how many.
func addWeighIns(h *store.History, groups []withings.Group, zone *time.Location) int {
	n := 0
	for _, g := range groups {
		if g.Category != 1 || len(g.Measures) == 0 {
			continue
		}
		at := time.Unix(g.Date, 0).UTC()
		loc := zone
		if l, err := time.LoadLocation(g.Timezone); g.Timezone != "" && err == nil {
			loc = l
		}
		if loc == nil {
			loc = time.UTC
		}
		b := &store.Body{GrpID: strconv.FormatInt(g.GrpID, 10), Date: at.In(loc).Format(time.DateOnly), Time: at, TZ: g.Timezone,
			Device: g.DeviceID, Modified: g.Modified, Measures: map[string]float64{}}
		for _, m := range g.Measures {
			b.Measures[withings.KeyOf(m.Type)] = m.Real()
		}
		if h.Body == nil {
			h.Body = map[string]*store.Body{}
		}
		h.Body[b.GrpID] = b
		n++
	}
	return n
}

func recordBodySync(h *store.History, err error, now time.Time) {
	st := h.BodySync
	if st == nil {
		st = &store.SyncState{}
		h.BodySync = st
	}
	st.LastAttempt = now.UTC()
	st.Error, st.Auth = "", false
	if err != nil {
		st.Error = err.Error()
		st.Auth = errors.Is(err, withings.ErrAuth)
		return
	}
	st.LastSuccess = now.UTC()
}

/* ---------- Connect Withings ---------- */

// withingsCallback is the address to register as the app's callback URL.
func (a *app) withingsCallback(req *http.Request) string {
	return a.siteURL(req) + "/api/withings/callback"
}

type withingsStatus struct {
	Connected bool   `json:"connected"`
	UserID    string `json:"user_id,omitempty"`
	Callback  string `json:"callback"`
}

func (a *app) withingsState(ctx context.Context, req *http.Request) withingsStatus {
	st := withingsStatus{Callback: a.withingsCallback(req)}
	if tok, err := loadWithingsToken(ctx, a.runner.settings); err == nil && tok != nil {
		st.Connected, st.UserID = true, tok.UserID
	}
	return st
}

// withingsConnect starts the consent; withingsCallbackRoute goes on only
// with this state and for this admin.
func (a *app) withingsConnect(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	if !a.runner.settings.CanEncrypt() {
		writeError(w, http.StatusConflict, "no_key")
		return
	}
	v, err := a.runner.settings.Values(req.Context())
	if err != nil {
		a.failed(w, "withings", err)
		return
	}
	if v.WithingsClientID == "" || v.WithingsSecret == "" {
		writeError(w, http.StatusConflict, "no_client")
		return
	}
	state, err := a.pending.put("", pendingWithings, "", &pending{user: p.user, secret: "withings", name: a.withingsCallback(req)})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "busy")
		return
	}
	cfg := withings.Config{ClientID: v.WithingsClientID, ClientSecret: v.WithingsSecret, RedirectURL: a.withingsCallback(req)}
	writeJSON(w, http.StatusOK, map[string]string{"url": cfg.AuthURL(state)})
}

// withingsCallbackRoute is where Withings sends the browser back. The code
// is valid for 30 seconds: it is exchanged here, at once.
func (a *app) withingsCallbackRoute(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	done := func(result string) {
		http.Redirect(w, req, "/#settings=withings&withings="+result, http.StatusSeeOther)
	}
	q := req.URL.Query()
	saved := a.pending.take(q.Get("state"), pendingWithings)
	if saved == nil || saved.user.ID != p.user.ID {
		a.audit_(ctx, p.user, "", "withings_failed", "state", clientIP(req))
		done("expired")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		done("denied")
		return
	}
	v, err := a.runner.settings.Values(ctx)
	if err != nil {
		a.failed(w, "withings", err)
		return
	}
	cfg := withings.Config{ClientID: v.WithingsClientID, ClientSecret: v.WithingsSecret, RedirectURL: saved.name}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tok, err := cfg.Exchange(ctx, &http.Client{Timeout: 20 * time.Second}, q.Get("code"), time.Now())
	if err == nil {
		err = saveWithingsToken(ctx, a.runner.settings, tok)
	}
	if err != nil {
		log.Printf("withings: %v", err)
		a.audit_(ctx, p.user, "", "withings_failed", "exchange", clientIP(req))
		done("failed")
		return
	}
	a.audit_(ctx, p.user, "", "withings_connected", "", clientIP(req))
	done("connected")
}

// withingsDisconnect forgets the token. Withings has no revoke call in the
// documented API; the grant also ends unused after a year, and can be ended
// in the Withings account.
func (a *app) withingsDisconnect(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	tok, err := loadWithingsToken(req.Context(), a.runner.settings)
	if err != nil {
		a.failed(w, "withings", err)
		return
	}
	if tok == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := a.runner.settings.SaveInternal(req.Context(), withingsTokenKey, ""); err != nil {
		a.failed(w, "withings", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "withings_disconnected", "", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
