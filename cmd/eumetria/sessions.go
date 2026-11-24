package main

import (
	"errors"
	"fmt"
	"net/http"

	"eumetria/internal/sessions"
)

// Trainings (PLAN phase 14): the hub of a training, with its device
// workouts and its sets; its note, lifts and benchmark results link to it.
// Part of the training data like lifts, so view.health reads them and
// edit.lifts writes them. A training with workouts takes the day of the
// first; work sets marked as bests become lift entries linked to it.

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
	if len(in.Workouts) > 0 {
		b, ok := a.build(w, req, p)
		if !ok {
			return in, false
		}
		first := ""
		for _, r := range in.Workouts {
			date, found := b.workouts[r.ID]
			if !found {
				writeError(w, http.StatusBadRequest, "bad_workout")
				return in, false
			}
			if first == "" || date < first {
				first = date
			}
		}
		in.Date = first
	}
	return in, true
}

// trainingOf puts what is noted for the Fitbit workout workout (a lift, a
// benchmark result, a note) into that workout's training, making one when
// it has none (sessions.Store.Resolve, prefer as there), and returns the
// training and the workout's day. A workout the principal can't see is
// refused with 400 bad_workout, or 404 when notFound.
func (a *app) trainingOf(w http.ResponseWriter, req *http.Request, p *principal, workout string, prefer *int64, notFound bool) (int64, string, bool) {
	b, ok := a.build(w, req, p)
	if !ok {
		return 0, "", false
	}
	date, found := b.workouts[workout]
	if !found {
		if notFound {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			writeError(w, http.StatusBadRequest, "bad_workout")
		}
		return 0, "", false
	}
	id, err := a.training.Resolve(req.Context(), sessions.Ref{Source: sessions.Fitbit, ID: workout}, date, prefer, p.user.ID)
	if err != nil {
		a.failed(w, "sessions", err)
		return 0, "", false
	}
	return id, date, true
}

// linkTraining sets the training of a lift or benchmark result: the one of
// its Fitbit workout (made when needed), else the training it names, which
// must exist. It reports false after answering itself.
func (a *app) linkTraining(w http.ResponseWriter, req *http.Request, p *principal, workout string, session **int64, date *string) bool {
	if workout != "" {
		id, day, ok := a.trainingOf(w, req, p, workout, *session, false)
		if !ok {
			return false
		}
		*session, *date = &id, day
		return true
	}
	if *session == nil {
		return true
	}
	found, err := a.training.Exists(req.Context(), **session)
	if err != nil {
		a.failed(w, "sessions", err)
		return false
	}
	if !found {
		writeError(w, http.StatusBadRequest, "bad_session")
		return false
	}
	return true
}

// sessionSaveError answers a failed save.
func (a *app) sessionSaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sessions.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, sessions.ErrTaken):
		writeError(w, http.StatusConflict, "workout_taken")
	default:
		a.failed(w, "sessions", err)
	}
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
		a.sessionSaveError(w, err)
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
	if err != nil {
		a.sessionSaveError(w, err)
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
