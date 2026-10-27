package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"eumetria/internal/google"
	"eumetria/internal/store"
)

type fakeHealth struct {
	workouts  []store.Workout
	sleeps    []google.SleepSession
	dailies   map[string]*google.Daily
	dailyErr  error
	hrCalls   int
	routeCall int
}

func (f *fakeHealth) Workouts(context.Context, time.Time, time.Time) ([]store.Workout, error) {
	return f.workouts, nil
}
func (f *fakeHealth) WorkoutHR(context.Context, time.Time, time.Time) ([]store.HRMinute, error) {
	f.hrCalls++
	return []store.HRMinute{{Avg: 120, Max: 130}}, nil
}
func (f *fakeHealth) WorkoutRoute(context.Context, store.Workout) ([]store.RoutePoint, error) {
	f.routeCall++
	return []store.RoutePoint{{48, 11, 0, 520, 120}, {48.001, 11, 111, 524, 125}}, nil
}
func (f *fakeHealth) DailyTotals(context.Context, time.Time, time.Time) (map[string]*google.Totals, error) {
	return map[string]*google.Totals{"2026-10-16": {Kcal: 2450, ZoneMinutes: &[3]float64{12, 8, 0}}}, nil
}
func (f *fakeHealth) Devices(context.Context) ([]store.Device, error) {
	return []store.Device{{Type: "TRACKER", Name: "Google Fitbit Air", Battery: 64}}, nil
}
func (f *fakeHealth) Sleeps(context.Context, time.Time, time.Time) ([]google.SleepSession, error) {
	return f.sleeps, nil
}
func (f *fakeHealth) Dailies(context.Context, time.Time, time.Time) (map[string]*google.Daily, error) {
	return f.dailies, f.dailyErr
}

func TestSyncHealth(t *testing.T) {
	berlin := 7200
	// A late-evening ride: 23:30 local on the 15th is 21:30 UTC.
	ride := store.Workout{ID: "1", Type: "BIKING", Start: time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC),
		End: time.Date(2026, 10, 15, 21, 50, 0, 0, time.UTC), UTCOffset: berlin, HasGPS: true,
		UpdatedAt: time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC)}
	night := google.SleepSession{Main: true, Sleep: store.Sleep{Start: time.Date(2026, 10, 15, 20, 55, 0, 0, time.UTC),
		End: time.Date(2026, 10, 16, 2, 57, 0, 0, time.UTC), UTCOffset: berlin, Asleep: 355}}
	nap := google.SleepSession{Main: false, Sleep: store.Sleep{Start: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		End: time.Date(2026, 10, 16, 12, 30, 0, 0, time.UTC), UTCOffset: berlin, Asleep: 30}}
	src := &fakeHealth{
		workouts: []store.Workout{ride},
		sleeps:   []google.SleepSession{night, nap},
		dailies:  map[string]*google.Daily{"2026-10-16": {RestingHR: 63, HRV: 31.2, HRVDeep: 28, SleepHR: 55}},
	}
	from := time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local)
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h := &store.History{Health: map[string]*store.HealthDay{
		"2026-10-14": {Date: "2026-10-14", RestingHR: 70}, // in the window, no data any more: dropped
		"2026-10-01": {Date: "2026-10-01", RestingHR: 60}, // before the window: kept
	}}

	if err := syncHealth(context.Background(), src, h, from, today); err != nil {
		t.Fatal(err)
	}
	if d := h.Health["2026-10-15"]; d == nil || len(d.Workouts) != 1 || d.Workouts[0].DetailsVersion != store.DetailsVersion || len(d.Workouts[0].Route) != 2 {
		t.Errorf("ride not on its local date with details: %+v", d)
	}
	if d := h.Health["2026-10-16"]; d == nil || d.TotalKcal != 2450 || d.ZoneMinutes == nil || d.ZoneMinutes[1] != 8 {
		t.Errorf("daily totals missing: %+v", d)
	}
	if len(h.Devices) != 1 || h.Devices[0].Battery != 64 {
		t.Errorf("devices = %+v", h.Devices)
	}
	if d := h.Health["2026-10-16"]; d == nil || d.Sleep == nil || d.Sleep.Asleep != 355 || d.RestingHR != 63 || d.HRVDeep != 28 || d.SleepHR != 55 {
		t.Errorf("night (not the nap) and dailies belong to the wake-up day: %+v", d)
	}
	if h.Health["2026-10-14"] != nil || h.Health["2026-10-01"] == nil {
		t.Error("window not replaced correctly")
	}
	if h.HealthFrom != "2026-10-14" {
		t.Errorf("HealthFrom = %q", h.HealthFrom)
	}

	// Unchanged workouts keep their details without another download;
	// a changed one is fetched again.
	if err := syncHealth(context.Background(), src, h, from, today); err != nil {
		t.Fatal(err)
	}
	if src.hrCalls != 1 || src.routeCall != 1 {
		t.Errorf("details fetched %d/%d times, want once", src.hrCalls, src.routeCall)
	}
	src.workouts[0].UpdatedAt = src.workouts[0].UpdatedAt.Add(time.Hour)
	if err := syncHealth(context.Background(), src, h, from, today); err != nil {
		t.Fatal(err)
	}
	if src.hrCalls != 2 {
		t.Errorf("edited workout not re-fetched")
	}

	// A failing daily type keeps the stored values instead of wiping them.
	src.dailies, src.dailyErr = map[string]*google.Daily{"2026-10-15": {}}, errors.New("boom")
	if err := syncHealth(context.Background(), src, h, from, today); err == nil {
		t.Error("daily error not reported")
	}
	if d := h.Health["2026-10-16"]; d == nil || d.RestingHR != 63 {
		t.Errorf("daily values lost after a failed fetch: %+v", d)
	}
}

func TestHealthWindow(t *testing.T) {
	since := time.Date(2025, 10, 16, 0, 0, 0, 0, time.Local)
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h := &store.History{}
	if got := healthWindow(h, since, today, 3, false); !got.Equal(since) {
		t.Errorf("first run: %v", got)
	}
	h.HealthFrom = "2025-10-16"
	if got := healthWindow(h, since, today, 3, false); !got.Equal(today.AddDate(0, 0, -2)) {
		t.Errorf("later run: %v", got)
	}
	if got := healthWindow(h, since.AddDate(0, -1, 0), today, 3, false); !got.Equal(since.AddDate(0, -1, 0)) {
		t.Errorf("since moved back: %v", got)
	}
	if got := healthWindow(h, since, today, 3, true); !got.Equal(since) {
		t.Errorf("refresh all: %v", got)
	}
}

func TestRecordHealthSync(t *testing.T) {
	h := &store.History{}
	ok := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	recordHealthSync(h, nil, ok)
	if st := h.HealthSync; st == nil || !st.LastSuccess.Equal(ok) || st.Error != "" {
		t.Fatalf("after success: %+v", st)
	}
	fail := ok.Add(time.Hour)
	recordHealthSync(h, fmt.Errorf("token: %w", google.ErrNotAuthorized), fail)
	if st := h.HealthSync; !st.LastSuccess.Equal(ok) || !st.LastAttempt.Equal(fail) || st.Error == "" || !st.Auth {
		t.Errorf("after auth failure: %+v", st)
	}
	recordHealthSync(h, errors.New("HTTP 500"), fail)
	if st := h.HealthSync; st.Auth || st.Error != "HTTP 500" {
		t.Errorf("after other failure: %+v", st)
	}
}
