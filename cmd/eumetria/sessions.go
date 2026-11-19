package main

import (
	"errors"
	"fmt"
	"net/http"

	"eumetria/internal/sessions"
)

// Training sessions, set by set: part of the training data like lifts, so
// view.health reads them and edit.lifts writes them. A session linked to a
// Fitbit workout takes its date; work sets marked as bests become lift
// entries linked to the session.

// sessionsList answers GET /api/training/sessions: every session, the oldest first.
func (a *app) sessionsList(w http.ResponseWriter, req *http.Request, p *principal) {
	list, err := a.training.List(req.Context())
	if err != nil {
		a.failed(w, "sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *app) sessionInput(w http.ResponseWriter, req *http.Request, p *principal) (sessions.Input, bool) {
	var in sessions.Input
	if !readJSONUpTo(w, req, &in, 64<<10) {
		return in, false
	}
	in, err := in.Clean(a.store.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_session")
		return in, false
	}
	if in.FitbitWorkout != "" {
		b, ok := a.build(w, req, p)
		if !ok {
			return in, false
		}
		date, found := b.workouts[in.FitbitWorkout]
		if !found {
			writeError(w, http.StatusBadRequest, "bad_workout")
			return in, false
		}
		in.Date = date
	}
	return in, true
}

// sessionDetail is what the audit log says about a session.
func sessionDetail(x sessions.Session) string {
	s := fmt.Sprintf("%s, %d sets", x.Date, len(x.Sets))
	if len(x.Bests) > 0 {
		s += fmt.Sprintf(", %d bests", len(x.Bests))
	}
	return s
}

func (a *app) sessionPost(w http.ResponseWriter, req *http.Request, p *principal) {
	in, ok := a.sessionInput(w, req, p)
	if !ok {
		return
	}
	x, err := a.training.Add(req.Context(), in, p.user.ID)
	if err != nil {
		a.failed(w, "sessions", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "session_added", sessionDetail(x), clientIP(req))
	writeJSON(w, http.StatusCreated, x)
}

func (a *app) sessionPut(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	in, ok := a.sessionInput(w, req, p)
	if !ok {
		return
	}
	x, err := a.training.Update(req.Context(), id, in, p.user.ID)
	if errors.Is(err, sessions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "sessions", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "session_changed", sessionDetail(x), clientIP(req))
	writeJSON(w, http.StatusOK, x)
}

func (a *app) sessionDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	x, err := a.training.Delete(req.Context(), id)
	if errors.Is(err, sessions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "sessions", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "session_deleted", sessionDetail(x), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
