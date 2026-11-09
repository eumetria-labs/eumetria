// Package scores computes Eumetria's own daily scores from the Fitbit data:
// Sleep (the night), Recovery (the body's state in the morning) and
// Readiness (both plus the recent training load), and the training load
// itself. They are Eumetria's formulas, documented here and shown in the
// page part by part; they are not named after or tuned to Fitbit's.
//
// Body measurements are compared with the person's own last 30 days (the
// median and the usual spread), so a normal day scores about 80 and a day
// two usual swings better 100. Sleep is scored against fixed targets and
// the sleep goal. Missing inputs are left out and the rest is scaled up;
// each part says what it was based on.
package scores

import (
	"math"
	"slices"
	"sort"
	"time"

	"eumetria/internal/store"
)

// DefaultSleepGoal is the sleep goal in minutes when none is set.
const DefaultSleepGoal = 480

const (
	baselineDays = 30 // days a measurement is compared with
	baselineMin  = 14 // values needed in them before a measurement counts
	normal       = 80 // the Recovery of a day like the baseline
	perSwing     = 10 // points per usual swing (robust standard deviation)
	tempCap      = 60 // Recovery at most, with the skin temperature up
	tempUp       = 0.7
	// Readiness bands. A usual morning (Recovery 80, a good night, the
	// usual load) comes to about 85, so "ready for a hard session" needs
	// more than that.
	ReadyHard = 90
	ReadyEasy = 75
)

// Day holds the scores of one day; a nil score could not be computed.
type Day struct {
	Sleep     *Score `json:"sleep,omitempty"`
	Recovery  *Score `json:"recovery,omitempty"`
	Readiness *Score `json:"readiness,omitempty"`
	Load      *Load  `json:"load,omitempty"`
}

// Score is a value from 0 to 100 and the parts it was made of. Flags are
// warnings worth showing next to it: "temp" (skin temperature up), "spo2"
// (blood oxygen low).
type Score struct {
	Value int      `json:"value"`
	Parts []Part   `json:"parts"`
	Flags []string `json:"flags,omitempty"`
}

// Part is one input of a score. Points out of Max for Sleep and Readiness;
// for Recovery, Z is how many usual swings the measurement is from its
// baseline (positive is better), Weight its share, Value and Baseline the
// measurement and its 30-day median.
type Part struct {
	Key      string  `json:"key"`
	Points   float64 `json:"points"`
	Max      float64 `json:"max,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Baseline float64 `json:"baseline,omitempty"`
	Z        float64 `json:"z,omitempty"`
	Weight   float64 `json:"weight,omitempty"`
}

// Load is the training load: the active zone minutes of the last 7 days
// (Acute) against the last 28 (Chronic), both per week, and their ratio.
type Load struct {
	Ratio   float64 `json:"ratio"`
	Acute   float64 `json:"acute"`
	Chronic float64 `json:"chronic"`
}

// The usual training load, as a ratio of the last 7 to the last 28 days.
const LoadLow, LoadHigh = 0.8, 1.3

// Compute returns the scores of every day in health that has any, keyed by
// date. sleepGoal is in minutes (DefaultSleepGoal when 0).
func Compute(health map[string]*store.HealthDay, sleepGoal float64) map[string]*Day {
	if sleepGoal <= 0 {
		sleepGoal = DefaultSleepGoal
	}
	dates := make([]string, 0, len(health))
	for d := range health {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	c := calc{health: health, goal: sleepGoal}
	out := map[string]*Day{}
	for _, date := range dates {
		day := &Day{Load: c.load(date), Sleep: c.sleep(date), Recovery: c.recovery(date)}
		day.Readiness = readiness(day)
		if day.Load != nil || day.Sleep != nil || day.Recovery != nil {
			out[date] = day
		}
	}
	return out
}

type calc struct {
	health map[string]*store.HealthDay
	goal   float64
}

// back returns the date n days before date.
func back(date string, n int) string {
	t, _ := time.Parse(time.DateOnly, date)
	return t.AddDate(0, 0, -n).Format(time.DateOnly)
}

// before returns the values of f in the days days before date (date itself
// left out), skipping zeros.
func (c calc) before(date string, days int, f func(*store.HealthDay) float64) []float64 {
	var out []float64
	for k := 1; k <= days; k++ {
		if d := c.health[back(date, k)]; d != nil {
			if v := f(d); v != 0 {
				out = append(out, v)
			}
		}
	}
	return out
}

/* ---------- training load ---------- */

func worn(d *store.HealthDay) bool {
	return d != nil && (d.ZoneMinutes != nil || d.RestingHR > 0 || d.TotalKcal > 0)
}

func zoneMinutes(d *store.HealthDay) float64 {
	if d.ZoneMinutes == nil {
		return 0
	}
	return d.ZoneMinutes[0] + d.ZoneMinutes[1] + d.ZoneMinutes[2]
}

// load: only days the tracker was worn count; a worn day without zone
// minutes is a rest day with 0. Needs 4 worn days of the last 7 and 21 of
// the last 28.
func (c calc) load(date string) *Load {
	if !worn(c.health[date]) {
		return nil
	}
	var acute, chronic float64
	var acuteN, chronicN int
	for k := range 28 {
		d := c.health[back(date, k)]
		if !worn(d) {
			continue
		}
		chronic += zoneMinutes(d)
		chronicN++
		if k < 7 {
			acute += zoneMinutes(d)
			acuteN++
		}
	}
	if acuteN < 4 || chronicN < 21 || chronic == 0 {
		return nil
	}
	a, ch := acute/float64(acuteN)*7, chronic/float64(chronicN)*7
	return &Load{Ratio: round(a/ch, 3), Acute: math.Round(a), Chronic: math.Round(ch)}
}

/* ---------- sleep ---------- */

// ramp is 0 at lo, 1 at hi and linear between; lo may be above hi.
func ramp(v, lo, hi float64) float64 {
	return math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
}

// midpoint is the middle of the night in local minutes since noon, so
// nights around midnight don't wrap.
func midpoint(s *store.Sleep) float64 {
	mid := s.Start.Add(s.End.Sub(s.Start) / 2).UTC().Add(time.Duration(s.UTCOffset) * time.Second)
	m := float64(mid.Hour()*60+mid.Minute()) - 720
	if m < 0 {
		m += 1440
	}
	return m
}

// sleep scores the night that ended on date:
//
//	duration     50  asleep vs. the goal: full at 100 %, 0 at 50 %
//	efficiency   15  asleep / (asleep + awake): full at 92 %, 0 at 75 %
//	deep_rem     15  deep + REM share of sleep: full at 40 %, 0 at 20 %
//	regularity   20  midpoint vs. the median of the last 14 nights:
//	                 full within 30 min, 0 at 2 h (needs 5 nights)
//
// Nights without stages give the deep + REM points to efficiency.
func (c calc) sleep(date string) *Score {
	d := c.health[date]
	if d == nil || d.Sleep == nil || d.Sleep.Asleep <= 0 {
		return nil
	}
	s := d.Sleep
	var parts []Part
	add := func(key string, share, max, value float64) {
		parts = append(parts, Part{Key: key, Points: round(share*max, 1), Max: max, Value: round(value, 3)})
	}
	add("duration", ramp(s.Asleep/c.goal, 0.5, 1), 50, s.Asleep)
	eff := s.Asleep / (s.Asleep + s.Awake)
	if s.Deep+s.Light+s.REM > 0 {
		add("efficiency", ramp(eff, 0.75, 0.92), 15, eff)
		share := (s.Deep + s.REM) / s.Asleep
		add("deep_rem", ramp(share, 0.20, 0.40), 15, share)
	} else {
		add("efficiency", ramp(eff, 0.75, 0.92), 30, eff)
	}
	var mids []float64
	for k := 1; k <= 14; k++ {
		if p := c.health[back(date, k)]; p != nil && p.Sleep != nil && p.Sleep.Asleep > 0 {
			mids = append(mids, midpoint(p.Sleep))
		}
	}
	if len(mids) >= 5 {
		off := math.Abs(midpoint(s) - median(mids))
		add("regularity", ramp(off, 120, 30), 20, off)
	}
	return total(parts, nil)
}

// total scales the parts' points to 0–100.
func total(parts []Part, flags []string) *Score {
	var pts, max float64
	for _, p := range parts {
		pts += p.Points
		max += p.Max
	}
	if max == 0 {
		return nil
	}
	return &Score{Value: int(math.Round(pts / max * 100)), Parts: parts, Flags: flags}
}

/* ---------- recovery ---------- */

// The measurements of Recovery: its weight, whether higher is better, and
// the smallest spread assumed (a very steady baseline would make tiny
// changes look huge).
var recoveryInputs = []struct {
	key    string
	weight float64
	higher bool
	floor  float64
	get    func(*store.HealthDay) float64
}{
	{"hrv", 0.45, true, 2, func(d *store.HealthDay) float64 { return d.HRV }},
	{"resting_hr", 0.30, false, 1, func(d *store.HealthDay) float64 { return d.RestingHR }},
	{"sleeping_hr", 0.15, false, 1, func(d *store.HealthDay) float64 { return d.SleepHR }},
	{"breathing", 0.10, false, 0.3, func(d *store.HealthDay) float64 { return d.Respiratory }},
}

// recovery compares the morning's measurements with their last 30 days:
// z = (value − median) / spread, turned so that positive is better, and
// Recovery = 80 + 10 × the weighted z, from 0 to 100. A measurement needs
// 14 values in its baseline; HRV or resting heart rate must be there.
// A skin temperature 0.7 °C or more above Fitbit's baseline caps it at 60
// (flag "temp"); blood oxygen 2 points under its median or below 92 %
// adds the flag "spo2".
func (c calc) recovery(date string) *Score {
	d := c.health[date]
	if d == nil {
		return nil
	}
	var parts []Part
	var sum, weights float64
	core := false
	for _, in := range recoveryInputs {
		v := in.get(d)
		if v == 0 {
			continue
		}
		base := c.before(date, baselineDays, in.get)
		if len(base) < baselineMin {
			continue
		}
		med := median(base)
		spread := math.Max(in.floor, 1.4826*mad(base, med))
		z := (v - med) / spread
		if !in.higher {
			z = -z
		}
		z = math.Max(-4, math.Min(4, z))
		parts = append(parts, Part{Key: in.key, Value: v, Baseline: round(med, 1), Z: round(z, 2), Weight: in.weight})
		sum += z * in.weight
		weights += in.weight
		core = core || in.key == "hrv" || in.key == "resting_hr"
	}
	if !core {
		return nil
	}
	z := sum / weights
	value := math.Max(0, math.Min(100, normal+perSwing*z))
	for i := range parts {
		// Each part's share of the distance from the normal 80.
		parts[i].Points = round(perSwing*parts[i].Z*parts[i].Weight/weights, 1)
	}
	var flags []string
	if d.SkinTemp != 0 && d.SkinBaseline != 0 && d.SkinTemp-d.SkinBaseline >= tempUp {
		flags = append(flags, "temp")
		value = math.Min(value, tempCap)
	}
	if d.SpO2 > 0 {
		base := c.before(date, baselineDays, func(d *store.HealthDay) float64 { return d.SpO2 })
		if d.SpO2 < 92 || (len(base) >= baselineMin && d.SpO2 <= median(base)-2) {
			flags = append(flags, "spo2")
		}
	}
	return &Score{Value: int(math.Round(value)), Parts: parts, Flags: flags}
}

/* ---------- readiness ---------- */

// loadPoints: 100 within the usual load (0.8–1.3); above it down to 40 at
// 1.8; below it down to 80 at 0.5 (rested, but out of rhythm).
func loadPoints(r float64) float64 {
	switch {
	case r > LoadHigh:
		return 100 - 60*ramp(r, LoadHigh, 1.8)
	case r < LoadLow:
		return 100 - 20*ramp(r, LoadLow, 0.5)
	}
	return 100
}

// readiness: 50 % Recovery, 30 % last night's Sleep, 20 % training load,
// with missing parts left out; it needs Recovery. With the temperature up
// it stays below "take it easy" (ReadyEasy).
func readiness(d *Day) *Score {
	if d.Recovery == nil {
		return nil
	}
	parts := []Part{{Key: "recovery", Points: 0.5 * float64(d.Recovery.Value), Max: 50, Value: float64(d.Recovery.Value)}}
	if d.Sleep != nil {
		parts = append(parts, Part{Key: "sleep", Points: 0.3 * float64(d.Sleep.Value), Max: 30, Value: float64(d.Sleep.Value)})
	}
	if d.Load != nil {
		parts = append(parts, Part{Key: "load", Points: round(0.2*loadPoints(d.Load.Ratio), 1), Max: 20, Value: d.Load.Ratio})
	}
	s := total(parts, d.Recovery.Flags)
	if slices.Contains(d.Recovery.Flags, "temp") && s.Value >= ReadyEasy {
		s.Value = ReadyEasy - 1
	}
	return s
}

/* ---------- helpers ---------- */

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// mad is the median absolute deviation from med.
func mad(v []float64, med float64) float64 {
	dev := make([]float64, len(v))
	for i, x := range v {
		dev[i] = math.Abs(x - med)
	}
	return median(dev)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
