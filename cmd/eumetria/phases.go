package main

import (
	"errors"
	"fmt"
	"net/http"

	"eumetria/internal/auth"
	"eumetria/internal/phases"
)

// Phases (a cut, a bulk, …) and events (a competition), noted on the page.
// They frame the weight, nutrition and training data, so view.nutrition or
// view.health reads them; edit.lifts writes them, like the other training
// entries people note themselves.

// phasesList answers GET /api/phases: every one, the earliest first.
func (a *app) phasesList(w http.ResponseWriter, req *http.Request, p *principal) {
	if !p.can(auth.ViewNutrition) && !p.can(auth.ViewHealth) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	list, err := a.phases.List(req.Context())
	if err != nil {
		a.failed(w, "phases", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *app) phaseInput(w http.ResponseWriter, req *http.Request) (phases.Input, bool) {
	var in phases.Input
	if !readJSON(w, req, &in) {
		return in, false
	}
	in, err := in.Clean(a.store.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_phase")
		return in, false
	}
	return in, true
}

// phaseDetail is what the audit log says about one.
func phaseDetail(ph phases.Phase) string {
	return fmt.Sprintf("%s %q %s–%s", ph.Kind, ph.Label, ph.Start, ph.End)
}

func (a *app) phasePost(w http.ResponseWriter, req *http.Request, p *principal) {
	in, ok := a.phaseInput(w, req)
	if !ok {
		return
	}
	ph, err := a.phases.Add(req.Context(), in, p.user.ID)
	if err != nil {
		a.failed(w, "phases", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "phase_added", phaseDetail(ph), clientIP(req))
	writeJSON(w, http.StatusCreated, ph)
}

func (a *app) phasePut(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	in, ok := a.phaseInput(w, req)
	if !ok {
		return
	}
	ph, err := a.phases.Update(req.Context(), id, in, p.user.ID)
	if errors.Is(err, phases.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "phases", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "phase_changed", phaseDetail(ph), clientIP(req))
	writeJSON(w, http.StatusOK, ph)
}

func (a *app) phaseDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	ph, err := a.phases.Delete(req.Context(), id)
	if errors.Is(err, phases.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.failed(w, "phases", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "phase_deleted", phaseDetail(ph), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
