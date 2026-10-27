package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"eumetria/internal/google"
	"eumetria/internal/store"
)

// healthSource is what syncHealth needs from the Google Health API; tests
// replace it with a fake.
type healthSource interface {
	Workouts(ctx context.Context, from, to time.Time) ([]store.Workout, error)
	WorkoutHR(ctx context.Context, start, end time.Time) ([]store.HRMinute, error)
	WorkoutRoute(ctx context.Context, w store.Workout) ([]store.RoutePoint, error)
	Sleeps(ctx context.Context, from, to time.Time) ([]google.SleepSession, error)
	Dailies(ctx context.Context, from, to time.Time) (map[string]*google.Daily, error)
	DailyTotals(ctx context.Context, from, to time.Time) (map[string]*google.Totals, error)
	Devices(ctx context.Context) ([]store.Device, error)
}

// googleClient returns the Google Health client, or nil when Google is not
// configured (GOOGLE_CLIENT_ID unset): the Fitbit part is optional.
func googleClient(tokenPath string) (*google.Client, error) {
	if os.Getenv("GOOGLE_CLIENT_ID") == "" {
		return nil, nil
	}
	cfg, err := googleConfig()
	if err != nil {
		return nil, err
	}
	return google.New(cfg, tokenPath)
}

// healthWindow returns the first day to fetch from Google: the whole window
// on the first run, after -since moved back, or with refreshAll; otherwise
// only the most recent refreshDays days (Fitbit data can arrive late).
func healthWindow(h *store.History, since, today time.Time, refreshDays int, refreshAll bool) time.Time {
	if refreshAll || h.HealthFrom == "" || since.Format(time.DateOnly) < h.HealthFrom {
		return since
	}
	from := today.AddDate(0, 0, -(refreshDays - 1))
	if from.Before(since) {
		return since
	}
	return from
}

// firstLogged returns the first day with Yazio data, or since if none is later.
func firstLogged(h *store.History, since time.Time) time.Time {
	first := ""
	for date, d := range h.Days {
		if (d.Total.Energy != 0 || d.Weight != nil || d.Steps != 0 || d.Water != 0) && (first == "" || date < first) {
			first = date
		}
	}
	t, err := time.ParseInLocation(time.DateOnly, first, time.Local)
	if err != nil || t.Before(since) {
		return since
	}
	return t
}

// syncHealth replaces the Google Health data of every day in [from, today]
// with fresh data. Workout details (heart rate per minute, route) are only
// downloaded for new or changed workouts.
func syncHealth(ctx context.Context, src healthSource, h *store.History, from, today time.Time) error {
	to := today.AddDate(0, 0, 1)
	now := time.Now().UTC()

	workouts, err := src.Workouts(ctx, from, to)
	if err != nil {
		return err
	}
	// Sleep belongs to the day it ends on; a night ending the morning after
	// "to" is not over yet.
	sleeps, err := src.Sleeps(ctx, from, to)
	if err != nil {
		return err
	}
	dailies, dailyErr := src.Dailies(ctx, from, to)
	if dailyErr != nil && len(dailies) == 0 {
		return dailyErr
	}
	totals, totalsErr := src.DailyTotals(ctx, from, to)
	if totalsErr != nil {
		dailyErr = errors.Join(dailyErr, totalsErr)
	}
	// Battery and last sync for the page header. Optional: the API does not
	// document the scope it needs, so a failure only skips it.
	if devs, err := src.Devices(ctx); err != nil {
		log.Printf("google health: devices skipped: %v", err)
	} else {
		h.Devices = devs
	}

	old := map[string]store.Workout{}
	for _, d := range h.Health {
		for _, w := range d.Workouts {
			old[w.ID] = w
		}
	}

	days := map[string]*store.HealthDay{}
	day := func(date string) *store.HealthDay {
		if days[date] == nil {
			days[date] = &store.HealthDay{Date: date, FetchedAt: now}
		}
		return days[date]
	}

	detailErrs := 0
	for _, w := range workouts {
		if prev, ok := old[w.ID]; ok && prev.DetailsVersion == store.DetailsVersion && prev.UpdatedAt.Equal(w.UpdatedAt) {
			w.HR, w.Route, w.DetailsVersion = prev.HR, prev.Route, prev.DetailsVersion
		} else if err := fetchWorkoutDetails(ctx, src, &w); err != nil {
			detailErrs++
			log.Printf("workout %s %s: %v (retrying next sync)", localDate(w.Start, w.UTCOffset), w.Name, err)
		}
		d := day(localDate(w.Start, w.UTCOffset))
		d.Workouts = append(d.Workouts, w)
	}
	for _, s := range sleeps {
		if !s.Main {
			continue // naps
		}
		d := day(localDate(s.End, s.UTCOffset))
		if d.Sleep == nil || s.Asleep > d.Sleep.Asleep {
			sl := s.Sleep
			d.Sleep = &sl
		}
	}
	for date, v := range dailies {
		d := day(date)
		d.RestingHR, d.HRV, d.Respiratory = v.RestingHR, v.HRV, v.Respiratory
		d.HRVDeep, d.HRVEntropy, d.SleepHR = v.HRVDeep, v.HRVEntropy, v.SleepHR
		d.SpO2, d.SpO2Low, d.SpO2High = v.SpO2, v.SpO2Low, v.SpO2High
		d.SkinTemp, d.SkinBaseline = v.SkinTemp, v.SkinBaseline
		d.Zones = v.Zones
	}
	for date, v := range totals {
		d := day(date)
		d.TotalKcal, d.ZoneMinutes = v.Kcal, v.ZoneMinutes
	}

	// Replace the window: days without data in it are dropped, except when a
	// daily data type failed and its values would be lost.
	for t := from; t.Before(to); t = t.AddDate(0, 0, 1) {
		date := t.Format(time.DateOnly)
		nd, ok := days[date]
		if dailyErr != nil {
			if prev := h.Health[date]; prev != nil {
				if !ok {
					nd = &store.HealthDay{Date: date, FetchedAt: now}
				}
				keepDailies(nd, prev)
				ok = true
			}
		}
		if ok {
			sortWorkouts(nd.Workouts)
			h.Health[date] = nd
		} else {
			delete(h.Health, date)
		}
	}
	if h.HealthFrom == "" || from.Format(time.DateOnly) < h.HealthFrom {
		h.HealthFrom = from.Format(time.DateOnly)
	}
	log.Printf("google health: %d workouts, %d nights, %d days with daily values since %s",
		len(workouts), len(sleeps), len(dailies), from.Format(time.DateOnly))
	if dailyErr != nil {
		return dailyErr
	}
	if detailErrs > 0 {
		return fmt.Errorf("google health: %d workout details failed", detailErrs)
	}
	return nil
}

func fetchWorkoutDetails(ctx context.Context, src healthSource, w *store.Workout) error {
	hr, err := src.WorkoutHR(ctx, w.Start, w.End)
	if err != nil {
		return err
	}
	var route []store.RoutePoint
	if w.HasGPS {
		if route, err = src.WorkoutRoute(ctx, *w); err != nil {
			return err
		}
	}
	w.HR, w.Route, w.DetailsVersion = hr, route, store.DetailsVersion
	return nil
}

// keepDailies copies the daily values of prev into d where d has none.
func keepDailies(d, prev *store.HealthDay) {
	if d.RestingHR == 0 {
		d.RestingHR = prev.RestingHR
	}
	if d.HRV == 0 {
		d.HRV, d.HRVDeep, d.HRVEntropy, d.SleepHR = prev.HRV, prev.HRVDeep, prev.HRVEntropy, prev.SleepHR
	}
	if d.Respiratory == 0 {
		d.Respiratory = prev.Respiratory
	}
	if d.SpO2 == 0 {
		d.SpO2, d.SpO2Low, d.SpO2High = prev.SpO2, prev.SpO2Low, prev.SpO2High
	}
	if d.SkinTemp == 0 {
		d.SkinTemp, d.SkinBaseline = prev.SkinTemp, prev.SkinBaseline
	}
	if d.Zones == nil {
		d.Zones = prev.Zones
	}
	if d.TotalKcal == 0 {
		d.TotalKcal = prev.TotalKcal
	}
	if d.ZoneMinutes == nil {
		d.ZoneMinutes = prev.ZoneMinutes
	}
}

func sortWorkouts(ws []store.Workout) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && ws[j].Start.Before(ws[j-1].Start); j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}

// localDate is the calendar date of t in the time zone it was recorded in.
func localDate(t time.Time, utcOffset int) string {
	return t.UTC().Add(time.Duration(utcOffset) * time.Second).Format(time.DateOnly)
}

// recordHealthSync notes the outcome of a Google Health sync in the history.
func recordHealthSync(h *store.History, err error, now time.Time) {
	st := h.HealthSync
	if st == nil {
		st = &store.SyncState{}
		h.HealthSync = st
	}
	st.LastAttempt = now.UTC()
	st.Error, st.Auth = "", false
	if err != nil {
		st.Error = err.Error()
		st.Auth = errors.Is(err, google.ErrNotAuthorized)
		return
	}
	st.LastSuccess = now.UTC()
}
