package main

import (
	"errors"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/picture"
)

// The profile (PLAN M7): what users say about themselves, their picture and
// their API tokens. Only their own, and only from a session: a token can't
// make tokens. Everyone signed in sees pictures, since they sign notes.

const avatarSide = 512

type profileInfo struct {
	auth.Profile
	Name          string `json:"name"`
	Avatar        string `json:"avatar"`
	EmailVerified bool   `json:"email_verified"`
	EmailPending  string `json:"email_pending,omitempty"` // waiting for its confirmation
	Mail          bool   `json:"mail"`                    // the server can send mail
}

func (a *app) profile(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	prof, avatar, err := a.store.Profile(ctx, p.user)
	if err != nil {
		a.failed(w, "profile", err)
		return
	}
	st, err := a.store.Email(ctx, p.user)
	if err != nil {
		a.failed(w, "profile", err)
		return
	}
	_, mail := a.mailValues(ctx)
	writeJSON(w, http.StatusOK, profileInfo{prof, p.user.Name, avatar, st.Verified, st.Pending, mail})
}

func (a *app) profileSave(w http.ResponseWriter, req *http.Request, p *principal) {
	var in auth.Profile
	if !readJSON(w, req, &in) {
		return
	}
	prof, err := in.Clean()
	switch {
	case errors.Is(err, auth.ErrBadPersonName):
		writeError(w, http.StatusBadRequest, "bad_person_name")
		return
	case errors.Is(err, auth.ErrBadEmail):
		writeError(w, http.StatusBadRequest, "bad_email")
		return
	case errors.Is(err, auth.ErrBadInstagram):
		writeError(w, http.StatusBadRequest, "bad_instagram")
		return
	}
	// A new address goes its own way: confirmed first (email.go).
	current, _, err := a.store.Profile(req.Context(), p.user)
	if err != nil {
		a.failed(w, "profile", err)
		return
	}
	st, err := a.store.Email(req.Context(), p.user)
	if err != nil {
		a.failed(w, "profile", err)
		return
	}
	if prof.Email != current.Email && prof.Email != st.Pending && !a.changeEmail(w, req, p, prof.Email) {
		return
	}
	if err := a.store.SaveProfile(req.Context(), p.user, prof); err != nil {
		a.failed(w, "profile", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "profile_changed", "", clientIP(req))
	a.profile(w, req, p)
}

func avatarFile(id int64) string { return strconv.FormatInt(id, 10) + ".jpg" }

// avatarPost takes a new picture: decoded, cut to a square and written anew.
func (a *app) avatarPost(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Image []byte `json:"image"` // base64
	}
	if !readJSONUpTo(w, req, &in, maxImageBody) {
		return
	}
	if len(in.Image) > maxImage {
		writeError(w, http.StatusRequestEntityTooLarge, "too_long")
		return
	}
	data, err := picture.Clean(in.Image, picture.AvatarPixels, func(img *image.RGBA) *image.RGBA {
		return picture.Square(img, avatarSide)
	})
	if errors.Is(err, picture.ErrBusy) {
		writeError(w, http.StatusServiceUnavailable, "busy")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_image")
		return
	}
	err = picture.Save(a.avatars, avatarFile(p.user.ID), data)
	if err == nil {
		err = a.store.SetAvatar(req.Context(), p.user, true)
	}
	if err != nil {
		a.failed(w, "picture", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "picture_changed", "", clientIP(req))
	a.profile(w, req, p)
}

func (a *app) avatarDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	if err := a.store.SetAvatar(req.Context(), p.user, false); err != nil {
		a.failed(w, "picture", err)
		return
	}
	if err := os.Remove(filepath.Join(a.avatars, avatarFile(p.user.ID))); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("picture of %s: %v", p.user.Name, err)
	}
	a.audit_(req.Context(), p.user, "", "picture_removed", "", clientIP(req))
	a.profile(w, req, p)
}

// avatar serves a user's picture. Its address changes with the picture.
func (a *app) avatar(w http.ResponseWriter, req *http.Request, _ *principal) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, req)
		return
	}
	data, err := os.ReadFile(filepath.Join(a.avatars, avatarFile(id)))
	if err != nil {
		http.NotFound(w, req)
		return
	}
	personal(w, req, "image/jpeg", data)
}

/* ---------- API tokens ---------- */

func (a *app) tokens(w http.ResponseWriter, req *http.Request, p *principal) {
	list, err := a.store.APITokens(req.Context(), p.user)
	if err != nil {
		a.failed(w, "tokens", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// tokenCreate makes a token, which only this answer ever shows. A token can
// do what a stolen session can, for much longer, so it needs a recent
// sign-in, and syncing needs the second factor a sync session needs:
// otherwise a token would be the way around it.
func (a *app) tokenCreate(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
		Days   int      `json:"days"` // 0: never expires
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	ctx := req.Context()
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }) {
		writeError(w, http.StatusBadRequest, "bad_name")
		return
	}
	if in.Days < 0 || in.Days > 3650 {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if slices.Contains(in.Scopes, auth.ScopeSync) {
		if !p.user.Can(auth.ScopeSync) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if f, err := a.store.Factors(ctx, p.user); err != nil || !f.Any() {
			writeError(w, http.StatusForbidden, "second_factor_required")
			return
		}
	}
	var expires time.Time
	if in.Days > 0 {
		expires = a.store.Now().AddDate(0, 0, in.Days)
	}
	token, err := a.store.CreateAPIToken(ctx, p.user, name, in.Scopes, expires)
	if errors.Is(err, auth.ErrBadScope) {
		writeError(w, http.StatusBadRequest, "bad_scopes")
		return
	}
	if err != nil {
		a.failed(w, "token", err)
		return
	}
	a.audit_(ctx, p.user, "", "token_created", name+" ("+strings.Join(in.Scopes, " ")+")", clientIP(req))
	a.notify(ctx, p.user, "token_created", name, clientIP(req), p)
	writeJSON(w, http.StatusCreated, map[string]string{"token": token})
}

func (a *app) tokenDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	name, err := a.store.DeleteAPIToken(req.Context(), p.user, id)
	if err != nil {
		a.failed(w, "token", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "token_deleted", name, clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
