package stats

// Alcohol, fibre, minerals and vitamins: what the foods eaten bring, from
// the nutrients Yazio lists per product and recipe (store.Food), and the
// reference values of the German, Austrian and Swiss nutrition societies
// (DGE/ÖGE/SGE, "Referenzwerte für die Nährstoffzufuhr", read on dge.de on
// 2026-11-18) to compare with.
//
// Not every food lists every nutrient, so each day's amount comes with its
// coverage: the share of the day's kcal from entries whose food lists it.
// An entry without a known food (a simple or AI entry, or a product synced
// before entries kept their product) counts as not listing it.

import (
	"math"
	"time"

	"eumetria/internal/store"
)

// A tracked nutrient: its key on the page, Yazio's key, and the unit shown
// with the factor from Yazio's grams.
type nutrient struct {
	Key, Yazio, Unit string
	Factor           float64
}

var tracked = []nutrient{
	{"alcohol", "nutrient.alcohol", "g", 1},
	{"fiber", "nutrient.dietaryfiber", "g", 1},
	{"sodium", "nutrient.sodium", "mg", 1e3}, // from the salt when only that is listed
	{"potassium", "mineral.potassium", "mg", 1e3},
	{"magnesium", "mineral.magnesium", "mg", 1e3},
	{"calcium", "mineral.calcium", "mg", 1e3},
	{"iron", "mineral.iron", "mg", 1e3},
	{"zinc", "mineral.zinc", "mg", 1e3},
	{"iodine", "mineral.iodine", "µg", 1e6},
	{"vitamin_a", "vitamin.a", "µg", 1e6},
	{"folate", "vitamin.b11", "µg", 1e6},
	{"vitamin_b12", "vitamin.b12", "µg", 1e6},
	{"vitamin_c", "vitamin.c", "mg", 1e3},
	{"vitamin_d", "vitamin.d", "µg", 1e6},
	{"vitamin_e", "vitamin.e", "mg", 1e3},
}

// NutrientUnit is the unit a tracked nutrient's amounts are in ("" for an
// unknown key).
func NutrientUnit(key string) string {
	for _, n := range tracked {
		if n.Key == key {
			return n.Unit
		}
	}
	return ""
}

// Reference is a daily reference value: Kind is "recommended" (Empfehlung),
// "estimated" (Schätzwert) or "guide" (Richtwert, meant as at least).
type Reference struct {
	Amount float64 `json:"amount"`
	Kind   string  `json:"kind"`
}

// References are the adult values for a person of sex born on birthDate
// ("YYYY-MM-DD"); nil without a sex (the page then shows amounts only).
// Without a date of birth the values for 25 to 51 apply. Alcohol has none: the DGE names no safe amount.
// Zinc assumes a medium phytate intake; iron for women follows the page's
// split at 51 (before and after menopause).
func References(sex, birthDate string, now time.Time) map[string]Reference {
	if sex != "male" && sex != "female" {
		return nil
	}
	age := 30
	if born, err := time.Parse(time.DateOnly, birthDate); err == nil {
		age = now.Year() - born.Year()
		if now.Month() < born.Month() || now.Month() == born.Month() && now.Day() < born.Day() {
			age-- // the birthday is still ahead this year
		}
	}
	male := sex == "male"
	pick := func(m, f float64) float64 {
		if male {
			return m
		}
		return f
	}
	iron := pick(11, 16)
	if !male && age >= 51 {
		iron = 14
	}
	vitaminA := pick(850, 700)
	if male && age >= 65 {
		vitaminA = 800
	}
	return map[string]Reference{
		"fiber":       {30, "guide"},
		"sodium":      {1500, "estimated"},
		"potassium":   {4000, "estimated"},
		"magnesium":   {pick(350, 300), "estimated"},
		"calcium":     {1000, "recommended"},
		"iron":        {iron, "recommended"},
		"zinc":        {pick(14, 8), "recommended"},
		"iodine":      {150, "recommended"},
		"vitamin_a":   {vitaminA, "recommended"},
		"folate":      {300, "recommended"},
		"vitamin_b12": {4, "estimated"},
		"vitamin_c":   {pick(110, 95), "recommended"},
		"vitamin_d":   {20, "estimated"},
		"vitamin_e":   {8, "estimated"},
	}
}

// dayNutrients sums the tracked nutrients of a day's entries: per key the
// amount (in its unit) and the coverage (0 to 1); keys no entry lists are
// left out.
func dayNutrients(h *store.History, items []store.Item) map[string][2]float64 {
	var kcal float64
	amount := make([]float64, len(tracked))
	covered := make([]float64, len(tracked))
	for _, it := range items {
		kcal += it.Energy
		f := h.FoodOf(it)
		if f == nil {
			continue
		}
		scale := store.Scale(it, f)
		for i, n := range tracked {
			if v, ok := f.Nutrient(n.Yazio); ok {
				amount[i] += v * scale * n.Factor
				covered[i] += it.Energy
			}
		}
	}
	out := map[string][2]float64{}
	for i, n := range tracked {
		if covered[i] == 0 {
			continue
		}
		cov := 1.0
		if kcal > 0 {
			cov = math.Min(1, covered[i]/kcal)
		}
		out[n.Key] = [2]float64{round(amount[i], 1), round(cov, 2)}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// foodNutrients collects each food's tracked nutrients (per g, ml or
// portion, in the nutrients' units) once, for the entries to point to:
// the page multiplies them by an entry's scale (Macro sources).
type foodNutrients struct {
	list  []map[string]float64
	index map[*store.Food]int
}

// link answers the entry's food (1-based index, 0 for none) and scale.
func (fn *foodNutrients) link(h *store.History, it store.Item) (int, float64) {
	f := h.FoodOf(it)
	if f == nil {
		return 0, 0
	}
	i, ok := fn.index[f]
	if !ok {
		m := map[string]float64{}
		for _, n := range tracked {
			if v, ok := f.Nutrient(n.Yazio); ok {
				m[n.Key] = roundSig(v*n.Factor, 4)
			}
		}
		if len(m) == 0 {
			fn.index[f] = 0
			return 0, 0
		}
		fn.list = append(fn.list, m)
		i = len(fn.list)
		fn.index[f] = i
	}
	if i == 0 {
		return 0, 0
	}
	return i, roundSig(store.Scale(it, f), 4)
}

// roundSig rounds v to n significant digits: per gram, a vitamin is a tiny
// number, which fixed decimals would round away.
func roundSig(v float64, n int) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow(10, float64(n)-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*p) / p
}
