package review

import (
	"strings"
	"testing"
	"time"

	"eumetria/internal/stats"
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
