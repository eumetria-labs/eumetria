package demo

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"eumetria/internal/store"
)

// workoutTypes maps the demo activities to Google Health exercise types;
// gps marks the ones recorded with a route.
var workoutTypes = map[string]struct {
	typ   string
	gps   bool
	hrMid float64 // typical average heart rate
}{
	"Running":           {"RUNNING", true, 152},
	"Cycling":           {"BIKING", true, 138},
	"Walking":           {"WALKING", true, 104},
	"Swimming":          {"SWIMMING", false, 135},
	"Strength training": {"STRENGTH_TRAINING", false, 112},
	"Yoga":              {"YOGA", false, 92},
	"HIIT":              {"HIIT", false, 158},
}

const demoOffset = 7200 // seconds; the demo lives in UTC+2

// demoZones are the lower bounds of moderate, vigorous and peak (bpm).
var demoZones = [3]float64{110, 133, 163}

// healthDay generates a night of sleep, the daily values and, when the day
// has a Fitbit activity, its workout. t runs from 0 (oldest day) to 1.
func healthDay(r *rand.Rand, day time.Time, t float64, act *store.Activity) *store.HealthDay {
	date := day.Format(time.DateOnly)
	z := demoZones
	hd := &store.HealthDay{
		Date:         date,
		FetchedAt:    time.Now().UTC(),
		RestingHR:    math.Round(66 - 5*t + r.NormFloat64()*1.5),
		HRV:          math.Round((30+9*t+r.NormFloat64()*4)*10) / 10,
		HRVDeep:      math.Round((26+8*t+r.NormFloat64()*4)*10) / 10,
		HRVEntropy:   math.Round((2.8+r.NormFloat64()*0.15)*100) / 100,
		SleepHR:      math.Round(58 - 4*t + r.NormFloat64()*1.5),
		Respiratory:  math.Round((15.8+r.NormFloat64()*0.5)*10) / 10,
		SpO2:         math.Round((97.4+r.NormFloat64()*0.6)*10) / 10,
		SkinBaseline: 33.4,
		SkinTemp:     math.Round((33.4+r.NormFloat64()*0.3)*100) / 100,
		Zones:        &z,
		Sleep:        sleep(r, day),
		TotalKcal:    math.Round(2350 + r.NormFloat64()*180),
	}
	azm := [3]float64{float64(r.IntN(15)), 0, 0}
	if act != nil {
		if wt, ok := workoutTypes[act.Name]; ok {
			w := workout(r, day, act, wt.typ, wt.gps, wt.hrMid)
			act.ExternalID = w.ID
			hd.Workouts = append(hd.Workouts, w)
			hd.TotalKcal += math.Round(w.Calories * 0.8)
			azm[0] += w.ZoneSec[1] / 60
			azm[1] += 2 * w.ZoneSec[2] / 60
			azm[2] += 2 * w.ZoneSec[3] / 60
		}
	}
	hd.ZoneMinutes = &azm
	return hd
}

// Devices is a tracker that synced a few minutes ago.
func Devices(now time.Time) []store.Device {
	return []store.Device{{Type: "TRACKER", Name: "Fitbit (demo)", Battery: 64, BatteryStatus: "Medium", LastSync: now.Add(-18 * time.Minute).UTC()}}
}

// local returns the UTC instant of a local demo time on day.
func local(day time.Time, h, m int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.UTC).Add(-demoOffset * time.Second)
}

// sleep generates the night that ended on the morning of day: about 90-minute
// cycles of light, deep and REM sleep with a few brief awakenings.
func sleep(r *rand.Rand, day time.Time) *store.Sleep {
	start := local(day.AddDate(0, 0, -1), 22, 30).Add(time.Duration(r.IntN(70)) * time.Minute)
	end := local(day, 6, 0).Add(time.Duration(r.IntN(80)) * time.Minute)
	s := &store.Sleep{Start: start, End: end, UTCOffset: demoOffset}
	add := func(stage string, from time.Time, min float64) time.Time {
		to := from.Add(time.Duration(min) * time.Minute)
		if to.After(end) {
			to = end
		}
		if !to.After(from) {
			return from
		}
		s.Stages = append(s.Stages, store.SleepStage{Start: from, End: to, Stage: stage})
		m := to.Sub(from).Minutes()
		switch stage {
		case "deep":
			s.Deep += m
		case "light":
			s.Light += m
		case "rem":
			s.REM += m
		case "awake":
			s.Awake += m
		}
		return to
	}
	at := add("light", start, 8+float64(r.IntN(10)))
	for cycle := 0; at.Before(end); cycle++ {
		// Deep sleep dominates early cycles, REM the late ones.
		at = add("deep", at, math.Max(5, 45-float64(cycle)*10+r.NormFloat64()*6))
		at = add("light", at, 20+float64(r.IntN(25)))
		at = add("rem", at, math.Min(45, 10+float64(cycle)*8+r.NormFloat64()*4))
		if r.Float64() < 0.5 {
			at = add("awake", at, 1+float64(r.IntN(6)))
		}
		at = add("light", at, 10+float64(r.IntN(15)))
	}
	s.Asleep = s.Deep + s.Light + s.REM
	s.ToFallAsleep = float64(r.IntN(20))
	// A few brief stirrings during the night.
	for n := 2 + r.IntN(5); n > 0; n-- {
		at := start.Add(time.Duration(r.Float64()*end.Sub(start).Minutes()) * time.Minute)
		s.Restless = append(s.Restless, store.SleepStage{Start: at, End: at.Add(time.Duration(1+r.IntN(3)) * time.Minute), Stage: "restless"})
	}
	sort.Slice(s.Restless, func(i, j int) bool { return s.Restless[i].Start.Before(s.Restless[j].Start) })
	return s
}

// workout generates a session at a typical time of day, its heart rate per
// minute (warm-up, intervals, cool-down) and, for outdoor types, a route.
func workout(r *rand.Rand, day time.Time, act *store.Activity, typ string, gps bool, hrMid float64) store.Workout {
	dur := math.Max(10, act.Duration)
	start := local(day, 17, 0).Add(time.Duration(r.IntN(120)) * time.Minute)
	end := start.Add(time.Duration(dur) * time.Minute)
	w := store.Workout{
		ID: fmt.Sprintf("demo-%s", day.Format("20060102")), UpdatedAt: end, Type: typ, Name: act.Name,
		Start: start, End: end, UTCOffset: demoOffset, Method: "ACTIVELY_MEASURED",
		ActiveSec: dur * 60, Calories: act.Energy, DistanceM: act.Distance, HasGPS: gps, DetailsVersion: store.DetailsVersion,
	}
	var sum float64
	for m := 0; m < int(dur); m++ {
		f := float64(m) / dur
		ramp := math.Min(1, f*6) * math.Min(1, (1-f)*8) // warm-up and cool-down
		hr := 88 + (hrMid-88)*ramp + 9*math.Sin(f*math.Pi*7)*ramp + r.NormFloat64()*3
		hr = math.Round(hr)
		w.HR = append(w.HR, store.HRMinute{Start: start.Add(time.Duration(m) * time.Minute), Avg: hr, Min: hr - 4, Max: hr + 5})
		sum += hr
		switch {
		case hr >= demoZones[2]:
			w.ZoneSec[3] += 60
		case hr >= demoZones[1]:
			w.ZoneSec[2] += 60
		case hr >= demoZones[0]:
			w.ZoneSec[1] += 60
		default:
			w.ZoneSec[0] += 60
		}
	}
	w.AvgHR = math.Round(sum / dur)
	w.ZoneMin = (w.ZoneSec[1] + 2*(w.ZoneSec[2]+w.ZoneSec[3])) / 60
	if typ == "WALKING" || typ == "RUNNING" {
		w.Steps = math.Round(dur * map[bool]float64{true: 165, false: 110}[typ == "RUNNING"])
	}
	if gps && act.Distance > 0 {
		w.Route = route(r, act.Distance, w.HR)
		w.ElevationM = math.Round(act.Distance / 250)
		// One split per full kilometre plus the rest, at a varying pace.
		per := w.ActiveSec / (act.Distance / 1000)
		for left := act.Distance; left > 0; left -= 1000 {
			d := math.Min(1000, left)
			w.Splits = append(w.Splits, store.Split{DistanceM: math.Round(d), ActiveSec: math.Round(per * d / 1000 * (0.92 + r.Float64()*0.16))})
		}
	}
	return w
}

// route is a wobbly loop of the given length (m) through a park, over a
// gentle hill, with the workout's heart rate along the way.
func route(r *rand.Rand, length float64, hr []store.HRMinute) []store.RoutePoint {
	const lat0, lon0 = 51.5073, -0.1657
	radius := length / (2 * math.Pi)
	n := 120
	phase := r.Float64() * 2 * math.Pi
	var pts []store.RoutePoint
	for i := 0; i <= n; i++ {
		a := float64(i) / float64(n) * 2 * math.Pi
		rr := radius * (1 + 0.18*math.Sin(3*a+phase) + 0.07*math.Sin(7*a))
		dy, dx := rr*math.Sin(a), rr*math.Cos(a)*1.4
		f := float64(i) / float64(n)
		pts = append(pts, store.RoutePoint{
			math.Round((lat0+dy/111320)*1e5) / 1e5,
			math.Round((lon0+dx/(111320*math.Cos(lat0*math.Pi/180)))*1e5) / 1e5,
			math.Round(length * f),
			math.Round((24+9*math.Sin(f*math.Pi*2+phase)+3*math.Sin(f*math.Pi*9))*10) / 10,
			hr[min(len(hr)-1, int(f*float64(len(hr))))].Avg,
		})
	}
	return pts
}
