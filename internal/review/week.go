// Package review writes a weekly review of the dashboard's data with Claude:
// one calendar week (Monday to Sunday) of nutrition, training and sleep,
// compared with the week before.
package review

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"eumetria/internal/checkins"
	"eumetria/internal/lifts"
	"eumetria/internal/phases"
	"eumetria/internal/stats"
	"eumetria/internal/wods"
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
	// Results the person noted themselves (the Training card's Personal
	// bests), not measured by a device.
	Lifts      []Lift      `json:"lifts_noted,omitempty"`
	Benchmarks []Benchmark `json:"benchmark_workouts_noted,omitempty"`
	// What the person said they are doing: the phases that overlap the
	// week (a cut, a bulk, …) and the events in it or ahead.
	Phases []Phase `json:"phases,omitempty"`
	Events []Event `json:"events,omitempty"`
	// Up to three minerals or vitamins furthest below their daily reference
	// value this week, only where the foods' data cover most of the kcal.
	NutrientsLow []NutrientLow `json:"nutrients_below_reference,omitempty"`
	// When the person slept: the week's regularity, and the chronotype
	// from the four weeks up to its end.
	SleepTiming *SleepTiming `json:"sleep_timing,omitempty"`
	// Training sessions noted set by set.
	Sessions []TrainingSession `json:"training_sessions_noted,omitempty"`
	// How many of the week's complete days met each goal.
	GoalHits []GoalHit `json:"goal_hit_rates,omitempty"`

	// The week's Fitbit workouts by id, as their activity rows name them
	// ("CrossFit at 16:25"): where a linked lift or benchmark was done.
	sessions map[string]string
	// The four weeks up to the week's end, for the chronotype again once
	// the days away and off are known (AddPhases, AddCheckins): the weekdays
	// off and holidays (free), vacation and day-off days (off), travel.
	recent []stats.Day
	// The complete days, the goals and the phases, for the hit rates again
	// once the phases are known (AddPhases).
	complete     []stats.Day
	sleepGoal    float64
	alcoholKnown bool
	phaseList    []phases.Phase
	free         stats.Output
	off          map[string]bool
	travel       map[string]bool
}

// SleepTiming is the week's sleep regularity and the chronotype of the
// last four weeks (stats/sleeptiming.go).
type SleepTiming struct {
	Regularity stats.Regularity  `json:"this_week"`
	Chronotype *stats.Chronotype `json:"last_4_weeks,omitempty"`
}

// NutrientLow is a mineral or vitamin the week's logged foods brought less
// of than the daily reference value (DGE/ÖGE/SGE).
type NutrientLow struct {
	Name      string  `json:"name"`
	PerDay    float64 `json:"avg_per_logged_day"`
	Unit      string  `json:"unit"`
	Reference float64 `json:"reference_per_day"`
	Percent   int     `json:"percent_of_reference"`
	Known     float64 `json:"share_of_kcal_with_data"`
}

// Phase is a stretch of days with a purpose the person noted.
type Phase struct {
	Kind  string `json:"kind"` // cut, bulk, maintain or other
	Name  string `json:"name,omitempty"`
	Start string `json:"start"`
	End   string `json:"end,omitempty"` // empty while it runs
	// The week of the phase that the review's last day falls in, from 1.
	Week int    `json:"week_of_phase"`
	Note string `json:"note,omitempty"`
}

// Event is a one-day event the person noted (a competition, a race).
type Event struct {
	Name string `json:"name"`
	Date string `json:"date"`
	// Days from the review's last day: positive ahead, 0 that day,
	// negative during the week.
	DaysAway int    `json:"days_from_last_day"`
	Note     string `json:"note,omitempty"`
}

// eventsAhead is how far ahead an event still counts.
const eventsAhead = 90

// Benchmark is one noted result of a benchmark workout, written as people
// say it ("4:12", "12 rounds + 5 reps").
type Benchmark struct {
	Date    string `json:"date"`
	Workout string `json:"workout"`
	Scoring string `json:"scored_by"`
	Result  string `json:"result"`
	Scaled  bool   `json:"scaled,omitempty"`
	// Better than every earlier result of this workout, Rx and scaled
	// counted apart; the first result is neither a best nor compared.
	Best  bool   `json:"new_personal_best,omitempty"`
	Prev  string `json:"previous_best,omitempty"`
	First bool   `json:"first_result_noted,omitempty"`
	Note  string `json:"note,omitempty"`
	// The activity of that day it was done in, if linked to one.
	DoneIn string `json:"done_in,omitempty"`
}

// Lift is one noted result: kg lifted reps times.
type Lift struct {
	Date string  `json:"date"`
	Lift string  `json:"lift"`
	Kg   float64 `json:"kg"`
	Reps int     `json:"reps"`
	// Better than every earlier result of this lift, whatever their reps:
	// heavier, or as heavy for more reps. The first result of a lift is
	// neither a best nor compared.
	Best     bool    `json:"new_personal_best,omitempty"`
	PrevKg   float64 `json:"previous_best_kg,omitempty"`
	PrevReps int     `json:"previous_best_reps,omitempty"`
	First    bool    `json:"first_result_noted,omitempty"`
	Note     string  `json:"note,omitempty"`
	// The activity of that day it was lifted in, if linked to one.
	DoneIn string `json:"done_in,omitempty"`
}

type Goals struct {
	KcalBase float64 `json:"kcal_base_goal,omitempty"`
	WeightKg float64 `json:"weight_goal_kg,omitempty"`
	Steps    float64 `json:"steps_goal,omitempty"`
}

type Day struct {
	Date      string             `json:"date"`
	Weekday   string             `json:"weekday"`
	Logged    bool               `json:"food_logged"`
	Kcal      float64            `json:"kcal_eaten,omitempty"`
	KcalGoal  float64            `json:"kcal_goal_incl_activity,omitempty"`
	ProteinG  float64            `json:"protein_g,omitempty"`
	ProteinGl float64            `json:"protein_goal_g,omitempty"`
	CarbsG    float64            `json:"carbs_g,omitempty"`
	FatG      float64            `json:"fat_g,omitempty"`
	Meals     map[string]float64 `json:"meals_kcal,omitempty"`
	Foods     []Food             `json:"foods,omitempty"`
	Steps     float64            `json:"steps,omitempty"`
	WeightKg  *float64           `json:"weight_kg,omitempty"`
	// The scale's morning weigh-in: where the weight comes from, and the
	// body composition with it.
	WeightFrom string      `json:"weight_from,omitempty"`
	Body       *stats.Body `json:"body_composition,omitempty"`
	BurnedKcal float64     `json:"kcal_burned_fitbit,omitempty"`
	Activities []Activity  `json:"activities,omitempty"`
	Sleep      *Sleep      `json:"sleep,omitempty"` // the night that ended this morning
	RestingHR  float64     `json:"resting_hr,omitempty"`
	HRV        float64     `json:"hrv_ms,omitempty"`
	SleepHR    float64     `json:"sleeping_hr,omitempty"`
	ZoneMin    float64     `json:"active_zone_minutes,omitempty"`
	// Eumetria's own scores (0–100) and the training load ratio.
	SleepScore int      `json:"eumetria_sleep_score,omitempty"`
	Recovery   int      `json:"eumetria_recovery,omitempty"`
	Readiness  int      `json:"eumetria_readiness,omitempty"`
	Flags      []string `json:"recovery_flags,omitempty"`
	LoadRatio  float64  `json:"training_load_ratio,omitempty"`
	// Alcohol in the foods logged, g.
	AlcoholG float64 `json:"alcohol_g,omitempty"`
	// The morning check-in (1 low to 5 high) and the day's tags.
	Checkin *Checkin `json:"checkin,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// Checkin is how the day felt, as noted that morning.
type Checkin struct {
	Energy   *int   `json:"energy_1_to_5,omitempty"`
	Soreness *int   `json:"soreness_1_to_5,omitempty"`
	Stress   *int   `json:"stress_1_to_5,omitempty"`
	Mood     *int   `json:"mood_1_to_5,omitempty"`
	Note     string `json:"note,omitempty"`
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
	DaysLogged    int     `json:"days_food_logged"`
	AvgKcal       float64 `json:"avg_kcal_eaten,omitempty"`
	AvgKcalGoal   float64 `json:"avg_kcal_goal,omitempty"`
	AvgProteinG   float64 `json:"avg_protein_g,omitempty"`
	AvgSteps      float64 `json:"avg_steps,omitempty"`
	Workouts      int     `json:"workouts"`
	WorkoutTime   string  `json:"workout_time,omitempty"`
	AvgSleep      string  `json:"avg_sleep,omitempty"`
	AvgRestingHR  float64 `json:"avg_resting_hr,omitempty"`
	AvgHRV        float64 `json:"avg_hrv_ms,omitempty"`
	AvgSleepScore float64 `json:"avg_eumetria_sleep_score,omitempty"`
	AvgRecovery   float64 `json:"avg_eumetria_recovery,omitempty"`
	AvgReadiness  float64 `json:"avg_eumetria_readiness,omitempty"`
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
		for _, x := range d.Workouts {
			if w.sessions == nil {
				w.sessions = map[string]string{}
			}
			w.sessions[x.ID] = sessionName(x)
		}
	}
	// Today is still being logged: it stays out of the averages.
	var complete []stats.Day
	for _, d := range days {
		if d.Date != today {
			complete = append(complete, d)
		}
	}
	w.Summary = totals(complete, out)
	w.complete, w.sleepGoal, w.alcoholKnown = complete, out.SleepGoal, alcoholEver(out)
	w.goalHits()
	w.NutrientsLow = nutrientsLow(complete, out.NutrientRefs)
	var prev []stats.Day
	for i := 7; i > 0; i-- {
		if d, ok := byDate[monday.AddDate(0, 0, -i).Format(time.DateOnly)]; ok {
			prev = append(prev, d)
		}
	}
	w.Previous = totals(prev, out)
	// The nights ending this morning count, today's too.
	for i := 27; i >= 0; i-- {
		date := monday.AddDate(0, 0, 6-i).Format(time.DateOnly)
		if d, ok := byDate[date]; ok && date <= today {
			w.recent = append(w.recent, d)
		}
	}
	w.free = stats.Output{FreeDays: out.FreeDays, Holidays: out.Holidays}
	w.off, w.travel = map[string]bool{}, map[string]bool{}
	w.sleepTiming(days)
	return w
}

// sleepTiming sets SleepTiming from the week's days; nil without nights.
func (w *Week) sleepTiming(days []stats.Day) {
	r := stats.SleepRegularity(days)
	if r.Nights == 0 {
		w.SleepTiming = nil
		return
	}
	w.SleepTiming = &SleepTiming{Regularity: r}
	w.chronotype()
}

// chronotype sets the chronotype of the last four weeks from what is known
// of the days off and away.
func (w *Week) chronotype() {
	if w.SleepTiming == nil {
		return
	}
	w.SleepTiming.Chronotype = nil
	if c := stats.SleepChronotype(w.recent, w.travel, stats.FreeNightOf(w.free, w.off)); c.OK {
		w.SleepTiming.Chronotype = &c
	}
}

func day(d stats.Day, date time.Time, out stats.Output) Day {
	o := Day{
		Date: d.Date, Weekday: date.Weekday().String(), Logged: d.Energy > 0,
		Kcal: d.Energy, KcalGoal: d.EnergyGoal, ProteinG: d.Protein, CarbsG: d.Carb, FatG: d.Fat,
		Meals: d.Meals, Steps: d.Steps, WeightKg: d.Weight, WeightFrom: d.WeightSource, Body: d.Body, BurnedKcal: d.FitbitKcal, AlcoholG: d.Nutrients["alcohol"][0],
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
	if s := d.Scores; s != nil {
		if s.Sleep != nil {
			o.SleepScore = s.Sleep.Value
		}
		if s.Recovery != nil {
			o.Recovery, o.Flags = s.Recovery.Value, s.Recovery.Flags
		}
		if s.Readiness != nil {
			o.Readiness = s.Readiness.Value
		}
		if s.Load != nil {
			o.LoadRatio = math.Round(s.Load.Ratio*100) / 100
		}
	}
	for _, f := range d.Foods {
		food := Food{Meal: f.Daytime, Name: clip(f.Name, nameMax), Amount: amount(f), Kcal: f.Energy}
		if p, ok := out.Products[f.Barcode]; ok {
			food.NutriScore, food.NOVA = p.NutriScore, p.NOVA
		}
		if r, ok := out.Recipes[f.Recipe]; ok {
			for _, in := range r.Ingredients {
				food.Recipe = append(food.Recipe, clip(in.Name, nameMax))
			}
		}
		o.Foods = append(o.Foods, food)
	}
	// Fitbit workouts carry the details; Yazio-only activities are listed
	// as logged.
	linked := map[string]bool{}
	for _, w := range d.Workouts {
		linked[w.ID] = true
		o.Activities = append(o.Activities, Activity{
			Name: activityName(w), Start: w.Start, Duration: dur(w.Duration / 60), Kcal: w.Energy,
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
	var kcal, goal, protein, steps, sleep, rhr, hrv, weights, sleepScore, recovery, readiness []float64
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
		if s := d.Scores; s != nil {
			if s.Sleep != nil {
				sleepScore = append(sleepScore, float64(s.Sleep.Value))
			}
			if s.Recovery != nil {
				recovery = append(recovery, float64(s.Recovery.Value))
			}
			if s.Readiness != nil {
				readiness = append(readiness, float64(s.Readiness.Value))
			}
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
	t.AvgSleepScore, t.AvgRecovery, t.AvgReadiness = avg(sleepScore), avg(recovery), avg(readiness)
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

// AddPhases adds the phases that overlap the week and the events in it or
// within 90 days after it.
func (w *Week) AddPhases(all []phases.Phase) {
	last := w.To
	if len(w.Days) > 0 {
		last = w.Days[len(w.Days)-1].Date
	}
	lastDay, err := time.Parse(time.DateOnly, last)
	if err != nil {
		return
	}
	if w.off == nil {
		w.off = map[string]bool{}
	}
	w.phaseList = all
	w.goalHits()
	for _, p := range all {
		start, err := time.Parse(time.DateOnly, p.Start)
		if err != nil {
			continue
		}
		// A vacation's days are days off for the chronotype.
		if p.Kind == phases.Vacation {
			for d := start; d.Format(time.DateOnly) <= last && (p.End == "" || d.Format(time.DateOnly) <= p.End); d = d.AddDate(0, 0, 1) {
				w.off[d.Format(time.DateOnly)] = true
			}
		}
		if p.Kind == phases.Event {
			days := int(math.Round(start.Sub(lastDay).Hours() / 24))
			if p.Start >= w.From && days <= eventsAhead {
				w.Events = append(w.Events, Event{Name: clip(p.Label, nameMax), Date: p.Start, DaysAway: days, Note: clip(p.Note, noteMax)})
			}
			continue
		}
		if p.Start > last || (p.End != "" && p.End < w.From) {
			continue
		}
		at := lastDay
		if p.End != "" && p.End < last {
			at, _ = time.Parse(time.DateOnly, p.End)
		}
		week := int(at.Sub(start).Hours()/24)/7 + 1
		w.Phases = append(w.Phases, Phase{Kind: p.Kind, Name: clip(p.Label, nameMax), Start: p.Start, End: p.End, Week: week, Note: clip(p.Note, noteMax)})
	}
	w.chronotype()
}

// AddLifts adds the week's results from all noted ones, each compared with
// the best before it (same lift, case aside, any rep count).
func (w *Week) AddLifts(all []lifts.Entry) {
	last := w.To
	if len(w.Days) > 0 {
		last = w.Days[len(w.Days)-1].Date
	}
	sorted := append([]lifts.Entry(nil), all...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].ID < sorted[j].ID
	})
	best := map[string]lifts.Entry{}
	for _, e := range sorted {
		key := strings.ToLower(e.Lift)
		prev, seen := best[key]
		better := !seen || lifts.Better(e, prev)
		if e.Date >= w.From && e.Date <= last {
			l := Lift{Date: e.Date, Lift: clip(e.Lift, nameMax), Kg: e.Kg, Reps: e.Reps, Note: clip(e.Note, noteMax), First: !seen, DoneIn: w.sessions[e.FitbitWorkout]}
			if seen && better {
				l.Best, l.PrevKg, l.PrevReps = true, prev.Kg, prev.Reps
			}
			w.Lifts = append(w.Lifts, l)
		}
		if better {
			best[key] = e
		}
	}
}

// AddBenchmarks adds the week's results of benchmark workouts, each compared
// with the best before it. results come the oldest first (wods.Store).
func (w *Week) AddBenchmarks(workouts []wods.Workout, results []wods.Result) {
	last := w.To
	if len(w.Days) > 0 {
		last = w.Days[len(w.Days)-1].Date
	}
	byID := map[int64]wods.Workout{}
	for _, wd := range workouts {
		byID[wd.ID] = wd
	}
	sorted := append([]wods.Result(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].ID < sorted[j].ID
	})
	type key struct {
		wod    int64
		scaled bool
	}
	best := map[key]wods.Result{}
	for _, r := range sorted {
		wd, ok := byID[r.Workout]
		if !ok {
			continue
		}
		k := key{r.Workout, r.Scaled}
		prev, seen := best[k]
		better := !seen || wods.Better(wd.Scoring, r, prev)
		if r.Date >= w.From && r.Date <= last {
			b := Benchmark{Date: r.Date, Workout: clip(wd.Name, nameMax), Scoring: wd.Scoring, Result: wods.Format(wd.Scoring, r), Scaled: r.Scaled, Note: clip(r.Note, noteMax), First: !seen, DoneIn: w.sessions[r.FitbitWorkout]}
			if seen && better {
				b.Best, b.Prev = true, wods.Format(wd.Scoring, prev)
			}
			w.Benchmarks = append(w.Benchmarks, b)
		}
		if better {
			best[k] = r
		}
	}
}

// activityName is what a Fitbit workout's activity row is called.
func activityName(w stats.Workout) string {
	if w.Name != "" {
		return clip(w.Name, nameMax)
	}
	return w.Type
}

// sessionName names a workout as its activity row does, with its start, so
// a result can say which one of the day it was done in.
func sessionName(w stats.Workout) string {
	if w.Start == "" {
		return activityName(w)
	}
	return activityName(w) + " at " + w.Start
}

// Free text goes to the model short and without control characters
// (pentest F17): people with edit.lifts, and Yazio, write it, and a long
// note is room for instructions. The system prompt says it is data only.
const (
	nameMax = 80
	noteMax = 200
)

func clip(s string, most int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > most {
		return string(r[:most-1]) + "…"
	}
	return s
}

// nutrientsLow lists the minerals and vitamins the logged days brought least
// of against their references: at most three, below 100 %, and only where
// the foods' data cover at least 70 % of the kcal (else a low number may
// just be missing data).
func nutrientsLow(days []stats.Day, refs map[string]stats.Reference) []NutrientLow {
	var out []NutrientLow
	for key, ref := range refs {
		if key == "fiber" || ref.Amount <= 0 {
			continue
		}
		var sum, kcal, known float64
		var n int
		for _, d := range days {
			if d.Energy <= 0 {
				continue
			}
			n++
			v := d.Nutrients[key]
			sum += v[0]
			kcal += d.Energy
			known += v[1] * d.Energy
		}
		if n == 0 || kcal == 0 || known/kcal < 0.7 {
			continue
		}
		per := sum / float64(n)
		pct := int(math.Round(per / ref.Amount * 100))
		if pct >= 100 {
			continue
		}
		out = append(out, NutrientLow{Name: strings.ReplaceAll(key, "_", " "), PerDay: round1(per), Unit: stats.NutrientUnit(key),
			Reference: ref.Amount, Percent: pct, Known: math.Round(known/kcal*100) / 100})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Percent < out[j].Percent || out[i].Percent == out[j].Percent && out[i].Name < out[j].Name
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// alcoholDay is the alcohol (g) that tags a day "alcohol" by itself, as on
// the page (nutrients.js, ALCOHOL_DAY).
const alcoholDay = 10

// AddCheckins puts each day's check-in and tags on the week's days. A day
// with 10 g of alcohol or more is tagged "alcohol" unless someone removed
// that tag.
func (w *Week) AddCheckins(all []checkins.Checkin) {
	by := map[string]checkins.Checkin{}
	for _, c := range all {
		by[c.Date] = c
	}
	for i := range w.Days {
		d := &w.Days[i]
		c, ok := by[d.Date]
		if ok && (c.Energy != nil || c.Soreness != nil || c.Stress != nil || c.Mood != nil || c.Note != "") {
			d.Checkin = &Checkin{Energy: c.Energy, Soreness: c.Soreness, Stress: c.Stress, Mood: c.Mood, Note: c.Note}
		}
		tags := slices.Clone(c.Tags)
		if d.AlcoholG >= alcoholDay && !slices.Contains(tags, "alcohol") && !slices.Contains(c.Dismissed, "alcohol") {
			tags = append([]string{"alcohol"}, tags...)
		}
		d.Tags = tags
	}
	// The chronotype again without the nights away, and with the days off.
	if w.off == nil {
		w.off, w.travel = map[string]bool{}, map[string]bool{}
	}
	for _, c := range all {
		if slices.Contains(c.Tags, "travel") {
			w.travel[c.Date] = true
		}
		if slices.Contains(c.Tags, "day_off") {
			w.off[c.Date] = true
		}
	}
	w.chronotype()
}
