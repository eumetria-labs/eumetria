package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"eumetria/internal/checkins"
)

// The daily check-in and the day's tags (internal/checkins): view.health
// reads them, edit.checkin writes them. A day tagged sick or travel leaves
// the scores' baselines, so a change builds the stats again.

// checkinsList answers GET /api/checkins?from=&to= (both dates; without
// them, the last 400 days).
func (a *app) checkinsList(w http.ResponseWriter, req *http.Request, p *principal) {
	from, to := req.URL.Query().Get("from"), req.URL.Query().Get("to")
	if from == "" && to == "" {
		now := a.store.Now()
		from, to = now.AddDate(0, 0, -400).Format("2006-01-02"), now.AddDate(0, 0, 1).Format("2006-01-02")
	}
	if !checkins.ValidDate(from) || !checkins.ValidDate(to) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	list, err := a.checkins.List(req.Context(), from, to)
	if err != nil {
		a.failed(w, "checkins", err)
		return
	}
	if list == nil {
		list = []checkins.Checkin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkins": list, "tags": checkins.Tags})
}

func (a *app) checkinPut(w http.ResponseWriter, req *http.Request, p *principal) {
	date := req.PathValue("date")
	if !checkins.ValidDate(date) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	var in checkins.Input
	if !readJSON(w, req, &in) {
		return
	}
	in, err := in.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_checkin")
		return
	}
	c, err := a.checkins.Put(req.Context(), date, in, p.user.ID)
	if err != nil {
		a.failed(w, "checkins", err)
		return
	}
	a.audit_(req.Context(), p.user, "", "checkin_saved", date+" "+strings.Join(c.Tags, ","), clientIP(req))
	a.skipChanged(req.Context())
	writeJSON(w, http.StatusOK, c)
}

func (a *app) checkinDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	date := req.PathValue("date")
	if !checkins.ValidDate(date) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	ok, err := a.checkins.Delete(req.Context(), date)
	if err != nil {
		a.failed(w, "checkins", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.audit_(req.Context(), p.user, "", "checkin_deleted", date, clientIP(req))
	a.skipChanged(req.Context())
	w.WriteHeader(http.StatusNoContent)
}

// skipChanged gives the stats the days tagged sick or travel again.
func (a *app) skipChanged(ctx context.Context) {
	if a.data == nil {
		return
	}
	skip, err := a.checkins.SkipDays(ctx)
	if err != nil {
		log.Printf("checkins: the sick and travel days: %v", err)
		return
	}
	a.data.setSkip(skip)
}
