package main

import (
	"errors"
	"fmt"
	"net/http"

	"eumetria/internal/wods"
)

// Benchmark workouts (Fran, a WOD the box repeats, …) and their results,
// in the Training card next to the lifts and under the same scopes:
// view.health reads them, edit.lifts writes them.

// wodsList answers GET /api/wods: every workout by name and every result,
// the oldest first.
func (a *app) wodsList(w http.ResponseWriter, req *http.Request, p *principal) {
	ws, err := a.wods.Workouts(req.Context())
	if err != nil {
		a.failed(w, "wods", err)
		return
	}
	rs, err := a.wods.Results(req.Context())
	if err != nil {
		a.failed(w, "wods", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workouts": ws, "results": rs})
}

// wodError answers the store's errors that people can fix; it reports
// whether err was one of them or nil.
func (a *app) wodError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, wods.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, wods.ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken")
	case errors.Is(err, wods.ErrHasResults):
		writeError(w, http.StatusConflict, "has_results")
	default:
		a.failed(w, "wods", err)
	}
	return true
}

func (a *app) workoutInput(w http.ResponseWriter, req *http.Request) (wods.WorkoutInput, bool) {
	var in wods.WorkoutInput
	if !readJSON(w, req, &in) {
		return in, false
	}
	in, err := in.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_wod")
		return in, false
	}
	return in, true
}

// resultInput reads and checks a result. One linked to a Fitbit workout
// takes that workout's date; a workout the principal can't see is refused.
func (a *app) resultInput(w http.ResponseWriter, req *http.Request, p *principal, scoring string) (wods.ResultInput, bool) {
	var in wods.ResultInput
	if !readJSON(w, req, &in) {
		return in, false
	}
	in, err := in.Clean(scoring, a.store.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_result")
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

// resultDetail is what the audit log says about a result.
func resultDetail(wd wods.Workout, r wods.Result) string {
	s := fmt.Sprintf("%s %s %s", r.Date, wd.Name, wods.Format(wd.Scoring, r))
	if r.Scaled {
		s += " (scaled)"
	}
	if r.FitbitWorkout != "" {
		s += " in Fitbit workout " + r.FitbitWorkout
	}
	return s
}

func (a *app) wodPost(w http.ResponseWriter, req *http.Request, p *principal) {
	in, ok := a.workoutInput(w, req)
	if !ok {
		return
	}
	wd, err := a.wods.AddWorkout(req.Context(), in, p.user.ID)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_added", wd.Name, clientIP(req))
	writeJSON(w, http.StatusCreated, wd)
}

func (a *app) wodPut(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	in, ok := a.workoutInput(w, req)
	if !ok {
		return
	}
	wd, err := a.wods.UpdateWorkout(req.Context(), id, in, p.user.ID)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_changed", wd.Name, clientIP(req))
	writeJSON(w, http.StatusOK, wd)
}

func (a *app) wodDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	wd, n, err := a.wods.DeleteWorkout(req.Context(), id)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_deleted", fmt.Sprintf("%s (%d results)", wd.Name, n), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) wodResultPost(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	wd, err := a.wods.Workout(req.Context(), id)
	if a.wodError(w, err) {
		return
	}
	in, ok := a.resultInput(w, req, p, wd.Scoring)
	if !ok {
		return
	}
	r, err := a.wods.AddResult(req.Context(), id, in, p.user.ID)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_result_added", resultDetail(wd, r), clientIP(req))
	writeJSON(w, http.StatusCreated, r)
}

func (a *app) wodResultPut(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	wd, err := a.wods.ResultWorkout(req.Context(), id)
	if a.wodError(w, err) {
		return
	}
	in, ok := a.resultInput(w, req, p, wd.Scoring)
	if !ok {
		return
	}
	r, err := a.wods.UpdateResult(req.Context(), id, in, p.user.ID)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_result_changed", resultDetail(wd, r), clientIP(req))
	writeJSON(w, http.StatusOK, r)
}

func (a *app) wodResultDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	id, ok := liftID(w, req)
	if !ok {
		return
	}
	wd, err := a.wods.ResultWorkout(req.Context(), id)
	if a.wodError(w, err) {
		return
	}
	r, err := a.wods.DeleteResult(req.Context(), id)
	if a.wodError(w, err) {
		return
	}
	a.audit_(req.Context(), p.user, "", "wod_result_deleted", resultDetail(wd, r), clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}
