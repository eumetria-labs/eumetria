package review

import (
	"strings"
	"testing"
	"time"

	"eumetria/internal/lifts"
	"eumetria/internal/stats"
	"eumetria/internal/wods"
)

func TestMonday(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-19": "2026-10-19", // Monday
		"2026-10-22": "2026-10-19",
		"2026-10-25": "2026-10-19", // Sunday belongs to the week before it
		"2026-10-26": "2026-10-26",
	} {
		d, _ := time.Parse(time.DateOnly, in)
		if got := Monday(d).Format(time.DateOnly); got != want {
			t.Errorf("Monday(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestBuildWeek(t *testing.T) {
	w := 80.0
	out := stats.Output{
		Days: []stats.Day{
			{Date: "2026-10-12", Energy: 2000, Steps: 1000}, // previous week
			{Date: "2026-10-19", Energy: 1500, Steps: 8000, Weight: &w, Foods: []stats.Food{
				{Name: "Bowl", Daytime: "breakfast", Energy: 400, Recipe: "r"},
				{Name: "Apple", Daytime: "snack", Energy: 100},
			}},
			{Date: "2026-10-20", Energy: 300, Steps: 2000, Sleep: &stats.Sleep{Start: "23:10", End: "06:40", Asleep: 410}}, // today, still being logged
		},
		Recipes: map[string]stats.Recipe{"r": {Name: "Bowl", Portions: 1, Ingredients: []stats.Ingredient{
			{Name: "Skyr", Energy: 300, Barcode: "1"}, {Name: "Oats", Energy: 100, Barcode: "2"},
		}}},
		Products: map[string]stats.Product{"1": {NutriScore: "a", NOVA: 3}, "2": {NOVA: 1}},
	}
	monday, _ := time.Parse(time.DateOnly, "2026-10-19")
	wk := BuildWeek(out, monday, "2026-10-20")
	if wk.From != "2026-10-19" || wk.To != "2026-10-25" || !wk.Running || wk.Today != "2026-10-20" || len(wk.Days) != 2 {
		t.Fatalf("week = %s..%s running=%v today=%q days=%d", wk.From, wk.To, wk.Running, wk.Today, len(wk.Days))
	}
	// Today is listed but stays out of the averages.
	if s := wk.Summary; s.DaysLogged != 1 || s.AvgKcal != 1500 || s.AvgSteps != 8000 || *s.WeightLast != 80 {
		t.Errorf("summary = %+v", s)
	}
	if p := wk.Previous; p.DaysLogged != 1 || p.AvgKcal != 2000 {
		t.Errorf("previous = %+v", p)
	}
	// The recipe counts by its ingredients: 300 of 400 kcal Skyr, 100 oats.
	if n := wk.Summary.NOVAShare; n["3"] != 0.6 || n["1"] != 0.2 || n["unknown"] != 0.2 {
		t.Errorf("NOVA shares = %v", n)
	}
	if s := wk.Summary; s.AvgWeightKg != 80 || s.WeighIns != 1 || s.LabelledShare != 0.8 {
		t.Errorf("weight %v (%d weigh-ins), labelled share %v", s.AvgWeightKg, s.WeighIns, s.LabelledShare)
	}
	if s := wk.Days[1].Sleep; s == nil || s.Night != "Monday to Tuesday" {
		t.Errorf("sleep = %+v", s)
	}
	if f := wk.Days[0].Foods[0]; len(f.Recipe) != 2 || f.Recipe[0] != "Skyr" {
		t.Errorf("recipe food = %+v", f)
	}
}

func TestEncodeKeepsAmpersands(t *testing.T) {
	s, err := encode(Week{Days: []Day{{Foods: []Food{{Name: "Cremig & Quarkig"}}}}})
	if err != nil || !strings.Contains(s, "Cremig & Quarkig") {
		t.Errorf("encoded: %s (%v)", s, err)
	}
}

func TestBilling(t *testing.T) {
	for _, c := range []struct {
		status int
		msg    string
		want   bool
	}{
		{400, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`, true},
		{400, "messages: at least one message is required", false},
		{429, "rate limit exceeded; your spend limit", false},
		{403, "workspace spend limit reached", true},
	} {
		if got := billing(c.status, c.msg); got != c.want {
			t.Errorf("billing(%d, %q) = %v", c.status, c.msg, got)
		}
	}
}

func TestDur(t *testing.T) {
	for in, want := range map[float64]string{0: "", 37: "37 min", 59.6: "1 h", 438: "7 h 18 min", 180: "3 h"} {
		if got := dur(in); got != want {
			t.Errorf("dur(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestAddLifts(t *testing.T) {
	w := Week{From: "2026-10-19", To: "2026-10-25", Days: []Day{{Date: "2026-10-19"}, {Date: "2026-10-20"}}}
	w.AddLifts([]lifts.Entry{
		{ID: 1, Lift: "Power Clean", Kg: 80, Reps: 1, Date: "2026-10-01"},
		{ID: 2, Lift: "power clean", Kg: 82.5, Reps: 1, Date: "2026-10-20", Note: "finally"}, // a best, later in the list
		{ID: 3, Lift: "Power Clean", Kg: 75, Reps: 1, Date: "2026-10-19"},                    // not one
		{ID: 4, Lift: "Back Squat", Kg: 100, Reps: 5, Date: "2026-10-19"},                    // the first
		{ID: 5, Lift: "Back Squat", Kg: 120, Reps: 5, Date: "2026-10-22"},                    // after today
		{ID: 6, Lift: "Snatch", Kg: 50, Reps: 1, Date: "2026-10-12"},                         // the week before
	})
	want := []Lift{
		{Date: "2026-10-19", Lift: "Power Clean", Kg: 75, Reps: 1},
		{Date: "2026-10-19", Lift: "Back Squat", Kg: 100, Reps: 5, First: true},
		{Date: "2026-10-20", Lift: "power clean", Kg: 82.5, Reps: 1, Best: true, PrevKg: 80, Note: "finally"},
	}
	if len(w.Lifts) != len(want) {
		t.Fatalf("lifts = %+v", w.Lifts)
	}
	for i := range want {
		if w.Lifts[i] != want[i] {
			t.Errorf("lift %d = %+v, want %+v", i, w.Lifts[i], want[i])
		}
	}
}

func TestAddBenchmarks(t *testing.T) {
	w := Week{From: "2026-10-19", To: "2026-10-25", Days: []Day{{Date: "2026-10-19"}, {Date: "2026-10-20"}}}
	w.AddBenchmarks([]wods.Workout{{ID: 1, Name: "Fran", Scoring: wods.Time}, {ID: 2, Name: "Cindy", Scoring: wods.Rounds}}, []wods.Result{
		{ID: 1, Workout: 1, Value: 300, Date: "2026-10-01"},
		{ID: 2, Workout: 1, Value: 280, Date: "2026-10-19"},               // faster: a best
		{ID: 3, Workout: 1, Value: 200, Scaled: true, Date: "2026-10-19"}, // scaled: its own first
		{ID: 4, Workout: 2, Value: 12, Reps: 5, Date: "2026-10-10"},
		{ID: 5, Workout: 2, Value: 12, Reps: 3, Date: "2026-10-20"}, // fewer reps: not one
		{ID: 6, Workout: 9, Value: 1, Date: "2026-10-20"},           // no such workout
	})
	want := []Benchmark{
		{Date: "2026-10-19", Workout: "Fran", Scoring: "time", Result: "4:40", Best: true, Prev: "5:00"},
		{Date: "2026-10-19", Workout: "Fran", Scoring: "time", Result: "3:20", Scaled: true, First: true},
		{Date: "2026-10-20", Workout: "Cindy", Scoring: "rounds", Result: "12 rounds + 3 reps"},
	}
	if len(w.Benchmarks) != len(want) {
		t.Fatalf("benchmarks = %+v", w.Benchmarks)
	}
	for i := range want {
		if w.Benchmarks[i] != want[i] {
			t.Errorf("benchmark %d = %+v, want %+v", i, w.Benchmarks[i], want[i])
		}
	}
}
