package demo

// Vitamins, minerals and alcohol for the sample foods, so the Nutrients tab
// and the macro sources have something to show: rough values per 100 kcal,
// in the units the page shows (mg, µg), turned into Yazio's grams per g, ml
// or portion. And now and then a beer with dinner.

import (
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"eumetria/internal/store"
)

// per100kcal: potassium, magnesium, calcium, iron, zinc (mg), vitamin C
// (mg), vitamin D, B12, folate, iodine (µg), sodium (mg).
type micro struct{ k, mg, ca, fe, zn, c, d, b12, fol, iod, na float64 }

var micros = map[string]micro{
	"Oatmeal":          {110, 37, 14, 1.2, 1.0, 0, 0, 0, 8, 1, 2},
	"Greek yogurt":     {150, 11, 110, 0.1, 0.5, 1, 0.1, 0.5, 7, 15, 45},
	"Banana":           {400, 30, 6, 0.3, 0.2, 10, 0, 0, 22, 2, 1},
	"Scrambled eggs":   {90, 8, 40, 1.2, 0.9, 0, 1.3, 0.6, 30, 6, 160},
	"Wholegrain bread": {110, 35, 15, 1.1, 0.8, 0, 0, 0, 12, 3, 190},
	"Coffee with milk": {300, 25, 120, 0.1, 0.4, 0.5, 0, 0.4, 8, 18, 50},
	"Chicken breast":   {230, 26, 8, 0.5, 0.7, 0, 0.1, 0.3, 4, 2, 50},
	"Basmati rice":     {30, 10, 3, 0.2, 0.4, 0, 0, 0, 3, 1, 1},
	"Mixed salad":      {700, 45, 120, 3.0, 0.8, 70, 0, 0, 180, 4, 30},
	"Lentil soup":      {350, 40, 25, 2.5, 1.2, 3, 0, 0, 90, 2, 280},
	"Tuna wrap":        {150, 15, 25, 0.8, 0.5, 2, 1.2, 1.0, 10, 8, 300},
	"Salmon fillet":    {180, 14, 6, 0.2, 0.3, 0, 5.5, 1.5, 12, 15, 25},
	"Sweet potato":     {390, 29, 35, 0.7, 0.4, 25, 0, 0, 13, 2, 60},
	"Broccoli":         {900, 60, 140, 2.1, 1.2, 250, 0, 0, 180, 4, 90},
	"Tofu stir-fry":    {180, 40, 150, 2.0, 1.0, 8, 0, 0, 20, 2, 240},
	"Pizza Margherita": {80, 9, 70, 0.5, 0.5, 1, 0.1, 0.2, 6, 4, 230},
	"Apple":            {210, 10, 10, 0.2, 0.1, 12, 0, 0, 5, 2, 2},
	"Almonds":          {120, 45, 45, 0.6, 0.5, 0, 0, 0, 8, 0.3, 0.2},
	"Protein bar":      {90, 30, 60, 1.2, 1.0, 0, 0.5, 0.3, 15, 3, 70},
	"Dark chocolate":   {120, 40, 13, 2.0, 0.6, 0, 0, 0, 2, 1, 4},
	"Skyr":             {220, 18, 230, 0.1, 0.6, 0, 0, 1.0, 20, 25, 70},
	"Pasta bolognese":  {170, 20, 25, 1.0, 1.2, 4, 0.1, 0.4, 12, 3, 160},
	"Chili con carne":  {300, 30, 30, 1.8, 1.4, 12, 0.1, 0.5, 30, 3, 220},
}

// nutrientsOf is a food's nutrients per unit (kcalPer kcal each) in Yazio's
// grams, from its micro values per 100 kcal.
func nutrientsOf(name string, kcalPer float64) map[string]float64 {
	n := map[string]float64{"energy.energy": kcalPer}
	m, ok := micros[name]
	if !ok {
		return n
	}
	per := kcalPer / 100 // per unit, as a share of 100 kcal
	mg, ug := per/1e3, per/1e6
	n["mineral.potassium"], n["mineral.magnesium"], n["mineral.calcium"] = m.k*mg, m.mg*mg, m.ca*mg
	n["mineral.iron"], n["mineral.zinc"], n["vitamin.c"] = m.fe*mg, m.zn*mg, m.c*mg
	n["vitamin.d"], n["vitamin.b12"], n["vitamin.b11"], n["mineral.iodine"] = m.d*ug, m.b12*ug, m.fol*ug, m.iod*ug
	n["nutrient.sodium"], n["nutrient.alcohol"] = m.na*mg, 0
	if f, ok := mixes[name]; ok && f.fiber >= 0 {
		n["nutrient.dietaryfiber"] = f.fiber * per
	}
	return n
}

// demoProductID is the product a sample food is logged as.
func demoProductID(name string) string {
	return "demo-" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}

const beerName = "Pils"

// addNutrients gives the sample foods their products and recipes their
// nutrients, and adds a beer with dinner on some Fridays and Saturdays.
func addNutrients(h *store.History, today time.Time) {
	kcalPer := map[string]float64{}
	for _, list := range foods {
		for _, f := range list {
			kcalPer[f.name] = f.kcalPer
		}
	}
	for _, f := range h.Foods {
		if f.Unit == "portion" && f.Portions > 0 {
			// A recipe's nutrients are per portion.
			var kcal float64
			for _, in := range f.Ingredients {
				kcal += in.Energy
			}
			f.Nutrients = nutrientsOf(f.Name, kcal/f.Portions)
		}
	}
	h.Foods["product:"+demoProductID(beerName)] = &store.Food{Version: store.FoodVersion, Name: beerName, Unit: "ml",
		Nutrients: map[string]float64{"energy.energy": 0.42, "nutrient.alcohol": 0.04, "nutrient.carb": 0.032, "nutrient.protein": 0.005,
			"nutrient.fat": 0, "mineral.potassium": 0.00038, "mineral.magnesium": 0.00006, "nutrient.sodium": 0.00004, "vitamin.b11": 6e-8}}

	r := rand.New(rand.NewPCG(5, 23)) // its own, so the rest of the sample data stays as it was
	// In date order: a map's order changes from run to run, and r would too.
	dates := make([]string, 0, len(h.Days))
	for date := range h.Days {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		d := h.Days[date]
		for i, it := range d.Items {
			if it.RecipeID != "" || it.AI || it.Unit == "portion" {
				continue
			}
			id := demoProductID(it.Name)
			key := "product:" + id
			f := h.Foods[key]
			if f == nil {
				f = &store.Food{Version: store.FoodVersion, Name: it.Name, EAN: it.EAN}
				h.Foods[key] = f
			}
			if f.Nutrients == nil {
				f.Unit = it.Unit
				f.Nutrients = nutrientsOf(it.Name, kcalPer[it.Name])
			}
			d.Items[i].ProductID = id
		}
		day, err := time.Parse(time.DateOnly, date)
		if err != nil || len(d.Items) == 0 {
			continue
		}
		weekend := day.Weekday() == time.Friday || day.Weekday() == time.Saturday
		// Always on the day before yesterday's evening out, else now and then.
		if !(weekend && r.Float64() < 0.55) && date != today.AddDate(0, 0, -1).Format(time.DateOnly) {
			continue
		}
		ml := 500.0
		if r.Float64() < 0.4 {
			ml = 1000
		}
		e := 0.42 * ml
		beer := store.Item{Name: beerName, Daytime: "dinner", Amount: ml, Unit: "ml", Serving: "bottle", ServingQty: ml / 500,
			Energy: e, Carb: math.Round(0.032*ml*10) / 10, Protein: math.Round(0.005*ml*10) / 10, ProductID: demoProductID(beerName)}
		d.Items = append(d.Items, beer)
		m := d.Meals["dinner"]
		m.Add(store.Macros{Energy: e, Carb: beer.Carb, Protein: beer.Protein})
		d.Meals["dinner"] = m
		d.Total.Add(store.Macros{Energy: e, Carb: beer.Carb, Protein: beer.Protein})
	}
}
