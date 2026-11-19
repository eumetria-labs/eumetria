package stats

// Sleep timing, as site/js/sleeptiming.js computes it for the page: the
// weekly review gets the same numbers (tests/fixtures/sleeptiming.json
// keeps the two equal). Regularity: the standard deviation of bedtime and
// wake time, and the Sleep Regularity Index (Phillips et al. 2017).
// Chronotype: mid-sleep on free nights (ending on a Saturday or Sunday
// morning), corrected for sleep caught up (MSFsc), and social jetlag
// (Roenneberg et al. 2012). Times are the nights' local "HH:MM", so
// daylight saving time needs no care.

import (
	"math"
	"time"
)

const (
	minNights = 4
	minPairs  = 4
	minFree   = 4
	minWork   = 4
)

func hhmm(s string) int {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0
	}
	return t.Hour()*60 + t.Minute()
}

// Minutes since the noon before (bedtime), since the 18:00 before (waking),
// asleep to awake, and the middle of the night since the noon before.
func onsetMin(s *Sleep) int   { return (hhmm(s.Start) - 720 + 1440) % 1440 }
func wakeMin(s *Sleep) int    { return (hhmm(s.End) - 1080 + 1440) % 1440 }
func spanMin(s *Sleep) int    { return (hhmm(s.End) - hhmm(s.Start) + 1440) % 1440 }
func midMin(s *Sleep) float64 { return float64(onsetMin(s)) + float64(spanMin(s))/2 }
func hasTimes(s *Sleep) bool  { return s != nil && s.Start != "" && s.End != "" }
func roundInt(v float64) int  { return int(math.Round(v)) }
func meanOf(xs []float64) float64 {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func sampleSD(xs []float64) float64 {
	m := meanOf(xs)
	var sum float64
	for _, x := range xs {
		sum += (x - m) * (x - m)
	}
	return math.Sqrt(sum / float64(len(xs)-1))
}

// asleepMask is the night minute by minute from the noon before to noon:
// true while asleep.
func asleepMask(s *Sleep) [1440]bool {
	var m [1440]bool
	o := onsetMin(s)
	n := min(spanMin(s), 1440-o)
	for i := o; i < o+n; i++ {
		m[i] = true
	}
	for _, st := range s.Stages {
		if st[2] != 0 {
			continue
		}
		from, to := min(1440, o+int(st[0])), min(1440, o+int(st[0]+st[1]))
		for i := from; i < to; i++ {
			m[i] = false
		}
	}
	return m
}

// Regularity is how alike the nights are: SDs in minutes and the SRI
// (-100…100), nil while there are too few nights or pairs of nights.
type Regularity struct {
	Nights  int  `json:"nights"`
	OnsetSD *int `json:"bedtime_sd_min,omitempty"`
	WakeSD  *int `json:"wake_time_sd_min,omitempty"`
	Pairs   int  `json:"night_pairs"`
	SRI     *int `json:"sleep_regularity_index,omitempty"`
}

// SleepRegularity of days (in date order).
func SleepRegularity(days []Day) Regularity {
	var out Regularity
	byDate := map[string]*Sleep{}
	var onsets, wakes []float64
	for _, d := range days {
		if !hasTimes(d.Sleep) {
			continue
		}
		out.Nights++
		byDate[d.Date] = d.Sleep
		onsets = append(onsets, float64(onsetMin(d.Sleep)))
		wakes = append(wakes, float64(wakeMin(d.Sleep)))
	}
	if out.Nights >= minNights {
		o, w := roundInt(sampleSD(onsets)), roundInt(sampleSD(wakes))
		out.OnsetSD, out.WakeSD = &o, &w
	}
	same, total := 0, 0
	for _, d := range days {
		if !hasTimes(d.Sleep) {
			continue
		}
		day, err := time.Parse(time.DateOnly, d.Date)
		if err != nil {
			continue
		}
		next := byDate[day.AddDate(0, 0, 1).Format(time.DateOnly)]
		if next == nil {
			continue
		}
		a, b := asleepMask(d.Sleep), asleepMask(next)
		for i := range a {
			if a[i] == b[i] {
				same++
			}
		}
		total += 1440
		out.Pairs++
	}
	if out.Pairs >= minPairs {
		v := roundInt(200*float64(same)/float64(total) - 100)
		out.SRI = &v
	}
	return out
}

// Chronotype is mid-sleep on free and work nights as "HH:MM", and the
// social jetlag in minutes; OK false with too few of either.
type Chronotype struct {
	FreeNights int    `json:"free_nights"`
	WorkNights int    `json:"work_nights"`
	OK         bool   `json:"-"`
	MSF        string `json:"mid_sleep_free_days,omitempty"`
	MSFsc      string `json:"mid_sleep_free_days_corrected,omitempty"`
	MSW        string `json:"mid_sleep_work_days,omitempty"`
	JetlagMin  *int   `json:"social_jetlag_min,omitempty"`
	// The same in minutes since noon, for the tests.
	msf, msfsc, msw int
}

func freeNight(day time.Time) bool {
	return day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
}

// clockOf is minutes since noon as "HH:MM".
func clockOf(sinceNoon int) string {
	m := (sinceNoon + 720) % 1440
	return time.Date(0, 1, 1, m/60, m%60, 0, 0, time.UTC).Format("15:04")
}

// SleepChronotype of days, without the nights whose date or the evening
// before is in skip (travel); free tells the free nights (nil: those
// ending on a Saturday or Sunday morning).
func SleepChronotype(days []Day, skip map[string]bool, free FreeNight) Chronotype {
	if free == nil {
		free = func(date string) bool {
			d, _ := time.Parse(time.DateOnly, date)
			return freeNight(d)
		}
	}
	var out Chronotype
	var freeMid, workMid, freeSpan, workSpan []float64
	for _, d := range days {
		day, err := time.Parse(time.DateOnly, d.Date)
		if err != nil || !hasTimes(d.Sleep) || skip[d.Date] || skip[day.AddDate(0, 0, -1).Format(time.DateOnly)] {
			continue
		}
		if free(d.Date) {
			freeMid, freeSpan = append(freeMid, midMin(d.Sleep)), append(freeSpan, float64(spanMin(d.Sleep)))
		} else {
			workMid, workSpan = append(workMid, midMin(d.Sleep)), append(workSpan, float64(spanMin(d.Sleep)))
		}
	}
	out.FreeNights, out.WorkNights = len(freeMid), len(workMid)
	if out.FreeNights < minFree || out.WorkNights < minWork {
		return out
	}
	msf, msw := meanOf(freeMid), meanOf(workMid)
	sdf, sdw := meanOf(freeSpan), meanOf(workSpan)
	msfsc := msf
	if sdf > sdw {
		msfsc = msf - (sdf-(5*sdw+2*sdf)/7)/2
	}
	out.OK = true
	out.msf, out.msfsc, out.msw = roundInt(msf), roundInt(msfsc), roundInt(msw)
	out.MSF, out.MSFsc, out.MSW = clockOf(out.msf), clockOf(out.msfsc), clockOf(out.msw)
	j := roundInt(msf - msw)
	out.JetlagMin = &j
	return out
}

// freeDays are the weekdays off, Saturday and Sunday when not set.
func freeDays(days []int) []int {
	if days == nil {
		return []int{0, 6}
	}
	return days
}

// FreeNight tells whether the night ending on a date's morning is free:
// a weekday off, a public holiday, or a day in vacation (or tagged day off;
// the review adds those).
type FreeNight func(date string) bool

// FreeNightOf is FreeNight for out's weekdays off and holidays, and the
// dates in also.
func FreeNightOf(out Output, also map[string]bool) FreeNight {
	days := freeDays(out.FreeDays)
	return func(date string) bool {
		d, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return false
		}
		for _, w := range days {
			if int(d.Weekday()) == w {
				return true
			}
		}
		return out.Holidays[date] != "" || also[date]
	}
}
