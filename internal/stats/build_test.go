package stats

import (
	"testing"
	"time"

	"eumetria/internal/store"
)

func ptr[T any](v T) *T { return &v }

func TestBaseGoal(t *testing.T) {
	goals := map[string]float64{"energy.energy": 2006.4}
	tests := []struct {
		name    string
		consume *bool
		want    float64
	}{
		{"activity included", ptr(true), 1705.6},
		{"activity not included", ptr(false), 2006.4},
		{"unknown (older history) assumes included", nil, 1705.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &store.Day{Goals: goals, ActivityEnergy: 300.8, ConsumeActivityEnergy: tt.consume}
			if got := round(baseGoal(d), 1); got != tt.want {
				t.Errorf("baseGoal = %v, want %v", got, tt.want)
			}
		})
	}
	if got := baseGoal(&store.Day{Goals: map[string]float64{"energy.energy": 100}, ActivityEnergy: 300}); got != 0 {
		t.Errorf("baseGoal must not go negative, got %v", got)
	}
}

func TestServingLabel(t *testing.T) {
	for serving, want := range map[string]string{"": "", "gram": "", "milliliter": "", "slice": "slice", "bottle": "bottle"} {
		if got := servingLabel(serving); got != want {
			t.Errorf("servingLabel(%q) = %q, want %q", serving, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	w := 77.2
	h := &store.History{Days: map[string]*store.Day{
		"2026-10-13": {
			Date: "2026-10-13", Total: store.Macros{Energy: 1862, Protein: 94.2},
			Goals: map[string]float64{
				"energy.energy": 2006.4195, "nutrient.protein": 83.2012, "nutrient.carb": 208.003,
				"nutrient.fat": 55.0201, "bodyvalue.weight": 70,
			},
			ActivityEnergy: 300.7945, ConsumeActivityEnergy: ptr(true), Weight: &w,
			Items: []store.Item{
				{Name: "Spezial", Daytime: "dinner", Amount: 500, Unit: "ml", Serving: "bottle", ServingQty: 1, Energy: 210},
				{Name: "Chimichurri Bowl", Daytime: "dinner", Energy: 645, AI: true},
			},
			Photos: []store.Photo{{Daytime: "dinner", URL: "https://cdn/x.webp", File: "abc.jpg"}, {Daytime: "lunch", URL: "https://cdn/y.webp"}},
		},
		// No food, weight, steps or water: left out of the public file.
		"2026-10-12": {Date: "2026-10-12"},
	}}

	out := Build(h, Options{Nutrition: true, Foods: true, Photos: true, PhotoPath: "photos/"}, time.Now())
	if len(out.Days) != 1 {
		t.Fatalf("got %d days, want 1 (empty days are skipped)", len(out.Days))
	}
	d := out.Days[0]
	if d.EnergyGoal != 2006 || d.EnergyBase != 1706 || out.Goals.Energy != 1706 {
		t.Errorf("energy goal %v, base %v, overall %v", d.EnergyGoal, d.EnergyBase, out.Goals.Energy)
	}
	if d.ProteinGoal != 83.2 || d.CarbGoal != 208 || d.FatGoal != 55 {
		t.Errorf("macro goals %v/%v/%v", d.ProteinGoal, d.CarbGoal, d.FatGoal)
	}
	if len(d.Foods) != 2 || d.Foods[0].Serving != "bottle" || d.Foods[0].Amount != 500 || !d.Foods[1].AI {
		t.Errorf("foods = %+v", d.Foods)
	}
	if len(d.Photos) != 1 || d.Photos[0].Src != "photos/abc.jpg" {
		t.Errorf("photos = %+v; only downloaded photos are published", d.Photos)
	}

	priv := Build(h, Options{Nutrition: true}, time.Now())
	if len(priv.Days[0].Foods) != 0 || len(priv.Days[0].Photos) != 0 {
		t.Error("foods and photos must be omitted without their scopes")
	}
}

func TestBuildDevicePicksLatestTracker(t *testing.T) {
	// Order and values as the API returned them on 2026-10-19, phone first.
	h := &store.History{Days: map[string]*store.Day{}}
	h.Devices = []store.Device{
		{Type: "TRACKER", Name: "MobileTrack", BatteryStatus: "Empty", LastSync: time.Date(2026, 10, 11, 13, 1, 37, 0, time.UTC)},
		{Type: "TRACKER", Name: "Fitbit Air", Battery: 56, BatteryStatus: "Medium", LastSync: time.Date(2026, 10, 19, 9, 46, 24, 0, time.UTC)},
	}
	out := Build(h, Options{Health: true}, time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC))
	if out.Device == nil || out.Device.Name != "Fitbit Air" {
		t.Fatalf("Device = %+v, want Fitbit Air", out.Device)
	}
}

func TestProductsFromOpenFoodFacts(t *testing.T) {
	h := &store.History{
		Days: map[string]*store.Day{"2026-10-20": {Date: "2026-10-20", Total: store.Macros{Energy: 900}, Items: []store.Item{
			{Name: "Skyr", Daytime: "breakfast", Energy: 66, EAN: "111"},
			{Name: "Milk", Daytime: "breakfast", Energy: 47}, // stored before barcodes: matched by name
			{Name: "Twin", Daytime: "lunch", Energy: 10},     // two products share the name: no match
			{Name: "Ghost", Daytime: "lunch", Energy: 10, EAN: "404"},
			{Name: "Bowl", Daytime: "dinner", Energy: 500, Unit: "portion", RecipeID: "r"},
		}}},
		Foods: map[string]*store.Food{
			"product:a": {Name: "Skyr", EAN: "111"}, "product:b": {Name: "Milk", EAN: "222"},
			"product:c": {Name: "Twin", EAN: "333"}, "product:d": {Name: "Twin", EAN: "334"},
			"recipe:r": {Name: "Bowl", Portions: 1, Ingredients: []store.Ingredient{{Name: "Milk", Amount: 100, Unit: "ml"}}},
		},
		OFF: map[string]*store.OFFProduct{
			"111": {Name: "Skyr Natur", NutriScore: "a", NOVA: 3},
			"222": {Name: "H-Milch", NutriScore: "b", NOVA: 1, Additives: []string{"E322"}},
			"333": {Name: "Twin A", NOVA: 4},
			"404": {NotFound: true},
		},
	}
	out := Build(h, Options{Foods: true}, time.Now())
	got := map[string]string{}
	for _, f := range out.Days[0].Foods {
		got[f.Name] = f.Barcode
	}
	if got["Skyr"] != "111" || got["Milk"] != "222" || got["Twin"] != "" || got["Ghost"] != "" {
		t.Errorf("barcodes = %v", got)
	}
	if p := out.Products["222"]; p.Name != "H-Milch" || p.NutriScore != "b" || p.NOVA != 1 || len(p.Additives) != 1 {
		t.Errorf("product 222 = %+v", p)
	}
	if _, ok := out.Products["333"]; ok || len(out.Products) != 2 {
		t.Errorf("published products %v; want only the linked, known ones", out.Products)
	}
	if in := out.Recipes["r"].Ingredients; len(in) != 1 || in[0].Barcode != "222" {
		t.Errorf("recipe ingredients = %+v", in)
	}
	if out := Build(h, Options{}, time.Now()); out.Products != nil {
		t.Error("products included without view.foods")
	}
}

func TestReviewsAndCreditStatus(t *testing.T) {
	h := &store.History{
		Days: map[string]*store.Day{"2026-10-20": {Date: "2026-10-20", Total: store.Macros{Energy: 1}}},
		Reviews: map[string]*store.Review{
			"2026-10-05": {Week: "2026-10-05", Text: "Older.", CostUSD: 0.10},
			"2026-10-12": {Week: "2026-10-12", Text: "Newer.", CostUSD: 0.10},
		},
	}
	out := Build(h, Options{Review: true, CreditStatus: true, CreditUSD: 10}, time.Now())
	if len(out.Reviews) != 2 || out.Reviews[0].Week != "2026-10-12" {
		t.Errorf("reviews = %+v", out.Reviews)
	}
	if out.ReviewStatus != nil {
		t.Errorf("plenty of credit, but status %+v", out.ReviewStatus)
	}
	// $0.50 left at $0.10 a week: 5 weeks, not low yet; $0.30: 3 weeks, low.
	out = Build(h, Options{CreditStatus: true, CreditUSD: 0.70}, time.Now())
	if out.Reviews != nil || out.ReviewStatus != nil {
		t.Errorf("reviews without view.review %v, status %+v", out.Reviews, out.ReviewStatus)
	}
	out = Build(h, Options{CreditStatus: true, CreditUSD: 0.50}, time.Now())
	if s := out.ReviewStatus; s == nil || !s.CreditLow || s.WeeksLeft != 3 || s.RemainingUSD != 0.30 {
		t.Errorf("low credit status = %+v", s)
	}
	// A refusal for money reasons always shows.
	h.ReviewState = &store.ReviewState{Billing: true}
	if s := Build(h, Options{CreditStatus: true}, time.Now()).ReviewStatus; s == nil || !s.Billing {
		t.Errorf("billing status = %+v", s)
	}
	// Only admins get it.
	if s := Build(h, Options{Review: true}, time.Now()).ReviewStatus; s != nil {
		t.Errorf("status without CreditStatus = %+v", s)
	}
}

func TestGoalChanges(t *testing.T) {
	day := func(date string, kcal, weight float64) *store.Day {
		return &store.Day{Date: date, Total: store.Macros{Energy: 1500}, ConsumeActivityEnergy: ptr(false),
			Goals: map[string]float64{"energy.energy": kcal, "bodyvalue.weight": weight, "activity.step": 10000}}
	}
	h := &store.History{Days: map[string]*store.Day{
		"2026-08-15": day("2026-08-15", 2000, 0),
		"2026-08-16": day("2026-08-16", 1791.2, 75),
		"2026-08-17": day("2026-08-17", 1791.4, 75),                           // the same goal after rounding
		"2026-08-18": {Date: "2026-08-18", Total: store.Macros{Energy: 1500}}, // no goals fetched
		"2026-09-16": day("2026-09-16", 1731, 70),
		// Goals without a weight goal that day: no change to 0 and back.
		"2026-09-17": {Date: "2026-09-17", Total: store.Macros{Energy: 1500}, ConsumeActivityEnergy: ptr(false),
			Goals: map[string]float64{"energy.energy": 1731, "activity.step": 10000}},
		"2026-09-18": day("2026-09-18", 1731, 70),
	}}
	got := Build(h, Options{Nutrition: true, Activity: true}, time.Now()).GoalChanges
	want := []GoalChange{
		{"2026-08-16", "energy", 2000, 1791}, {"2026-08-16", "weight", 0, 75},
		{"2026-09-16", "energy", 1791, 1731}, {"2026-09-16", "weight", 75, 70},
	}
	if len(got) != len(want) {
		t.Fatalf("GoalChanges = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GoalChanges[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Every part of a day comes only with its scope; a role that sees none of
// Yazio's data gets only the Fitbit days.
func TestBuildFollowsTheScopes(t *testing.T) {
	start := time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)
	h := &store.History{
		Days: map[string]*store.Day{"2026-10-20": {
			Date: "2026-10-20", Total: store.Macros{Energy: 1800, Protein: 90}, Water: 1500, Steps: 9000, ActivityEnergy: 300,
			Weight: ptr(75.0), Meals: map[string]store.Macros{"lunch": {Energy: 700}},
			Goals:      map[string]float64{"energy.energy": 2000, "activity.step": 10000, "bodyvalue.weight": 72},
			Items:      []store.Item{{Name: "Skyr", Daytime: "breakfast", Energy: 66}},
			Photos:     []store.Photo{{Daytime: "lunch", File: "a.jpg"}},
			Activities: []store.Activity{{Name: "hiit", Energy: 300, Start: "2026-10-20 19:00:00"}},
		}},
		Health: map[string]*store.HealthDay{
			"2026-10-20": {Date: "2026-10-20", RestingHR: 60, Workouts: []store.Workout{{ID: "w1", Start: start, End: start.Add(time.Hour), UTCOffset: 7200,
				Route: []store.RoutePoint{{48, 11, 0, 500, 120}}}}},
			"2026-10-21": {Date: "2026-10-21", RestingHR: 61},
		},
		Reviews: map[string]*store.Review{"2026-10-12": {Week: "2026-10-12", Text: "Fine."}},
	}

	all := Build(h, Options{}.Everything(), time.Now())
	d := all.Days[0]
	if d.Energy != 1800 || d.Steps != 9000 || d.Weight == nil || len(d.Foods) != 1 || len(d.Photos) != 1 || d.Activities[0].Workout != "w1" ||
		len(d.Workouts[0].Route) != 1 || len(all.Reviews) != 1 || all.Goals.Steps != 10000 {
		t.Fatalf("everything = %+v", d)
	}

	// A coach: food and nutrition, no activity, no Fitbit.
	d = Build(h, Options{Nutrition: true, Foods: true}, time.Now()).Days[0]
	if d.Energy != 1800 || d.Water != 1500 || len(d.Foods) != 1 || d.Steps != 0 || d.ActivityEnergy != 0 || d.Activities != nil ||
		d.Photos != nil || d.RestingHR != 0 || d.Workouts != nil {
		t.Errorf("nutrition and foods = %+v", d)
	}

	// Activity without the workouts: the link to the workout goes too.
	out := Build(h, Options{Activity: true}, time.Now())
	d = out.Days[0]
	if d.Steps != 9000 || len(d.Activities) != 1 || d.Activities[0].Workout != "" || d.Energy != 0 || d.Weight != nil || d.Meals != nil {
		t.Errorf("activity = %+v", d)
	}
	if out.Goals != (Goals{Steps: 10000}) {
		t.Errorf("activity goals = %+v", out.Goals)
	}

	// Fitbit only: both health days, no Yazio numbers, routes only when asked.
	out = Build(h, Options{Health: true}, time.Now())
	if len(out.Days) != 2 || out.Days[0].Energy != 0 || out.Days[0].Activities != nil || out.Days[0].Workouts[0].Route != nil || out.Reviews != nil {
		t.Errorf("health = %+v", out.Days)
	}
	// Routes without health give nothing.
	if out := Build(h, Options{Routes: true}, time.Now()); len(out.Days) != 0 {
		t.Errorf("routes alone = %+v", out.Days)
	}
}
