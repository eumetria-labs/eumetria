package stats

import (
	"testing"
	"time"

	"eumetria/internal/store"
)

func TestBuildHealth(t *testing.T) {
	start := time.Date(2026, 9, 24, 14, 32, 0, 0, time.UTC)
	h := &store.History{
		Days: map[string]*store.Day{"2026-09-24": {Date: "2026-09-24", Total: store.Macros{Energy: 2000}, Activities: []store.Activity{
			{Name: "cycling", Energy: 233, ExternalID: "6762"},
			{Name: "Yoga", Energy: 100}, // logged in Yazio by hand
		}}},
		Health: map[string]*store.HealthDay{
			"2026-09-24": {Date: "2026-09-24", RestingHR: 62, HRV: 35.44, HRVDeep: 33.84, HRVEntropy: 2.918, SleepHR: 54, SkinTemp: 33.01, SkinBaseline: 33.43,
				Zones: &[3]float64{109, 133, 163},
				Workouts: []store.Workout{{
					ID: "6762", Type: "BIKING", Start: start, End: start.Add(29 * time.Minute), UTCOffset: 7200,
					ActiveSec: 1744.98, Calories: 254, AvgHR: 83, ZoneSec: [4]float64{1740, 0, 0, 0},
					HR:     []store.HRMinute{{Start: start, Avg: 80.4, Max: 88}, {Start: start.Add(time.Minute), Avg: 95.6, Max: 101}},
					Pauses: [][2]time.Time{{start.Add(10 * time.Minute), start.Add(12 * time.Minute)}},
					Route:  []store.RoutePoint{{48, 11, 0, 520, 120}, {48.001, 11, 111, 524, 125}},
					Splits: []store.Split{{DistanceM: 1000, ActiveSec: 258}},
				}},
			},
			// A day with only Fitbit data still appears.
			"2026-09-25": {Date: "2026-09-25", Sleep: &store.Sleep{
				Start: time.Date(2026, 9, 24, 20, 55, 0, 0, time.UTC), End: time.Date(2026, 9, 25, 2, 57, 0, 0, time.UTC),
				UTCOffset: 7200, Asleep: 355, Awake: 7, Deep: 82,
				Stages: []store.SleepStage{{Start: time.Date(2026, 9, 24, 21, 6, 0, 0, time.UTC), End: time.Date(2026, 9, 24, 21, 13, 0, 0, time.UTC), Stage: "awake"}},
			}},
		},
	}
	out := Build(h, Options{Nutrition: true, Activity: true, Health: true, Routes: true}, time.Now())
	if len(out.Days) != 2 {
		t.Fatalf("got %d days, want 2", len(out.Days))
	}
	d := out.Days[0]
	if d.RestingHR != 62 || d.HRV != 35.4 || d.HRVDeep != 33.8 || d.HRVEntropy != 2.92 || d.SleepHR != 54 || d.SkinTemp == nil || *d.SkinTemp != -0.4 {
		t.Errorf("daily values: rhr %v hrv %v/%v/%v sleep hr %v temp %v", d.RestingHR, d.HRV, d.HRVDeep, d.HRVEntropy, d.SleepHR, d.SkinTemp)
	}
	if d.Activities[0].Workout != "6762" || d.Activities[1].Workout != "" {
		t.Errorf("activity links = %q, %q", d.Activities[0].Workout, d.Activities[1].Workout)
	}
	w := d.Workouts[0]
	if w.Start != "16:32" || w.Duration != 1745 || w.Elapsed != 1740 || w.MaxHR != 101 || *w.Limits != [3]float64{109, 133, 163} {
		t.Errorf("workout = %+v", w)
	}
	if len(w.HR) != 2 || w.HR[1] != [2]float64{60, 96} || w.Pauses[0] != [2]float64{600, 720} || len(w.Route) != 2 || w.Route[1][4] != 125 || w.Splits[0] != [2]float64{1000, 258} {
		t.Errorf("hr %v pauses %v route %v", w.HR, w.Pauses, w.Route)
	}
	s := out.Days[1].Sleep
	if s == nil || s.Start != "22:55" || s.End != "04:57" || len(s.Stages) != 1 || s.Stages[0] != [3]float64{11, 7, 0} {
		t.Errorf("sleep = %+v", s)
	}

	// Fitbit days before the first Yazio day are left out.
	h.Health["2026-01-01"] = &store.HealthDay{Date: "2026-01-01", RestingHR: 70}
	if out = Build(h, Options{Health: true}, time.Now()); out.Days[0].Date != "2026-09-24" {
		t.Errorf("page starts on %s, want the first Yazio day", out.Days[0].Date)
	}

	out = Build(h, Options{Health: true}, time.Now())
	if out.Days[0].Workouts[0].Route != nil {
		t.Error("route published with Routes off")
	}
	out = Build(h, Options{Nutrition: true, Activity: true}, time.Now())
	if len(out.Days) != 1 || out.Days[0].Workouts != nil || out.Days[0].Activities[0].Workout != "" {
		t.Error("health data published with Health off")
	}
}

// Activities Yazio imports through Health Connect carry no workout ID and are
// linked by start time; manual ones stay unlinked.
func TestBuildHealthConnectLink(t *testing.T) {
	start := time.Date(2026, 10, 29, 18, 1, 46, 0, time.UTC)
	h := &store.History{
		Days: map[string]*store.Day{"2026-10-29": {Date: "2026-10-29", Total: store.Macros{Energy: 2000}, Activities: []store.Activity{
			{Name: "hiit", Energy: 823, Duration: 75, Start: "2026-10-29 19:01:46"},
			{Name: "Europabad", Energy: 550, Duration: 240},             // logged in Yazio by hand
			{Name: "walking", Energy: 90, Start: "2026-10-29 08:00:00"}, // no workout near it
		}}},
		Health: map[string]*store.HealthDay{"2026-10-29": {Date: "2026-10-29", Workouts: []store.Workout{
			{ID: "3262930630979810696", Type: "CROSSFIT", Start: start, End: start.Add(75 * time.Minute), UTCOffset: 3600},
		}}},
	}
	d := Build(h, Options{Activity: true, Health: true}, time.Now()).Days[0]
	if got := []string{d.Activities[0].Workout, d.Activities[1].Workout, d.Activities[2].Workout}; got[0] != "3262930630979810696" || got[1] != "" || got[2] != "" {
		t.Errorf("activity links = %q", got)
	}
}

func TestBuildHealthSync(t *testing.T) {
	ok := time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC)
	h := &store.History{Days: map[string]*store.Day{}, HealthSync: &store.SyncState{LastSuccess: ok, Error: "secret API body", Auth: true}}
	out := Build(h, Options{Health: true}, time.Now())
	if s := out.HealthSync; s == nil || !s.Failing || !s.Auth || s.LastSuccess == nil || !s.LastSuccess.Equal(ok) {
		t.Errorf("HealthSync = %+v", s)
	}
	h.HealthSync.Error = ""
	if out := Build(h, Options{Health: true}, time.Now()); out.HealthSync != nil {
		t.Errorf("a working sync is not published: %+v", out.HealthSync)
	}
}
