package stats

import (
	"testing"
	"time"

	"eumetria/internal/store"
)

func TestScaleWeightWinsOverYazio(t *testing.T) {
	w := func(v float64) *float64 { return &v }
	morning := time.Date(2026, 11, 18, 6, 10, 0, 0, time.UTC)
	h := &store.History{
		Days: map[string]*store.Day{
			"2026-11-17": {Date: "2026-11-17", Weight: w(79.0), Total: store.Macros{Energy: 2000}},
			"2026-11-18": {Date: "2026-11-18", Weight: w(79.0), Total: store.Macros{Energy: 2000}},
		},
		Body: map[string]*store.Body{
			"2": {GrpID: "2", Date: "2026-11-18", Time: morning.Add(10 * time.Hour), TZ: "Europe/Berlin", Measures: map[string]float64{"weight": 79.6}},
			"1": {GrpID: "1", Date: "2026-11-18", Time: morning, TZ: "Europe/Berlin", Measures: map[string]float64{"weight": 78.44, "fat_ratio": 18.2, "fat_mass": 14.28, "muscle_mass": 60.3}},
		},
		BodySync: &store.SyncState{Error: "invalid token", Auth: true},
	}
	out := Build(h, Options{Nutrition: true}, morning)
	var d17, d18 Day
	for _, d := range out.Days {
		switch d.Date {
		case "2026-11-17":
			d17 = d
		case "2026-11-18":
			d18 = d
		}
	}
	if *d17.Weight != 79 || d17.WeightSource != "" || d17.Body != nil {
		t.Errorf("a day without the scale = %+v", d17)
	}
	// The morning weigh-in, not the evening one; lean mass from weight − fat.
	if *d18.Weight != 78.4 || d18.WeightSource != "withings" || d18.Body.FatPct != 18.2 || d18.Body.LeanKg != 64.2 || d18.Body.Time != "07:10" {
		t.Errorf("the scale's day = %+v %+v", d18, d18.Body)
	}
	if out.BodySync == nil || !out.BodySync.Auth {
		t.Errorf("body sync = %+v", out.BodySync)
	}
	// Without nutrition, no weight from either.
	if o := Build(h, Options{Health: true}, morning); len(o.Days) > 0 && o.Days[0].Body != nil {
		t.Error("body without the nutrition scope")
	}
}
