package review

import (
	"strings"
	"testing"
	"time"

	"eumetria/internal/checkins"
	"eumetria/internal/phases"
	"eumetria/internal/stats"
)

func TestWeekSleepTiming(t *testing.T) {
	monday := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	var out stats.Output
	for d := monday.AddDate(0, 0, -21); !d.After(monday.AddDate(0, 0, 6)); d = d.AddDate(0, 0, 1) {
		sl := &stats.Sleep{Start: "23:00", End: "06:30"}
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			sl = &stats.Sleep{Start: "00:30", End: "09:30"}
		}
		out.Days = append(out.Days, stats.Day{Date: d.Format(time.DateOnly), Sleep: sl})
	}
	w := BuildWeek(out, monday, "2026-11-20")
	st := w.SleepTiming
	if st == nil || st.Regularity.Nights != 7 || st.Regularity.SRI == nil || st.Chronotype == nil || *st.Chronotype.JetlagMin != 135 || st.Chronotype.MSFsc != "04:28" {
		t.Fatalf("sleep timing = %+v", st)
	}
	// Three weekends away leave too few free nights.
	var away []checkins.Checkin
	for _, d := range []string{"2026-10-24", "2026-10-31", "2026-11-07"} {
		away = append(away, checkins.Checkin{Date: d, Tags: []string{"travel"}})
	}
	w.AddCheckins(away)
	if w.SleepTiming.Chronotype != nil {
		t.Errorf("chronotype with three weekends away = %+v", w.SleepTiming.Chronotype)
	}
}

func TestWeekGoalHits(t *testing.T) {
	monday := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	out := stats.Output{Goals: stats.Goals{Steps: 10000}, SleepGoal: 450}
	day := func(i int, kcal, protein, steps, asleep, alcohol float64) stats.Day {
		d := stats.Day{Date: monday.AddDate(0, 0, i).Format(time.DateOnly), Energy: kcal, EnergyGoal: 2000, EnergyBase: 2000, Protein: protein, ProteinGoal: 140, Steps: steps}
		if asleep > 0 {
			d.Sleep = &stats.Sleep{Asleep: asleep}
		}
		d.Nutrients = map[string][2]float64{"alcohol": {alcohol, 1}}
		return d
	}
	out.Days = []stats.Day{
		day(0, 2100, 150, 12000, 470, 0), // all met (2100 within ±10 %)
		day(1, 1700, 120, 8000, 400, 20), // under the calories: missed outside a cut
		day(2, 0, 0, 9000, 480, 0),       // nothing logged: only steps and sleep count
	}
	w := BuildWeek(out, monday, "2026-11-20")
	got := map[string][2]int{}
	for _, g := range w.GoalHits {
		got[g.Goal] = [2]int{g.Hit, g.Of}
	}
	want := map[string][2]int{"protein": {1, 2}, "calories": {1, 2}, "steps": {1, 3}, "sleep": {2, 3}, "alcohol_free": {1, 2}}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all %v)", k, got[k], v, got)
		}
	}
	// In a cut, 1700 kcal is on goal and 2100 isn't.
	w.AddPhases([]phases.Phase{{Kind: phases.Cut, Start: "2026-11-01"}})
	for _, g := range w.GoalHits {
		if g.Goal == "calories" && (g.Hit != 1 || g.Of != 2 || !strings.Contains(g.Rule, "cut")) {
			t.Errorf("calories in a cut = %+v", g)
		}
	}
}
