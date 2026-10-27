// Package review writes a weekly review of the dashboard's data with Claude:
// one calendar week (Monday to Sunday) of nutrition, training and sleep,
// compared with the week before.
package review

import (
	"fmt"
	"math"
	"time"

	"eumetria/internal/stats"
)

// Monday returns the Monday of the calendar week that contains day.
func Monday(day time.Time) time.Time {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// Week is what the model gets: the week's days and a summary of the week
// before. Totals and shares are computed here; the model only reads them.
type Week struct {
	From     string `json:"from"` // Monday
	To       string `json:"to"`   // Sunday
	Running  bool   `json:"week_still_running,omitempty"`
	Today    string `json:"today_partial,omitempty"` // today's totals are still being logged
	Goals    Goals  `json:"goals"`
	Days     []Day  `json:"days"`
	Summary  Totals `json:"this_week"`
	Previous Totals `json:"previous_week"`
}

type Goals struct {
	KcalBase float64 `json:"kcal_base_goal,omitempty"`
	WeightKg float64 `json:"weight_goal_kg,omitempty"`
	Steps    float64 `json:"steps_goal,omitempty"`
}

type Day struct {
	Date       string             `json:"date"`
	Weekday    string             `json:"weekday"`
	Logged     bool               `json:"food_logged"`
	Kcal       float64            `json:"kcal_eaten,omitempty"`
	KcalGoal   float64            `json:"kcal_goal_incl_activity,omitempty"`
	ProteinG   float64            `json:"protein_g,omitempty"`
	ProteinGl  float64            `json:"protein_goal_g,omitempty"`
	CarbsG     float64            `json:"carbs_g,omitempty"`
	FatG       float64            `json:"fat_g,omitempty"`
	Meals      map[string]float64 `json:"meals_kcal,omitempty"`
	Foods      []Food             `json:"foods,omitempty"`
	Steps      float64            `json:"steps,omitempty"`
	WeightKg   *float64           `json:"weight_kg,omitempty"`
	BurnedKcal float64            `json:"kcal_burned_fitbit,omitempty"`
	Activities []Activity         `json:"activities,omitempty"`
	Sleep      *Sleep             `json:"sleep,omitempty"` // the night that ended this morning
	RestingHR  float64            `json:"resting_hr,omitempty"`
	HRV        float64            `json:"hrv_ms,omitempty"`
	SleepHR    float64            `json:"sleeping_hr,omitempty"`
	ZoneMin    float64            `json:"active_zone_minutes,omitempty"`
}

type Food struct {
	Meal   string  `json:"meal"`
	Name   string  `json:"name"`
	Amount string  `json:"amount,omitempty"`
	Kcal   float64 `json:"kcal"`
	// Open Food Facts labels, when the food has a barcode and is rated.
	NutriScore string   `json:"nutri_score,omitempty"`
	NOVA       int      `json:"nova,omitempty"`
	Recipe     []string `json:"recipe_ingredients,omitempty"`
}

type Activity struct {
	Name     string  `json:"name"`
	Start    string  `json:"start,omitempty"`
	Duration string  `json:"duration,omitempty"`
	Kcal     float64 `json:"kcal,omitempty"`
	Km       float64 `json:"km,omitempty"`
	AvgHR    float64 `json:"avg_hr,omitempty"`
	MaxHR    float64 `json:"max_hr,omitempty"`
	ZoneMin  float64 `json:"zone_minutes,omitempty"`
}

type Sleep struct {
	Night  string `json:"night"` // e.g. "Thursday to Friday"
	Bed    string `json:"bedtime"`
	Wake   string `json:"wake"`
	Asleep string `json:"asleep"`
	Deep   string `json:"deep,omitempty"`
	REM    string `json:"rem,omitempty"`
	Awake  string `json:"awake,omitempty"`
}

// Totals are a week's averages over the days that have the value.
type Totals struct {
	DaysLogged   int     `json:"days_food_logged"`
	AvgKcal      float64 `json:"avg_kcal_eaten,omitempty"`
	AvgKcalGoal  float64 `json:"avg_kcal_goal,omitempty"`
	AvgProteinG  float64 `json:"avg_protein_g,omitempty"`
	AvgSteps     float64 `json:"avg_steps,omitempty"`
	Workouts     int     `json:"workouts"`
	WorkoutTime  string  `json:"workout_time,omitempty"`
	AvgSleep     string  `json:"avg_sleep,omitempty"`
	AvgRestingHR float64 `json:"avg_resting_hr,omitempty"`
	AvgHRV       float64 `json:"avg_hrv_ms,omitempty"`
	// Single weigh-ins swing with water and food; the week's average is
	// what to compare.
	AvgWeightKg float64  `json:"avg_weight_kg,omitempty"`
	WeighIns    int      `json:"weigh_ins,omitempty"`
	WeightFirst *float64 `json:"weight_first_kg,omitempty"`
	WeightLast  *float64 `json:"weight_last_kg,omitempty"`
	// Share of the logged calories from foods with Open Food Facts labels;
	// below about half, the shares below say little.
	LabelledShare float64 `json:"share_of_kcal_with_labels,omitempty"`
	// Share of the logged calories per Open Food Facts label; "unknown"
	// covers foods without a barcode or without a rating.
	NOVAShare  map[string]float64 `json:"kcal_share_by_nova,omitempty"`
	NutriShare map[string]float64 `json:"kcal_share_by_nutri_score,omitempty"`
}

// BuildWeek collects the calendar week starting at monday from out. today
// (YYYY-MM-DD) marks a week that is still running: later days are left out.
func BuildWeek(out stats.Output, monday time.Time, today string) Week {
	byDate := map[string]stats.Day{}
	for _, d := range out.Days {
		byDate[d.Date] = d
	}
	w := Week{
		From:  monday.Format(time.DateOnly),
		To:    monday.AddDate(0, 0, 6).Format(time.DateOnly),
		Goals: Goals{KcalBase: out.Goals.Energy, WeightKg: out.Goals.Weight, Steps: out.Goals.Steps},
	}
	var days []stats.Day
	for i := 0; i < 7; i++ {
		date := monday.AddDate(0, 0, i).Format(time.DateOnly)
		if date > today {
			w.Running = true
			break
		}
		if date == today {
			w.Running, w.Today = true, date
		}
		d, ok := byDate[date]
		if !ok {
			d = stats.Day{Date: date}
		}
		days = append(days, d)
		w.Days = append(w.Days, day(d, monday.AddDate(0, 0, i), out))
	}
	// Today is still being logged: it stays out of the averages.
	var complete []stats.Day
	for _, d := range days {
		if d.Date != today {
			complete = append(complete, d)
		}
	}
	w.Summary = totals(complete, out)
	var prev []stats.Day
	for i := 7; i > 0; i-- {
		if d, ok := byDate[monday.AddDate(0, 0, -i).Format(time.DateOnly)]; ok {
			prev = append(prev, d)
		}
	}
	w.Previous = totals(prev, out)
	return w
}

func day(d stats.Day, date time.Time, out stats.Output) Day {
	o := Day{
		Date: d.Date, Weekday: date.Weekday().String(), Logged: d.Energy > 0,
		Kcal: d.Energy, KcalGoal: d.EnergyGoal, ProteinG: d.Protein, CarbsG: d.Carb, FatG: d.Fat,
		Meals: d.Meals, Steps: d.Steps, WeightKg: d.Weight, BurnedKcal: d.FitbitKcal,
		RestingHR: d.RestingHR, HRV: d.HRV, SleepHR: d.SleepHR,
	}
	// The app scales macro goals with the day's activity bonus.
	if d.ProteinGoal > 0 {
		o.ProteinGl = d.ProteinGoal
		if d.EnergyBase > 0 && d.EnergyGoal > 0 {
			o.ProteinGl = round(d.ProteinGoal * d.EnergyGoal / d.EnergyBase)
		}
	}
	if z := d.ZoneMinutes; z != nil {
		o.ZoneMin = z[0] + z[1] + z[2]
	}
	for _, f := range d.Foods {
		food := Food{Meal: f.Daytime, Name: f.Name, Amount: amount(f), Kcal: f.Energy}
		if p, ok := out.Products[f.Barcode]; ok {
			food.NutriScore, food.NOVA = p.NutriScore, p.NOVA
		}
		if r, ok := out.Recipes[f.Recipe]; ok {
			for _, in := range r.Ingredients {
				food.Recipe = append(food.Recipe, in.Name)
			}
		}
		o.Foods = append(o.Foods, food)
	}
	// Fitbit workouts carry the details; Yazio-only activities are listed
	// as logged.
	linked := map[string]bool{}
	for _, w := range d.Workouts {
		linked[w.ID] = true
		name := w.Name
		if name == "" {
			name = w.Type
		}
		o.Activities = append(o.Activities, Activity{
			Name: name, Start: w.Start, Duration: dur(w.Duration / 60), Kcal: w.Energy,
			Km: round1(w.Distance / 1000), AvgHR: w.AvgHR, MaxHR: w.MaxHR, ZoneMin: w.ZoneMin,
		})
	}
	for _, a := range d.Activities {
		if a.Workout != "" && linked[a.Workout] {
			continue
		}
		o.Activities = append(o.Activities, Activity{Name: a.Name, Duration: dur(a.Duration), Kcal: a.Energy, Km: round1(a.Distance / 1000)})
	}
	if s := d.Sleep; s != nil {
		night := date.AddDate(0, 0, -1).Weekday().String() + " to " + date.Weekday().String()
		o.Sleep = &Sleep{Night: night, Bed: s.Start, Wake: s.End, Asleep: dur(s.Asleep), Deep: dur(s.Deep), REM: dur(s.REM), Awake: dur(s.Awake)}
	}
	return o
}

func amount(f stats.Food) string {
	if f.Amount == 0 {
		return ""
	}
	unit := f.Unit
	if unit == "portion" {
		unit = "portion(s)"
	}
	return fmt.Sprintf("%g %s", f.Amount, unit)
}

func totals(days []stats.Day, out stats.Output) Totals {
	var t Totals
	var kcal, goal, protein, steps, sleep, rhr, hrv, weights []float64
	var workoutMin float64
	nova, nutri := map[string]float64{}, map[string]float64{}
	var foodKcal float64
	for _, d := range days {
		if d.Energy > 0 {
			t.DaysLogged++
			kcal = append(kcal, d.Energy)
			protein = append(protein, d.Protein)
			if d.EnergyGoal > 0 {
				goal = append(goal, d.EnergyGoal)
			}
		}
		if d.Steps > 0 {
			steps = append(steps, d.Steps)
		}
		if d.Sleep != nil {
			sleep = append(sleep, d.Sleep.Asleep)
		}
		if d.RestingHR > 0 {
			rhr = append(rhr, d.RestingHR)
		}
		if d.HRV > 0 {
			hrv = append(hrv, d.HRV)
		}
		if d.Weight != nil {
			weights = append(weights, *d.Weight)
			if t.WeightFirst == nil {
				t.WeightFirst = d.Weight
			}
			t.WeightLast = d.Weight
		}
		for _, w := range d.Workouts {
			t.Workouts++
			workoutMin += w.Duration / 60
		}
		for _, f := range d.Foods {
			foodKcal += f.Energy
			for _, part := range parts(f, out) {
				p, ok := out.Products[part.barcode]
				n, s := "unknown", "unknown"
				if ok && p.NOVA > 0 {
					n = fmt.Sprint(p.NOVA)
				}
				if ok && len(p.NutriScore) == 1 {
					s = p.NutriScore
				}
				nova[n] += part.kcal
				nutri[s] += part.kcal
			}
		}
	}
	t.AvgKcal, t.AvgKcalGoal, t.AvgProteinG = avg(kcal), avg(goal), avg(protein)
	t.AvgSteps, t.AvgRestingHR, t.AvgHRV = avg(steps), avg(rhr), round1(avgRaw(hrv))
	t.AvgSleep, t.WorkoutTime = dur(avgRaw(sleep)), dur(workoutMin)
	t.AvgWeightKg, t.WeighIns = round1(avgRaw(weights)), len(weights)
	if foodKcal > 0 {
		t.NOVAShare, t.NutriShare = shares(nova, foodKcal), shares(nutri, foodKcal)
		t.LabelledShare = math.Round((1-nova["unknown"]/foodKcal)*100) / 100
	}
	return t
}

type part struct {
	barcode string
	kcal    float64
}

// parts splits a recipe entry's calories across its ingredients by their
// share of the recipe, as the page's Food quality card does; other foods
// are one part.
func parts(f stats.Food, out stats.Output) []part {
	r, ok := out.Recipes[f.Recipe]
	var total float64
	if ok {
		for _, in := range r.Ingredients {
			total += in.Energy
		}
	}
	if total <= 0 {
		return []part{{f.Barcode, f.Energy}}
	}
	ps := make([]part, len(r.Ingredients))
	for i, in := range r.Ingredients {
		ps[i] = part{in.Barcode, f.Energy * in.Energy / total}
	}
	return ps
}

func shares(m map[string]float64, total float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m {
		out[k] = math.Round(v/total*100) / 100
	}
	return out
}

func avgRaw(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func avg(xs []float64) float64 { return round(avgRaw(xs)) }

// dur writes minutes as the review should: "37 min", "7 h 18 min", "3 h".
// Durations go in already formatted, so the model never has to convert.
func dur(min float64) string {
	m := int(math.Round(min))
	switch {
	case m <= 0:
		return ""
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m%60 == 0:
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}
func round(v float64) float64  { return math.Round(v) }
func round1(v float64) float64 { return math.Round(v*10) / 10 }
