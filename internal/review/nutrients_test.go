package review

import (
	"slices"
	"testing"
	"time"

	"eumetria/internal/checkins"
	"eumetria/internal/stats"
)

func TestWeekAlcoholAndLowNutrients(t *testing.T) {
	monday := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	out := stats.Output{NutrientRefs: map[string]stats.Reference{
		"magnesium": {Amount: 350, Kind: "estimated"}, "vitamin_d": {Amount: 20, Kind: "estimated"},
		"iron": {Amount: 11, Kind: "recommended"}, "iodine": {Amount: 150, Kind: "recommended"},
		"calcium": {Amount: 1000, Kind: "recommended"}, "fiber": {Amount: 30, Kind: "guide"},
	}}
	for i := 0; i < 7; i++ {
		out.Days = append(out.Days, stats.Day{Date: monday.AddDate(0, 0, i).Format(time.DateOnly), Energy: 2000, Nutrients: map[string][2]float64{
			"alcohol":   {float64(i), 1},
			"magnesium": {280, 0.9}, // 80 %
			"vitamin_d": {4, 0.9},   // 20 %
			"iron":      {13, 0.9},  // over: left out
			"iodine":    {60, 0.5},  // too little known: left out
			"calcium":   {700, 0.8}, // 70 %
			"fiber":     {10, 0.9},  // fibre isn't a mineral or vitamin
		}})
	}
	w := BuildWeek(out, monday, "2026-11-20")
	if w.Days[3].AlcoholG != 3 {
		t.Errorf("alcohol on Thursday = %v", w.Days[3].AlcoholG)
	}
	got := w.NutrientsLow
	if len(got) != 3 || got[0].Name != "vitamin d" || got[0].Percent != 20 || got[1].Name != "calcium" || got[2].Name != "magnesium" {
		t.Fatalf("low = %+v", got)
	}
	if got[0].Unit != "µg" || got[0].Reference != 20 || got[0].Known != 0.9 {
		t.Errorf("vitamin D = %+v", got[0])
	}
	// Without references (no sex set) nothing.
	out.NutrientRefs = nil
	if w := BuildWeek(out, monday, "2026-11-20"); w.NutrientsLow != nil {
		t.Errorf("low without references = %+v", w.NutrientsLow)
	}
}

func TestAddCheckins(t *testing.T) {
	n := func(v int) *int { return &v }
	w := Week{Days: []Day{{Date: "2026-11-13", AlcoholG: 40}, {Date: "2026-11-14", AlcoholG: 30}, {Date: "2026-11-15"}, {Date: "2026-11-16", AlcoholG: 5}}}
	w.AddCheckins([]checkins.Checkin{
		{Date: "2026-11-14", Dismissed: []string{"alcohol"}},
		{Date: "2026-11-15", Energy: n(2), Tags: []string{"sick"}},
	})
	if !slices.Equal(w.Days[0].Tags, []string{"alcohol"}) || w.Days[0].Checkin != nil {
		t.Errorf("40 g, no check-in: %+v", w.Days[0])
	}
	if len(w.Days[1].Tags) != 0 || w.Days[1].Checkin != nil {
		t.Errorf("alcohol tag removed: %+v", w.Days[1])
	}
	if !slices.Equal(w.Days[2].Tags, []string{"sick"}) || *w.Days[2].Checkin.Energy != 2 {
		t.Errorf("sick: %+v", w.Days[2])
	}
	if len(w.Days[3].Tags) != 0 {
		t.Errorf("5 g: %+v", w.Days[3])
	}
}
