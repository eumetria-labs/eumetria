package coach

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"eumetria/internal/auth"
	"eumetria/internal/lifts"
	"eumetria/internal/stats"
)

func call(t *testing.T, d *Data, name, args string) map[string]any {
	t.Helper()
	out, err := Call(context.Background(), d, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func allViews() []string { return auth.ViewScopes }

func TestFixedOrderAndScopes(t *testing.T) {
	var names []string
	for _, tl := range Tools() {
		names = append(names, tl.Name)
		var s map[string]any
		if err := json.Unmarshal(tl.Schema, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: schema %v %v", tl.Name, s, err)
		}
	}
	if got := strings.Join(names, " "); got != "overview daily_metrics entries food_sources workouts sleep body checkins lifts sessions wods phases" {
		t.Errorf("order = %s", got)
	}
	if n := len(For(nil)); n != 1 { // overview only
		t.Errorf("no views: %d tools", n)
	}
	d := &Data{Views: []string{auth.ViewActivity}, Today: "2026-11-20"}
	_, err := Call(context.Background(), d, "entries", nil)
	var ae *ArgError
	if !errors.As(err, &ae) {
		t.Errorf("entries without view.foods: %v", err)
	}
	if _, err := Call(context.Background(), d, "nope", nil); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown tool: %v", err)
	}
}

func TestRangeIsCut(t *testing.T) {
	var days []stats.Day
	for i := 0; i < 200; i++ {
		days = append(days, stats.Day{Date: addDays("2026-01-01", i), Steps: float64(1000 + i)})
	}
	d := &Data{Out: stats.Output{Days: days}, Views: allViews(), Today: addDays("2026-01-01", 199)}
	m := call(t, d, "daily_metrics", `{"from":"2026-01-01"}`)
	if n := len(m["days"].([]any)); n != MaxDays {
		t.Errorf("%d days, want %d", n, MaxDays)
	}
	if !strings.Contains(m["note"].(string), "Cut to the last 120 days") || m["from"] != addDays(d.Today, -(MaxDays-1)) {
		t.Errorf("note %v, from %v", m["note"], m["from"])
	}
	m = call(t, d, "daily_metrics", `{}`)
	if n := len(m["days"].([]any)); n != DefaultDays || m["note"] != nil {
		t.Errorf("default: %d days, note %v", n, m["note"])
	}
	if _, err := Call(context.Background(), d, "daily_metrics", json.RawMessage(`{"from":"2026-03-01","to":"2026-02-01"}`)); err == nil {
		t.Error("from after to: no error")
	}
	if _, err := Call(context.Background(), d, "daily_metrics", json.RawMessage(`{"fields":["vibes"]}`)); err == nil {
		t.Error("unknown field: no error")
	}
}

func TestFoodSources(t *testing.T) {
	fiber := 3.0
	d := &Data{Views: allViews(), Today: "2026-11-02", Out: stats.Output{
		FoodNutrients: []map[string]float64{{"iron": 2, "alcohol": 10}},
		Days: []stats.Day{
			{Date: "2026-11-01", Foods: []stats.Food{
				{Name: "Lentils", Daytime: "lunch", Protein: 18, Fiber: &fiber, NutrientFood: 1, NutrientScale: 1.5},
				{Name: "Beer", Daytime: "dinner", Protein: 1, NutrientFood: 1, NutrientScale: 2},
			}},
			{Date: "2026-11-02", Foods: []stats.Food{{Name: "Lentils", Daytime: "lunch", Protein: 18, NutrientFood: 1, NutrientScale: 1}}},
		}}}
	m := call(t, d, "food_sources", `{"nutrient":"protein"}`)
	foods := m["foods"].([]any)
	first := foods[0].(map[string]any)
	if first["name"] != "Lentils" || first["total"] != 36.0 || first["entries"] != 2.0 || first["share_pct"] != 97.3 || m["unit"] != "g" {
		t.Errorf("protein = %v", m)
	}
	m = call(t, d, "food_sources", `{"nutrient":"iron"}`)
	if m["unit"] != "mg" || m["range_total"] != 9.0 { // 2×1.5 + 2×2 + 2×1
		t.Errorf("iron = %v", m)
	}
	m = call(t, d, "food_sources", `{"nutrient":"alcohol"}`)
	if first := m["foods"].([]any)[0].(map[string]any); first["name"] != "Lentils" || first["total"] != 25.0 {
		t.Errorf("alcohol = %v", m) // lentils' made-up alcohol: 15 + 10
	}
	if _, err := Call(context.Background(), d, "food_sources", json.RawMessage(`{"nutrient":"vibes"}`)); err == nil {
		t.Error("unknown nutrient: no error")
	}
	m = call(t, d, "entries", `{"date":"2026-11-01","meal":"dinner"}`)
	if day := m["days"].([]any)[0].(map[string]any); len(day["foods"].([]any)) != 1 {
		t.Errorf("entries = %v", m)
	}
}

func TestLiftsBestAndClip(t *testing.T) {
	d := &Data{Views: allViews(), Lifts: func(context.Context) ([]lifts.Entry, error) {
		return []lifts.Entry{
			{Lift: "Back Squat", Kg: 100, Reps: 3, Date: "2026-10-01"},
			{Lift: "Back Squat", Kg: 100, Reps: 5, Date: "2026-09-01", Note: strings.Repeat("x", 500)},
			{Lift: "Back Squat", Kg: 90, Reps: 8, Date: "2026-11-01"},
			{Lift: "Deadlift", Kg: 140, Reps: 1, Date: "2026-10-05"},
		}, nil
	}}
	m := call(t, d, "lifts", `{"lift":"back squat"}`)
	ls := m["lifts"].([]any)
	if len(ls) != 1 {
		t.Fatalf("lifts = %v", m)
	}
	sq := ls[0].(map[string]any)
	best := sq["best"].(map[string]any)
	if best["kg"] != 100.0 || best["reps"] != 5.0 || len([]rune(best["note"].(string))) != maxNoteChars+1 {
		t.Errorf("best = %v", best)
	}
	if first := sq["entries"].([]any)[0].(map[string]any); first["date"] != "2026-11-01" {
		t.Errorf("entries not newest first: %v", sq["entries"])
	}
	m = call(t, d, "lifts", `{"lift":"Snatch"}`)
	if !strings.Contains(m["note"].(string), "Back Squat, Deadlift") {
		t.Errorf("unknown lift = %v", m)
	}
}
