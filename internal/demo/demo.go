// Package demo generates plausible sample history for previewing the site
// without Yazio credentials.
package demo

import (
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"eumetria/internal/store"
)

var activities = []struct {
	name     string
	energy   float64
	duration float64
	distance float64
}{
	{"Running", 350, 30, 5200},
	{"Cycling", 280, 40, 15000},
	{"Walking", 150, 45, 3500},
	{"Swimming", 400, 30, 0},
	{"Strength training", 220, 45, 0},
	{"Yoga", 120, 60, 0},
	{"HIIT", 310, 20, 0},
}

// kcalPer is energy per unit (g, ml or portion); serving is how it's entered.
type food struct {
	name    string
	kcalPer float64
	unit    string
	typical float64
	serving string
}

var foods = map[string][]food{
	"breakfast": {{"Oatmeal", 3.7, "g", 60, ""}, {"Greek yogurt", 1.0, "g", 150, ""}, {"Banana", 0.9, "g", 120, "piece"}, {"Scrambled eggs", 1.5, "g", 140, ""}, {"Wholegrain bread", 2.3, "g", 80, "slice"}, {"Coffee with milk", 0.25, "ml", 200, "cup"}},
	"lunch":     {{"Chicken breast", 1.1, "g", 200, ""}, {"Basmati rice", 1.3, "g", 200, ""}, {"Mixed salad", 0.3, "g", 250, "bowl"}, {"Pasta bolognese", 620, "portion", 1, ""}, {"Lentil soup", 0.7, "ml", 450, ""}, {"Tuna wrap", 2.2, "g", 200, "piece"}},
	"dinner":    {{"Salmon fillet", 2.0, "g", 180, ""}, {"Sweet potato", 0.9, "g", 200, ""}, {"Broccoli", 0.35, "g", 150, ""}, {"Chili con carne", 560, "portion", 1, ""}, {"Tofu stir-fry", 1.4, "g", 300, ""}, {"Pizza Margherita", 2.6, "g", 300, "slice"}},
	"snack":     {{"Apple", 0.52, "g", 150, "piece"}, {"Almonds", 5.8, "g", 30, ""}, {"Protein bar", 3.6, "g", 55, "bar"}, {"Dark chocolate", 5.4, "g", 30, ""}, {"Skyr", 0.63, "g", 200, ""}},
}

// aiMeals are logged by "photo" in the demo, like Yazio's AI food recognition.
// mix is a food's share of energy from protein, carbs and fat, and its
// fibre in g per 100 kcal (-1: not known, like AI entries in Yazio).
type mix struct{ p, c, f, fiber float64 }

var mixes = map[string]mix{
	"Oatmeal": {0.14, 0.68, 0.18, 2.7}, "Greek yogurt": {0.38, 0.17, 0.45, 0}, "Banana": {0.04, 0.93, 0.03, 2.9},
	"Scrambled eggs": {0.33, 0.03, 0.64, 0}, "Wholegrain bread": {0.15, 0.73, 0.12, 3.0}, "Coffee with milk": {0.27, 0.40, 0.33, 0},
	"Chicken breast": {0.82, 0, 0.18, 0}, "Basmati rice": {0.08, 0.90, 0.02, 0.3}, "Mixed salad": {0.20, 0.55, 0.25, 6.5},
	"Pasta bolognese": {0.20, 0.52, 0.28, 1.4}, "Lentil soup": {0.27, 0.58, 0.15, 5.5}, "Tuna wrap": {0.30, 0.42, 0.28, 1.6},
	"Salmon fillet": {0.42, 0, 0.58, 0}, "Sweet potato": {0.07, 0.91, 0.02, 3.3}, "Broccoli": {0.28, 0.62, 0.10, 7.4},
	"Chili con carne": {0.28, 0.38, 0.34, 3.2}, "Tofu stir-fry": {0.30, 0.30, 0.40, 2.4}, "Pizza Margherita": {0.16, 0.50, 0.34, 1.0},
	"Apple": {0.02, 0.95, 0.03, 4.6}, "Almonds": {0.14, 0.14, 0.72, 2.1}, "Protein bar": {0.40, 0.35, 0.25, 2.5},
	"Dark chocolate": {0.05, 0.38, 0.57, 1.9}, "Skyr": {0.70, 0.27, 0.03, 0},
	// AI entries: macros, no fibre.
	"Poke bowl with salmon": {0.25, 0.48, 0.27, -1}, "Caesar salad": {0.22, 0.20, 0.58, -1}, "Ramen": {0.18, 0.55, 0.27, -1}, "Shakshuka": {0.24, 0.20, 0.56, -1},
}

// sugar is a food's sugar in g per 100 kcal (only for foods with fibre data).
var sugar = map[string]float64{
	"Oatmeal": 0.3, "Greek yogurt": 4.0, "Banana": 13.7, "Wholegrain bread": 1.5, "Coffee with milk": 9.6, "Basmati rice": 0.1,
	"Mixed salad": 9.0, "Pasta bolognese": 2.5, "Lentil soup": 2.0, "Tuna wrap": 1.8, "Sweet potato": 4.8, "Broccoli": 4.9,
	"Chili con carne": 3.0, "Tofu stir-fry": 3.5, "Pizza Margherita": 1.4, "Apple": 19.8, "Almonds": 0.7, "Protein bar": 6.0,
	"Dark chocolate": 4.4, "Skyr": 6.0,
}

// withMacros fills the entry's grams of protein, carbs, fat and fibre from
// its energy, its sugar, and saturated fat as a third of the fat when fibre
// is known.
func withMacros(it store.Item) store.Item {
	m, ok := mixes[it.Name]
	if !ok {
		return it
	}
	e := it.Energy
	it.Protein, it.Carb, it.Fat = math.Round(e*m.p/4*10)/10, math.Round(e*m.c/4*10)/10, math.Round(e*m.f/9*10)/10
	if m.fiber >= 0 {
		fi, su, sf := math.Round(e*m.fiber)/100, math.Round(e*sugar[it.Name])/100, math.Round(it.Fat*10/3)/10
		it.Fiber, it.Sugar, it.Saturated = &fi, &su, &sf
	}
	return it
}

var aiMeals = []struct {
	name string
	kcal float64
}{{"Poke bowl with salmon", 640}, {"Caesar salad", 470}, {"Ramen", 590}, {"Shakshuka", 410}}

// plate is a placeholder meal photo, so the demo needs no image files.
func plate(r *rand.Rand) string {
	hue := r.IntN(360)
	s := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 160 120">`+
		`<rect width="160" height="120" fill="hsl(%d 35%% 82%%)"/>`+
		`<circle cx="80" cy="60" r="44" fill="#fafafa"/><circle cx="80" cy="60" r="32" fill="hsl(%d 60%% 62%%)"/>`+
		`<circle cx="70" cy="52" r="9" fill="hsl(%d 55%% 45%%)"/><circle cx="92" cy="66" r="7" fill="hsl(%d 65%% 72%%)"/></svg>`,
		hue, (hue+40)%360, (hue+90)%360, (hue+200)%360)
	return "data:image/svg+xml," + url.PathEscape(s)
}

func History(today time.Time, n int) *store.History {
	r := rand.New(rand.NewPCG(42, 7))
	h := &store.History{Days: map[string]*store.Day{}, Foods: map[string]*store.Food{}, Health: map[string]*store.HealthDay{}}
	// A separate stream for the Fitbit data keeps the Yazio demo data as it was.
	hr := rand.New(rand.NewPCG(42, 8))
	const baseEnergy = 2100
	consume := true
	goals := map[string]float64{
		"energy.energy": baseEnergy, "nutrient.protein": 150, "nutrient.carb": 210, "nutrient.fat": 70,
		"activity.step": 10000, "water": 2500, "bodyvalue.weight": 78,
	}
	split := map[string]float64{"breakfast": 0.25, "lunch": 0.33, "dinner": 0.32, "snack": 0.10}

	for i := n - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i)
		t := float64(n-1-i) / float64(n-1)
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday

		d := &store.Day{Date: day.Format(time.DateOnly), FetchedAt: time.Now().UTC(), Goals: goals, Meals: map[string]store.Macros{}}

		// Skip logging food on ~6% of days.
		if r.Float64() > 0.06 {
			target := 2050 + r.NormFloat64()*220
			if weekend {
				target += 250
			}
			for meal, share := range split {
				kcal := target * share * (0.8 + r.Float64()*0.4)
				m := store.Macros{Energy: kcal, Protein: kcal * 0.28 / 4, Carb: kcal * 0.42 / 4, Fat: kcal * 0.30 / 9}
				d.Meals[meal] = m
				d.Total.Add(m)
				left := kcal
				if meal != "breakfast" && r.Float64() < 0.08 {
					ai := aiMeals[r.IntN(len(aiMeals))]
					d.Items = append(d.Items, withMacros(store.Item{Name: ai.name, Daytime: meal, Energy: ai.kcal, AI: true}))
					d.Photos = append(d.Photos, store.Photo{Daytime: meal, URL: plate(r)})
					left -= ai.kcal
				}
				opts := foods[meal]
				for left > 60 {
					f := opts[r.IntN(len(opts))]
					amt := f.typical * (0.6 + r.Float64()*0.8)
					if f.unit != "portion" {
						amt = math.Round(amt/5) * 5
					} else {
						amt = math.Round(amt*2) / 2
					}
					e := math.Min(f.kcalPer*amt, left)
					amt = e / f.kcalPer
					it := store.Item{Name: f.name, Daytime: meal, Amount: amt, Unit: f.unit, Energy: e}
					if f.serving != "" {
						it.Serving, it.ServingQty = f.serving, math.Round(amt/f.typical*2)/2
					}
					d.Items = append(d.Items, withMacros(it))
					left -= e
				}
			}
		}

		d.Steps = math.Max(1500, 8500+r.NormFloat64()*2800)
		d.Water = math.Round(math.Max(750, 2100+r.NormFloat64()*450)/250) * 250

		d.ConsumeActivityEnergy = &consume
		if r.Float64() < 0.45 {
			acts := activities[r.IntN(len(activities))]
			e := acts.energy * (0.8 + r.Float64()*0.4)
			dur := acts.duration * (0.8 + r.Float64()*0.4)
			a := store.Activity{Name: acts.name, Energy: math.Round(e), Duration: math.Round(dur)}
			if acts.distance > 0 {
				a.Distance = math.Round(acts.distance*(0.8+r.Float64()*0.4)*100) / 100
			}
			h.Health[d.Date] = healthDay(hr, day, t, &a)
			d.Activities = append(d.Activities, a)
			d.ActivityEnergy = a.Energy
		} else {
			h.Health[d.Date] = healthDay(hr, day, t, nil)
		}
		// Steps burn energy too, and Yazio adds all of it to the day's goal.
		d.ActivityEnergy += math.Round(math.Max(0, d.Steps-3000) * 0.04)
		d.Goals = map[string]float64{}
		for k, v := range goals {
			d.Goals[k] = v
		}
		// Two goal changes, like lowering the goal after a first stretch;
		// the dashboard marks them on its charts.
		base := float64(baseEnergy)
		switch {
		case i >= 200:
			base, d.Goals["bodyvalue.weight"] = 2350, 80
		case i >= 45:
			base = 2200
		}
		d.Goals["energy.energy"] = base + d.ActivityEnergy

		// Weigh in ~65% of days; trend from 86 kg toward 79 kg with water-weight noise.
		if r.Float64() < 0.65 {
			w := 86 - 7*(1-math.Pow(1-t, 1.6)) + 0.4*math.Sin(t*30) + r.NormFloat64()*0.35
			w = math.Round(w*10) / 10
			d.Weight = &w
		}
		h.Days[d.Date] = d
	}
	addRecipes(h)
	addProducts(h)
	addReviews(h, today)
	return h
}

// Two weekly reviews in the shape Claude writes them (English and German),
// for the last two completed calendar weeks.
func addReviews(h *store.History, today time.Time) {
	monday := today.AddDate(0, 0, -((int(today.Weekday())+6)%7)-7)
	texts := [][2]string{{
		"A steady week: you logged every day, ate close to your goal and trained four times.\n\n**Nutrition**\n- You averaged **2.140 kcal** against a goal of 2.310 kcal (previous week: 2.090 vs 2.280).\n- Protein averaged 151 g, slightly above your 150 g goal.\n- 31 % of your calories came from unprocessed foods (NOVA 1), up from 26 %.\n\n**Training**\n- 4 workouts in 3 h 6 min, up from 3 workouts in 2 h 20 min.\n- Saturday's run was your longest this month: 8,4 km at 5:31 min/km.\n\n**Sleep & recovery**\n- You slept 7 h 12 min on average, up from 6 h 54 min.\n- Resting heart rate averaged 58 bpm (previous week: 59).\n\n**Worth a look**\n- Your two latest bedtimes (Friday and Saturday) came before the two lowest HRV readings of the week.",
		"Eine stetige Woche: Du hast jeden Tag erfasst, nah an deinem Ziel gegessen und viermal trainiert.\n\n**Ernährung**\n- Im Schnitt **2.140 kcal** bei einem Ziel von 2.310 kcal (Vorwoche: 2.090 zu 2.280).\n- Protein im Schnitt 151 g, knapp über deinem Ziel von 150 g.\n- 31 % deiner Kalorien kamen aus unverarbeiteten Lebensmitteln (NOVA 1), mehr als die 26 % der Vorwoche.\n\n**Training**\n- 4 Trainings in 3 h 6 min, nach 3 Trainings in 2 h 20 min.\n- Dein Lauf am Samstag war der längste des Monats: 8,4 km mit 5:31 min/km.\n\n**Schlaf & Erholung**\n- Du hast im Schnitt 7 h 12 min geschlafen, nach 6 h 54 min.\n- Ruhepuls im Schnitt 58 bpm (Vorwoche: 59).\n\n**Einen Blick wert**\n- Deine zwei spätesten Bettzeiten (Freitag und Samstag) lagen vor den zwei niedrigsten HRV-Werten der Woche.",
	}, {
		"A busier week with less sleep and one workout fewer than usual.\n\n**Nutrition**\n- You averaged **2.090 kcal** against a goal of 2.280 kcal on 6 logged days.\n- Dinner was your largest meal on 5 of 6 days.\n\n**Training**\n- 3 workouts in 2 h 20 min; steps averaged 8.420.\n\n**Sleep & recovery**\n- Average sleep was 6 h 54 min, the lowest in four weeks.\n- HRV averaged 41 ms (previous week: 44).\n\n**Worth a look**\n- Thursday had no food logged.",
		"Eine vollere Woche mit weniger Schlaf und einem Training weniger als sonst.\n\n**Ernährung**\n- Im Schnitt **2.090 kcal** bei einem Ziel von 2.280 kcal an 6 erfassten Tagen.\n- Das Abendessen war an 5 von 6 Tagen deine größte Mahlzeit.\n\n**Training**\n- 3 Trainings in 2 h 20 min; im Schnitt 8.420 Schritte.\n\n**Schlaf & Erholung**\n- Im Schnitt 6 h 54 min Schlaf, der niedrigste Wert seit vier Wochen.\n- HRV im Schnitt 41 ms (Vorwoche: 44).\n\n**Einen Blick wert**\n- Am Donnerstag war nichts erfasst.",
	}}
	h.Reviews = map[string]*store.Review{}
	for i, text := range texts {
		week := monday.AddDate(0, 0, -7*i).Format(time.DateOnly)
		h.Reviews[week] = &store.Review{Week: week, Text: text[0], TextDE: text[1], Model: "claude-opus-5-5", CreatedAt: time.Now().UTC()}
	}
}

// Open Food Facts data for the packaged demo foods (loose foods like an
// apple have no barcode). The barcodes are fake, so the page links none.
var products = map[string]store.OFFProduct{
	"Oatmeal":          {Name: "Rolled oats", NutriScore: "a", NOVA: 1},
	"Greek yogurt":     {Name: "Greek yogurt natural", NutriScore: "b", NOVA: 3},
	"Wholegrain bread": {Name: "Wholegrain bread", NutriScore: "b", NOVA: 3},
	"Chicken breast":   {Name: "Chicken breast fillet", NutriScore: "a", NOVA: 1},
	"Basmati rice":     {Name: "Basmati rice", NutriScore: "a", NOVA: 1},
	"Lentil soup":      {Name: "Lentil soup", NutriScore: "b", NOVA: 3},
	"Tuna wrap":        {Name: "Tuna wrap", NutriScore: "c", NOVA: 4, Additives: []string{"E330", "E415"}},
	"Salmon fillet":    {Name: "Salmon fillet", NutriScore: "b", NOVA: 1},
	"Tofu stir-fry":    {Name: "Tofu stir-fry", NutriScore: "b", NOVA: 3},
	"Pizza Margherita": {Name: "Pizza Margherita", NutriScore: "d", NOVA: 4, Additives: []string{"E300", "E331"}},
	"Almonds":          {Name: "Almonds", NutriScore: "a", NOVA: 1},
	"Protein bar":      {Name: "Protein bar chocolate", NutriScore: "d", NOVA: 4, Additives: []string{"E322", "E422", "E955"}},
	"Dark chocolate":   {Name: "Dark chocolate 70 %", NutriScore: "e", NOVA: 4, Additives: []string{"E322"}},
	"Skyr":             {Name: "Skyr natural", NutriScore: "a", NOVA: 3},
	"Spaghetti":        {Name: "Spaghetti", Brand: "Barilla", NutriScore: "a", NOVA: 1},
	"Tomato passata":   {Name: "Passata", Brand: "Mutti", NutriScore: "a", NOVA: 1},
	"Parmesan":         {Name: "Parmigiano Reggiano", NutriScore: "d", NOVA: 3},
	"Kidney beans":     {Name: "Kidney beans", Brand: "Bonduelle", NutriScore: "a", NOVA: 3},
	"Chopped tomatoes": {Name: "Chopped tomatoes", NutriScore: "a", NOVA: 3},
	"Sweetcorn":        {Name: "Sweetcorn", NutriScore: "b", NOVA: 3},
}

func addProducts(h *store.History) {
	h.OFF = map[string]*store.OFFProduct{}
	eans := map[string]string{}
	for name, p := range products {
		ean := "demo-" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		p.FetchedAt = time.Now().UTC()
		h.OFF[ean] = &p
		h.Foods["product:"+ean] = &store.Food{Version: store.FoodVersion, Name: name, EAN: ean}
		eans[name] = ean
	}
	for _, d := range h.Days {
		for i := range d.Items {
			d.Items[i].EAN = eans[d.Items[i].Name]
		}
	}
	for _, f := range h.Foods {
		for i := range f.Ingredients {
			f.Ingredients[i].EAN = eans[f.Ingredients[i].Name]
		}
	}
}

// The demo's portion foods are recipes with ingredients and a photo, like
// the user-made recipes in Yazio. Amounts are for the whole recipe.
var recipes = []struct {
	id, name string
	portions float64
	items    []store.Ingredient
}{
	{"demo-bolognese", "Pasta bolognese", 4, []store.Ingredient{
		{Name: "Spaghetti", Producer: "Barilla", Amount: 400, Unit: "g", Macros: store.Macros{Energy: 1432, Protein: 50, Carb: 284, Fat: 6}},
		{Name: "Ground beef", Amount: 500, Unit: "g", Macros: store.Macros{Energy: 1250, Protein: 100, Carb: 0, Fat: 95}},
		{Name: "Tomato passata", Producer: "Mutti", Amount: 700, Unit: "g", Macros: store.Macros{Energy: 245, Protein: 10, Carb: 42, Fat: 1}},
		{Name: "Onion", Amount: 150, Unit: "g", Serving: "piece", ServingQty: 1, Macros: store.Macros{Energy: 60, Protein: 2, Carb: 13, Fat: 0}},
		{Name: "Olive oil", Amount: 20, Unit: "ml", Macros: store.Macros{Energy: 165, Protein: 0, Carb: 0, Fat: 18}},
		{Name: "Parmesan", Amount: 40, Unit: "g", Macros: store.Macros{Energy: 160, Protein: 14, Carb: 0, Fat: 11}},
	}},
	{"demo-chili", "Chili con carne", 2, []store.Ingredient{
		{Name: "Ground beef", Amount: 250, Unit: "g", Macros: store.Macros{Energy: 625, Protein: 50, Carb: 0, Fat: 48}},
		{Name: "Kidney beans", Producer: "Bonduelle", Amount: 250, Unit: "g", Macros: store.Macros{Energy: 230, Protein: 16, Carb: 36, Fat: 1}},
		{Name: "Chopped tomatoes", Amount: 400, Unit: "g", Macros: store.Macros{Energy: 80, Protein: 4, Carb: 14, Fat: 1}},
		{Name: "Sweetcorn", Amount: 140, Unit: "g", Macros: store.Macros{Energy: 115, Protein: 4, Carb: 22, Fat: 2}},
		{Name: "Chili powder", Amount: 5, Unit: "g", Macros: store.Macros{Energy: 15, Protein: 1, Carb: 2, Fat: 1}},
	}},
}

func addRecipes(h *store.History) {
	r := rand.New(rand.NewPCG(42, 9))
	ids := map[string]string{}
	for _, rec := range recipes {
		// Scale the ingredients so a portion has the kcal the diary uses.
		var perPortion, sum float64
		for _, list := range foods {
			for _, f := range list {
				if f.name == rec.name {
					perPortion = f.kcalPer
				}
			}
		}
		for _, in := range rec.items {
			sum += in.Energy
		}
		items := make([]store.Ingredient, len(rec.items))
		for i, in := range rec.items {
			k := perPortion * rec.portions / sum
			in.Energy, in.Protein, in.Carb, in.Fat = in.Energy*k, in.Protein*k, in.Carb*k, in.Fat*k
			items[i] = in
		}
		h.Foods["recipe:"+rec.id] = &store.Food{
			Version: store.FoodVersion, Name: rec.name, Unit: "portion",
			Portions: rec.portions, Ingredients: items, Image: plate(r),
		}
		ids[rec.name] = rec.id
	}
	for _, d := range h.Days {
		for i := range d.Items {
			if id, ok := ids[d.Items[i].Name]; ok && d.Items[i].Unit == "portion" {
				d.Items[i].RecipeID = id
			}
		}
	}
}
