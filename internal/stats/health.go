package stats

import (
	"math"
	"time"

	"eumetria/internal/store"
)

// Sleep is the night that ended on the day. Times are local "HH:MM";
// Stages are [start minute from Start, duration minutes, stage code] with
// codes from sleepStageCode.
type Sleep struct {
	Start  string       `json:"start"`
	End    string       `json:"end"`
	Asleep float64      `json:"asleep"`
	Awake  float64      `json:"awake"`
	Deep   float64      `json:"deep,omitempty"`
	Light  float64      `json:"light,omitempty"`
	REM    float64      `json:"rem,omitempty"`
	Stages [][3]float64 `json:"stages,omitempty"`
	// Restless moments as [start minute, duration minutes] and their total.
	Restless    [][2]float64 `json:"restless,omitempty"`
	RestlessMin float64      `json:"restless_min,omitempty"`
	// Minutes to fall asleep and awake in bed after waking, as Fitbit's
	// restlessness algorithm reports them.
	ToFallAsleep float64 `json:"to_sleep,omitempty"`
	AfterWakeUp  float64 `json:"after_wake,omitempty"`
}

// Workout is a Fitbit workout. Durations are seconds, the start is local
// "HH:MM". HR is [second from start, average bpm] per minute; Zones is the
// time in the light, moderate, vigorous and peak zones; Limits are the lower
// bounds (bpm) of moderate, vigorous and peak that day.
type Workout struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Name     string       `json:"name,omitempty"`
	Start    string       `json:"start"`
	Duration float64      `json:"dur"`
	Elapsed  float64      `json:"elapsed"`
	Energy   float64      `json:"energy,omitempty"`
	AvgHR    float64      `json:"hr_avg,omitempty"`
	MaxHR    float64      `json:"hr_max,omitempty"`
	ZoneMin  float64      `json:"azm,omitempty"`
	Distance float64      `json:"dist,omitempty"` // metres
	Steps    float64      `json:"steps,omitempty"`
	Elev     float64      `json:"elev,omitempty"`
	Zones    [4]float64   `json:"zones"`
	Limits   *[3]float64  `json:"limits,omitempty"`
	HR       [][2]float64 `json:"hr,omitempty"`
	Pauses   [][2]float64 `json:"pauses,omitempty"`
	// Route points are [lat, lon, distance m, altitude m, heart rate bpm];
	// Splits are [distance m, seconds] per kilometre. eumetria moves the
	// route into a file of its own (RouteSrc), so stats.json stays small
	// and the page loads a track only when it shows it.
	Route    []store.RoutePoint `json:"route,omitempty"`
	RouteSrc string             `json:"route_src,omitempty"`
	Splits   [][2]float64       `json:"splits,omitempty"`
}

// Device is the tracker shown in the page header.
type Device struct {
	Name          string    `json:"name"`
	Battery       float64   `json:"battery,omitempty"`
	BatteryStatus string    `json:"battery_status,omitempty"`
	LastSync      time.Time `json:"last_sync,omitzero"`
}

// HealthSync is the state of a failing Google Health sync. The error text
// stays private (it can hold API responses); the page only learns whether a
// new authorization is needed and when the last sync worked.
type HealthSync struct {
	Failing     bool       `json:"failing"`
	Auth        bool       `json:"auth,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
}

func healthSync(st *store.SyncState) *HealthSync {
	if st == nil || st.Error == "" {
		return nil
	}
	out := &HealthSync{Failing: true, Auth: st.Auth}
	if !st.LastSuccess.IsZero() {
		t := st.LastSuccess
		out.LastSuccess = &t
	}
	return out
}

var sleepStageCode = map[string]float64{"awake": 0, "light": 1, "deep": 2, "rem": 3, "asleep": 4, "restless": 5}

// addHealth copies the day's Google Health data into d.
func addHealth(d *Day, hd *store.HealthDay, opt Options) {
	d.RestingHR = hd.RestingHR
	d.FitbitKcal = hd.TotalKcal
	d.ZoneMinutes = hd.ZoneMinutes
	d.HRV = round(hd.HRV, 1)
	d.HRVDeep = round(hd.HRVDeep, 1)
	d.HRVEntropy = round(hd.HRVEntropy, 2)
	d.SleepHR = hd.SleepHR
	d.Respiratory = round(hd.Respiratory, 1)
	d.SpO2 = round(hd.SpO2, 1)
	if hd.SkinTemp != 0 && hd.SkinBaseline != 0 {
		t := round(hd.SkinTemp-hd.SkinBaseline, 1)
		d.SkinTemp = &t
	}
	if s := hd.Sleep; s != nil {
		off := time.Duration(s.UTCOffset) * time.Second
		out := &Sleep{
			Start: clock(s.Start, off), End: clock(s.End, off),
			Asleep: s.Asleep, Awake: s.Awake, Deep: s.Deep, Light: s.Light, REM: s.REM,
		}
		for _, st := range s.Stages {
			code, ok := sleepStageCode[st.Stage]
			if !ok {
				continue
			}
			out.Stages = append(out.Stages, [3]float64{
				math.Round(st.Start.Sub(s.Start).Minutes()), math.Round(st.End.Sub(st.Start).Minutes()), code,
			})
		}
		for _, r := range s.Restless {
			dur := math.Round(r.End.Sub(r.Start).Minutes())
			out.Restless = append(out.Restless, [2]float64{math.Round(r.Start.Sub(s.Start).Minutes()), dur})
			out.RestlessMin += dur
		}
		out.ToFallAsleep, out.AfterWakeUp = s.ToFallAsleep, s.AfterWakeUp
		d.Sleep = out
	}
	for _, w := range hd.Workouts {
		off := time.Duration(w.UTCOffset) * time.Second
		o := Workout{
			ID: w.ID, Type: w.Type, Name: w.Name, Start: clock(w.Start, off),
			Duration: math.Round(w.ActiveSec), Elapsed: math.Round(w.End.Sub(w.Start).Seconds()),
			Energy: round(w.Calories, 0), AvgHR: w.AvgHR, ZoneMin: w.ZoneMin,
			Distance: round(w.DistanceM, 0), Steps: w.Steps, Elev: round(w.ElevationM, 0),
			Limits: hd.Zones,
		}
		for i, z := range w.ZoneSec {
			o.Zones[i] = math.Round(z)
		}
		for _, m := range w.HR {
			o.HR = append(o.HR, [2]float64{math.Round(m.Start.Sub(w.Start).Seconds()), round(m.Avg, 0)})
			o.MaxHR = math.Max(o.MaxHR, m.Max)
		}
		for _, p := range w.Pauses {
			o.Pauses = append(o.Pauses, [2]float64{math.Round(p[0].Sub(w.Start).Seconds()), math.Round(p[1].Sub(w.Start).Seconds())})
		}
		if opt.PublishRoutes {
			o.Route = w.Route
		}
		for _, sp := range w.Splits {
			o.Splits = append(o.Splits, [2]float64{sp.DistanceM, sp.ActiveSec})
		}
		d.Workouts = append(d.Workouts, o)
	}
}

// clock formats t as local "HH:MM" in the zone it was recorded in.
func clock(t time.Time, offset time.Duration) string {
	return t.UTC().Add(offset).Format("15:04")
}
