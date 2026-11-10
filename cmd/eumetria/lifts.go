package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"eumetria/internal/lifts"
)

// Personal bests in lifts (Power Clean, Strict Press, …), written in the
// Training card. Unlike notes they belong to no workout: they are part of
// the training data, so view.health reads them and edit.lifts writes them.

// liftsList answers GET /api/lifts: every entry, by lift, the oldest first.
func (a *app) liftsList(w http.ResponseWriter, req *http.Request, p *principal) {
	list, err := a.lifts.List(req.Context())
	if err != nil {
		a.failed(w, "lifts", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// liftInput reads and checks an entry from the request body.
func (a *app) liftInput(w http.ResponseWriter, req *http.Request) (lifts.Input, bool) {
	var in lifts.Input
	if !readJSON(w, req, &in) {
		return in, false
	}
	in, err := in.Clean(a.store.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_lift")
		return in, false
	}
	return in, true
}

func liftID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return 0, false
	}
	return id, true
}

// liftDetail is what the audit log says about an entry.
func liftDetail(e lifts.Entry) string {
	return fmt.Sprintf("%s %s %g kg × %d", e.Date, e.Lift, e.Kg, e.Reps)
}

// liftPost adds an entry.
func (a *app) liftPost(w http.ResponseWriter, req *http.Request, p *principal) {
	in, ok := a.liftInput(w, req)
	if !ok {
		return
	}
	e, err := a.lifts.Add(req.Context(), in, p.user.ID)
	if err != nil {
		a.failed(w, "lifts", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "lift_added", liftDetail(e), clientIP(req))
	writeJSON(w, http.StatusCreated, e)
}

// liftPut changes an entry.
func (a *app) liftPut(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	in, ok := a.liftInput(w, req)
	if !ok {
		return
	}
	e, err := a.lifts.Update(req.Context(), id, in, p.user.ID)
	if errors.Is(err, lifts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "lifts", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "lift_changed", liftDetail(e), clientIP(req))
	writeJSON(w, http.StatusOK, e)
}

// liftDelete removes an entry.
func (a *app) liftDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	e, err := a.lifts.Delete(req.Context(), id)
	if errors.Is(err, lifts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "lifts", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "lift_deleted", liftDetail(e), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
