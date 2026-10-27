// Package stats turns the private history into the public stats.json the site reads.
package stats

import (
	"math"
	"sort"
	"strings"
	"time"

	"eumetria/internal/store"
)

type Options struct {
	Title         string
	Timezone      string
	PublishFoods  bool          // include per-day food names (meals) in the public file
	PublishPhotos bool          // include meal photos
	PublishHealth bool          // include Google Health data (sleep, heart rate, workouts)
	PublishRoutes bool          // include the GPS track of workouts
	PublishReview bool          // include the weekly reviews
	CreditUSD     float64       // Anthropic credit bought, for the low-credit warning; 0 = no warning
	PhotoPath     string        // URL prefix of published photo files, e.g. "photos/"
	SyncInterval  time.Duration // how often syncs run, shown in the footer; 0 when unknown
}

type Goals struct {
	Energy  float64 `json:"energy,omitempty"`
	Protein float64 `json:"protein,omitempty"`
	Carb    float64 `json:"carb,omitempty"`
	Fat     float64 `json:"fat,omitempty"`
	Steps   float64 `json:"steps,omitempty"`
	Water   float64 `json:"water,omitempty"`
	Weight  float64 `json:"weight,omitempty"`
}

type Food struct {
	Name    string  `json:"n"`
	Daytime string  `json:"m"`
	Energy  float64 `json:"e"`
	// Grams of protein, carbs and fat; fibre, sugar and saturated fat only when
	// known (not for AI entries), as with the recipe ingredients' keys.
	Protein    float64  `json:"pr,omitempty"`
	Carb       float64  `json:"c,omitempty"`
	Fat        float64  `json:"f,omitempty"`
	Fiber      *float64 `json:"fi,omitempty"`
	Sugar      *float64 `json:"su,omitempty"`
	Saturated  *float64 `json:"sf,omitempty"`
	Amount     float64  `json:"a,omitempty"`
	Unit       string   `json:"u,omitempty"`
	Serving    string   `json:"s,omitempty"`
	ServingQty float64  `json:"sq,omitempty"`
	AI         bool     `json:"ai,omitempty"`
	// Recipe is the key of the recipe version in Output.Recipes;
	// RecipeStale marks one changed since that day whose version eaten is
	// unknown (the current one is shown).
	Recipe      string `json:"r,omitempty"`
	RecipeStale bool   `json:"rs,omitempty"`
	// Barcode is the key of the food in Output.Products.
	Barcode string `json:"x,omitempty"`
}

type Photo struct {
	Daytime string `json:"m,omitempty"`
	Src     string `json:"src"`
}

type Activity struct {
	Name     string  `json:"name"`
	Energy   float64 `json:"energy"`
	Duration float64 `json:"dur,omitempty"`
	Distance float64 `json:"dist,omitempty"`
	// Workout is the ID of the day's Fitbit workout this activity was
	// imported from.
	Workout string `json:"w,omitempty"`
}

type Day struct {
	Date       string  `json:"date"`
	Energy     float64 `json:"energy"`
	Protein    float64 `json:"protein"`
	Carb       float64 `json:"carb"`
	Fat        float64 `json:"fat"`
	EnergyGoal float64 `json:"energy_goal,omitempty"` // includes activity energy when Yazio adds it
	EnergyBase float64 `json:"energy_base,omitempty"` // goal before activity energy
	// Macro goals as the API reports them: a fixed share of the base energy
	// goal. The Yazio app scales them up by EnergyGoal/EnergyBase on active days.
	ProteinGoal    float64            `json:"protein_goal,omitempty"`
	CarbGoal       float64            `json:"carb_goal,omitempty"`
	FatGoal        float64            `json:"fat_goal,omitempty"`
	Meals          map[string]float64 `json:"meals,omitempty"`
	Steps          float64            `json:"steps,omitempty"`
	Water          float64            `json:"water,omitempty"`
	ActivityEnergy float64            `json:"activity_energy,omitempty"`
	Weight         *float64           `json:"weight,omitempty"`
	Foods          []Food             `json:"foods,omitempty"`
	Activities     []Activity         `json:"activities,omitempty"`
	Photos         []Photo            `json:"photos,omitempty"`

	// Google Health (Fitbit). SkinTemp is the night's deviation from the
	// personal baseline in °C.
	Sleep       *Sleep    `json:"sleep,omitempty"`
	RestingHR   float64   `json:"rhr,omitempty"`
	HRV         float64   `json:"hrv,omitempty"`
	HRVDeep     float64   `json:"hrv_deep,omitempty"`
	HRVEntropy  float64   `json:"hrv_ent,omitempty"`
	SleepHR     float64   `json:"sleep_hr,omitempty"`
	Respiratory float64   `json:"resp,omitempty"`
	SpO2        float64   `json:"spo2,omitempty"`
	SkinTemp    *float64  `json:"temp,omitempty"`
	Workouts    []Workout `json:"workouts,omitempty"`
	// FitbitKcal is Fitbit's estimate of all calories burned, resting
	// metabolism included; ZoneMinutes are the active zone minutes in the
	// fat burn, cardio and peak zones.
	FitbitKcal  float64     `json:"kcal_burned,omitempty"`
	ZoneMinutes *[3]float64 `json:"azm,omitempty"`
}

// GoalChange is a day a goal differs from the last day before it that had
// goals. Goal is "energy" (the base goal, kcal), "weight" (kg), "steps" or
// "water" (ml); From is 0 when the goal was not set before.
type GoalChange struct {
	Date string  `json:"date"`
	Goal string  `json:"goal"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

type Output struct {
	Title       string    `json:"title"`
	GeneratedAt time.Time `json:"generated_at"`
	Timezone    string    `json:"timezone"`
	// SyncIntervalMinutes is how often the data is synced (SYNC_INTERVAL).
	SyncIntervalMinutes int   `json:"sync_interval_minutes,omitempty"`
	Goals               Goals `json:"goals"`
	Days                []Day `json:"days"`
	// Device is the Fitbit tracker with its battery and last sync.
	Device *Device `json:"device,omitempty"`
	// HealthSync tells the page when the Fitbit sync is failing.
	HealthSync *HealthSync `json:"health_sync,omitempty"`
	// Recipes are the recipe versions behind food entries, by version hash
	// (or recipe ID for entries synced before versions were kept).
	Recipes map[string]Recipe `json:"recipes,omitempty"`
	// Products are the Open Food Facts data of foods, by barcode.
	Products map[string]Product `json:"products,omitempty"`
	// Reviews are the weekly reviews, newest first; ReviewStatus is set
	// when the Anthropic credit needs attention.
	Reviews      []Review      `json:"reviews,omitempty"`
	ReviewStatus *ReviewStatus `json:"review_status,omitempty"`
	// GoalChanges are the days a Yazio goal changed, oldest first.
	GoalChanges []GoalChange `json:"goal_changes,omitempty"`
	// Archive lists the files holding the older days, one per year, which
	// the sync moves out of Days (see publishArchive in cmd/eumetria).
	Archive []string `json:"archive,omitempty"`
}

func Build(h *store.History, opt Options, now time.Time) Output {
	out := Output{Title: opt.Title, GeneratedAt: now.UTC(), Timezone: opt.Timezone, Days: []Day{}}
	out.SyncIntervalMinutes = int(opt.SyncInterval.Round(time.Minute) / time.Minute)
	if opt.PublishHealth {
		// The phone ("MobileTrack") is a TRACKER too, so take the one that
		// synced last rather than the first in the list.
		for _, dev := range h.Devices {
			if dev.Type == "TRACKER" && (out.Device == nil || dev.LastSync.After(out.Device.LastSync)) {
				out.Device = &Device{Name: dev.Name, Battery: dev.Battery, BatteryStatus: dev.BatteryStatus, LastSync: dev.LastSync}
			}
		}
		out.HealthSync = healthSync(h.HealthSync)
	}

	var lastGoals map[string]float64 // the previous day's goals, rounded
	prods := newProducts(h)
	recs := newRecipes(h, opt, prods)
	seen := map[string]bool{}
	for d := range h.Days {
		seen[d] = true
	}
	if opt.PublishHealth {
		for d := range h.Health {
			seen[d] = true
		}
	}
	// The page starts with the first day logged in Yazio: Fitbit history
	// often reaches back much further and would leave the charts empty.
	first := ""
	for d, day := range h.Days {
		if yazioData(day) && (first == "" || d < first) {
			first = d
		}
	}
	dates := make([]string, 0, len(seen))
	for d := range seen {
		if d >= first {
			dates = append(dates, d)
		}
	}
	sort.Strings(dates)

	for _, date := range dates {
		src := h.Days[date]
		if src == nil {
			src = &store.Day{Date: date}
		}
		health := h.Health[date]
		if !opt.PublishHealth {
			health = nil
		}
		if !yazioData(src) && health == nil {
			continue
		}
		d := Day{
			Date:        date,
			Energy:      round(src.Total.Energy, 0),
			Protein:     round(src.Total.Protein, 1),
			Carb:        round(src.Total.Carb, 1),
			Fat:         round(src.Total.Fat, 1),
			EnergyGoal:  round(src.Goals["energy.energy"], 0),
			EnergyBase:  round(baseGoal(src), 0),
			ProteinGoal: round(src.Goals["nutrient.protein"], 1),
			CarbGoal:    round(src.Goals["nutrient.carb"], 1),
			FatGoal:     round(src.Goals["nutrient.fat"], 1),
			Steps:       round(src.Steps, 0),
			Water:       round(src.Water, 0),
		}
		if src.Weight != nil {
			w := round(*src.Weight, 1)
			d.Weight = &w
		}
		if len(src.Meals) > 0 {
			d.Meals = map[string]float64{}
			for k, m := range src.Meals {
				d.Meals[k] = round(m.Energy, 0)
			}
		}
		d.ActivityEnergy = round(src.ActivityEnergy, 0)
		if opt.PublishFoods {
			for _, it := range src.Items {
				d.Foods = append(d.Foods, Food{
					Name: it.Name, Daytime: it.Daytime, Energy: round(it.Energy, 0),
					Protein: round(it.Protein, 1), Carb: round(it.Carb, 1), Fat: round(it.Fat, 1),
					Fiber: roundPtr(it.Fiber, 1), Sugar: roundPtr(it.Sugar, 1), Saturated: roundPtr(it.Saturated, 1),
					Amount: round(it.Amount, 1), Unit: it.Unit,
					Serving: servingLabel(it.Serving), ServingQty: round(it.ServingQty, 2), AI: it.AI,
					Recipe: recs.link(it), RecipeStale: it.RecipeStale,
					Barcode: prods.link(it.EAN, it.Name),
				})
			}
		}
		if opt.PublishPhotos {
			for _, p := range src.Photos {
				switch {
				case strings.HasPrefix(p.URL, "data:"): // demo data
					d.Photos = append(d.Photos, Photo{Daytime: p.Daytime, Src: p.URL})
				case p.File != "":
					d.Photos = append(d.Photos, Photo{Daytime: p.Daytime, Src: opt.PhotoPath + p.File})
				}
			}
		}
		if health != nil {
			addHealth(&d, health, opt)
		}
		for _, a := range src.Activities {
			act := Activity{
				Name: a.Name, Energy: round(a.Energy, 0),
				Duration: round(a.Duration, 0), Distance: round(a.Distance, 2),
			}
			for _, w := range d.Workouts {
				if a.ExternalID != "" && w.ID == a.ExternalID {
					act.Workout = w.ID
				}
			}
			d.Activities = append(d.Activities, act)
		}
		if len(src.Goals) > 0 {
			goals := map[string]float64{
				"energy": round(baseGoal(src), 0),
				"weight": round(src.Goals["bodyvalue.weight"], 1),
				"steps":  round(src.Goals["activity.step"], 0),
				"water":  round(src.Goals["water"], 0),
			}
			for _, k := range []string{"energy", "weight", "steps", "water"} {
				switch {
				case lastGoals == nil:
				case goals[k] == 0:
					// Missing that day (or a base goal clamped to 0): keep
					// the last known goal rather than "changing" from 0 later.
					goals[k] = lastGoals[k]
				case goals[k] != lastGoals[k]:
					out.GoalChanges = append(out.GoalChanges, GoalChange{Date: date, Goal: k, From: lastGoals[k], To: goals[k]})
				}
			}
			lastGoals = goals
			out.Goals = Goals{
				Energy:  round(baseGoal(src), 0),
				Protein: round(src.Goals["nutrient.protein"], 0),
				Carb:    round(src.Goals["nutrient.carb"], 0),
				Fat:     round(src.Goals["nutrient.fat"], 0),
				Steps:   round(src.Goals["activity.step"], 0),
				Water:   round(src.Goals["water"], 0),
				Weight:  round(src.Goals["bodyvalue.weight"], 1),
			}
		}
		out.Days = append(out.Days, d)
	}
	if len(recs.out) > 0 {
		out.Recipes = recs.out
	}
	if len(prods.out) > 0 {
		out.Products = prods.out
	}
	if opt.PublishReview {
		out.Reviews = reviews(h)
	}
	out.ReviewStatus = reviewStatus(h, opt.CreditUSD)
	return out
}

func yazioData(d *store.Day) bool {
	return d.Total.Energy != 0 || d.Weight != nil || d.Steps != 0 || d.Water != 0
}

// baseGoal is the day's energy goal without the activity bonus. When Yazio is
// set to add burned calories to the goal, its goal grows with every workout
// and step; days fetched before the flag was recorded are assumed to do so.
func baseGoal(d *store.Day) float64 {
	g := d.Goals["energy.energy"]
	if d.ConsumeActivityEnergy == nil || *d.ConsumeActivityEnergy {
		g -= d.ActivityEnergy
	}
	return math.Max(0, g)
}

// servingLabel returns the serving an item was entered in, unless it is just
// the base unit (then the amount alone says everything).
func servingLabel(serving string) string {
	switch serving {
	case "", "gram", "milliliter", "g", "ml":
		return ""
	}
	return serving
}

func roundPtr(v *float64, places int) *float64 {
	if v == nil {
		return nil
	}
	r := round(*v, places)
	return &r
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
