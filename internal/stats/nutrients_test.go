package stats

import (
	"math"
	"testing"
	"time"

	"eumetria/internal/store"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

// The day of 2026-10-31: two bottles of beer (1000 ml "Spezial", 0.052 g of
// alcohol per ml, as Yazio lists it), an apple, a recipe with wine in it
// whose version isn't known any more (its kcal scaled), and an AI entry.
func nutrientHistory() (*store.History, []store.Item) {
	h := &store.History{Foods: map[string]*store.Food{
		"product:beer":  {Name: "Spezial", Unit: "ml", Nutrients: map[string]float64{"energy.energy": 0.42, "nutrient.alcohol": 0.052, "nutrient.salt": 0.0001}},
		"product:apple": {Name: "Apfel", Unit: "g", Nutrients: map[string]float64{"energy.energy": 0.65, "vitamin.c": 0.00012, "mineral.potassium": 0.00119, "nutrient.alcohol": 0, "nutrient.sodium": 0.00001}},
		"recipe:stew":   {Name: "Stew", Unit: "portion", Nutrients: map[string]float64{"energy.energy": 500, "nutrient.alcohol": 4, "mineral.iron": 0.003}},
	}}
	items := []store.Item{
		{Name: "Spezial", Amount: 1000, Unit: "ml", Energy: 420, ProductID: "beer"},
		{Name: "Apfel", Amount: 130, Unit: "g", Energy: 84.5, ProductID: "apple"},
		// Eaten in a version no longer known: Yazio counted 250 kcal for it.
		{Name: "Stew", Amount: 1, Unit: "portion", Energy: 250, RecipeID: "stew", RecipeVersion: "gone", RecipeStale: true},
		{Name: "Pasta", Energy: 450, AI: true},
	}
	return h, items
}

func TestDayNutrients(t *testing.T) {
	h, items := nutrientHistory()
	got := dayNutrients(h, items)
	// 52 g from the beer, 2 g from half a portion of stew (scaled by kcal).
	if a := got["alcohol"]; !near(a[0], 54) {
		t.Errorf("alcohol = %v, want 54 g", a)
	}
	// Known for the beer, the apple and the stew: all but the AI entry.
	if a := got["alcohol"]; !near(a[1], round((420+84.5+250)/(420+84.5+250+450), 2)) {
		t.Errorf("alcohol coverage = %v", a[1])
	}
	// Sodium: the apple lists it, the beer only its salt.
	if s := got["sodium"]; !near(s[0], 1000*0.0001*store.SodiumPerSalt*1000+130*0.00001*1000) {
		t.Errorf("sodium = %v mg", s)
	}
	if c := got["vitamin_c"]; !near(c[0], 15.6) || !near(c[1], round(84.5/1204.5, 2)) {
		t.Errorf("vitamin C = %v, want 15.6 mg on 7%% of the kcal", c)
	}
	if fe := got["iron"]; !near(fe[0], 1.5) {
		t.Errorf("iron = %v, want 1.5 mg from half a portion", fe)
	}
	if _, ok := got["vitamin_d"]; ok {
		t.Error("vitamin D without any food listing it")
	}
	// Nothing known: no map.
	if dayNutrients(h, []store.Item{{Name: "x", Energy: 100, AI: true}}) != nil {
		t.Error("a map for a day with no known foods")
	}
}

func TestFoodNutrientsLink(t *testing.T) {
	h, items := nutrientHistory()
	fn := &foodNutrients{index: map[*store.Food]int{}}
	k, scale := fn.link(h, items[0])
	if k != 1 || scale != 1000 || fn.list[0]["alcohol"] != 0.052 {
		t.Fatalf("beer = %d × %v, %v", k, scale, fn.list)
	}
	if k2, _ := fn.link(h, items[0]); k2 != 1 {
		t.Error("the same food listed twice")
	}
	if k, _ := fn.link(h, items[3]); k != 0 {
		t.Error("an AI entry got a food")
	}
	// Vitamins per gram keep their digits.
	_, _ = fn.link(h, items[1])
	if fn.list[1]["vitamin_c"] != 0.12 {
		t.Errorf("apple vitamin C = %v mg/g", fn.list[1]["vitamin_c"])
	}
}

func TestReferences(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if References("", "1987-04-02", now) != nil {
		t.Error("references without a sex")
	}
	m := References("male", "1987-04-02", now)
	if m["magnesium"].Amount != 350 || m["iron"].Amount != 11 || m["zinc"].Amount != 14 || m["vitamin_c"].Amount != 110 || m["fiber"].Kind != "guide" {
		t.Errorf("man of 39 = %v", m)
	}
	if _, ok := m["alcohol"]; ok {
		t.Error("a reference amount for alcohol")
	}
	// 65 on his birthday, not the day before.
	if References("male", "1961-11-18", now)["vitamin_a"].Amount != 800 {
		t.Error("vitamin A on the 65th birthday")
	}
	if References("male", "1961-11-19", now)["vitamin_a"].Amount != 850 {
		t.Error("vitamin A the day before the 65th birthday")
	}
	w := References("female", "1990-01-01", now)
	if w["iron"].Amount != 16 || w["magnesium"].Amount != 300 || w["vitamin_c"].Amount != 95 {
		t.Errorf("woman of 36 = %v", w)
	}
	if References("female", "1975-11-18", now)["iron"].Amount != 14 {
		t.Error("iron for a woman of 51")
	}
	if References("female", "", now)["iron"].Amount != 16 {
		t.Error("without a date of birth the values for 25 to 51")
	}
}
