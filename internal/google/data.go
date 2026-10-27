package google

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eumetria/internal/store"
)

// Range queries. Workouts are filtered by civil (local) start date, sleep by
// its end time, daily summaries by their date; daily filters only accept
// ">=" and "<".

// Workouts returns the workouts that started on a local date in [from, to).
func (c *Client) Workouts(ctx context.Context, from, to time.Time) ([]store.Workout, error) {
	f := fmt.Sprintf(`exercise.interval.civil_start_time >= "%s" AND exercise.interval.civil_start_time < "%s"`,
		from.Format(time.DateOnly), to.Format(time.DateOnly))
	raw, err := c.List(ctx, "exercise", "", f, 25)
	if err != nil {
		return nil, fmt.Errorf("workouts: %w", err)
	}
	out := make([]store.Workout, 0, len(raw))
	for _, r := range raw {
		w, err := parseWorkout(r)
		if err != nil {
			return nil, fmt.Errorf("workouts: %w", err)
		}
		out = append(out, w)
	}
	return out, nil
}

type interval struct {
	StartTime      time.Time `json:"startTime"`
	EndTime        time.Time `json:"endTime"`
	StartUTCOffset string    `json:"startUtcOffset"`
}

type exercisePoint struct {
	Name       string `json:"name"`
	DataSource struct {
		RecordingMethod string `json:"recordingMethod"`
	} `json:"dataSource"`
	Exercise struct {
		Interval       interval  `json:"interval"`
		ExerciseType   string    `json:"exerciseType"`
		DisplayName    string    `json:"displayName"`
		ActiveDuration string    `json:"activeDuration"`
		UpdateTime     time.Time `json:"updateTime"`
		MetricsSummary struct {
			CaloriesKcal           float64 `json:"caloriesKcal"`
			DistanceMM             float64 `json:"distanceMillimeters"`
			Steps                  num     `json:"steps"`
			AvgHR                  num     `json:"averageHeartRateBeatsPerMinute"`
			ActiveZoneMinutes      num     `json:"activeZoneMinutes"`
			ElevationGainMM        float64 `json:"elevationGainMillimeters"`
			HeartRateZoneDurations struct {
				Light    string `json:"lightTime"`
				Moderate string `json:"moderateTime"`
				Vigorous string `json:"vigorousTime"`
				Peak     string `json:"peakTime"`
			} `json:"heartRateZoneDurations"`
		} `json:"metricsSummary"`
		ExerciseMetadata struct {
			HasGPS bool `json:"hasGps"`
		} `json:"exerciseMetadata"`
		Splits []struct {
			ActiveDuration string `json:"activeDuration"`
			SplitType      string `json:"splitType"`
			MetricsSummary struct {
				DistanceMM float64 `json:"distanceMillimeters"`
			} `json:"metricsSummary"`
		} `json:"splits"`
		ExerciseEvents []struct {
			EventTime time.Time `json:"eventTime"`
			Type      string    `json:"exerciseEventType"`
		} `json:"exerciseEvents"`
	} `json:"exercise"`
}

func parseWorkout(raw json.RawMessage) (store.Workout, error) {
	var p exercisePoint
	if err := json.Unmarshal(raw, &p); err != nil {
		return store.Workout{}, err
	}
	e, m := p.Exercise, p.Exercise.MetricsSummary
	z := m.HeartRateZoneDurations
	w := store.Workout{
		ID:         p.Name[strings.LastIndex(p.Name, "/")+1:],
		UpdatedAt:  e.UpdateTime,
		Type:       e.ExerciseType,
		Name:       e.DisplayName,
		Start:      e.Interval.StartTime,
		End:        e.Interval.EndTime,
		UTCOffset:  int(seconds(e.Interval.StartUTCOffset)),
		Method:     p.DataSource.RecordingMethod,
		ActiveSec:  seconds(e.ActiveDuration),
		Calories:   m.CaloriesKcal,
		AvgHR:      float64(m.AvgHR),
		ZoneMin:    float64(m.ActiveZoneMinutes),
		DistanceM:  m.DistanceMM / 1000,
		Steps:      float64(m.Steps),
		ElevationM: m.ElevationGainMM / 1000,
		ZoneSec:    [4]float64{seconds(z.Light), seconds(z.Moderate), seconds(z.Vigorous), seconds(z.Peak)},
		HasGPS:     e.ExerciseMetadata.HasGPS,
	}
	for _, sp := range e.Splits {
		if sp.SplitType == "DISTANCE" {
			w.Splits = append(w.Splits, store.Split{DistanceM: math.Round(sp.MetricsSummary.DistanceMM / 1000), ActiveSec: math.Round(seconds(sp.ActiveDuration))})
		}
	}
	var paused *time.Time
	for _, ev := range e.ExerciseEvents {
		switch ev.Type {
		case "PAUSE":
			t := ev.EventTime
			paused = &t
		case "RESUME", "STOP":
			// A pause right before STOP is just the end of the workout.
			if paused != nil && ev.Type == "RESUME" {
				w.Pauses = append(w.Pauses, [2]time.Time{*paused, ev.EventTime})
			}
			paused = nil
		}
	}
	return w, nil
}

// WorkoutHR returns the heart rate per minute during a workout.
func (c *Client) WorkoutHR(ctx context.Context, start, end time.Time) ([]store.HRMinute, error) {
	body := map[string]any{
		"range":      map[string]string{"startTime": start.UTC().Format(time.RFC3339Nano), "endTime": end.UTC().Format(time.RFC3339Nano)},
		"windowSize": "60s",
		"pageSize":   1000,
	}
	var out []store.HRMinute
	for {
		data, err := c.Raw(ctx, http.MethodPost, "/users/me/dataTypes/heart-rate/dataPoints:rollUp", nil, body)
		if err != nil {
			return nil, fmt.Errorf("heart rate: %w", err)
		}
		var page struct {
			Points []struct {
				StartTime time.Time `json:"startTime"`
				HeartRate struct {
					Avg float64 `json:"beatsPerMinuteAvg"`
					Min float64 `json:"beatsPerMinuteMin"`
					Max float64 `json:"beatsPerMinuteMax"`
				} `json:"heartRate"`
			} `json:"rollupDataPoints"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("heart rate: %w", err)
		}
		for _, p := range page.Points {
			if p.HeartRate.Avg == 0 {
				continue // no reading in that minute
			}
			out = append(out, store.HRMinute{
				Start: p.StartTime,
				Avg:   math.Round(p.HeartRate.Avg*10) / 10,
				Min:   p.HeartRate.Min,
				Max:   p.HeartRate.Max,
			})
		}
		if page.NextPageToken == "" {
			break
		}
		body["pageToken"] = page.NextPageToken
	}
	// Windows come newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// WorkoutRoute downloads the workout's GPS track and simplifies it to the
// points needed to draw it, each with its distance, altitude and heart rate.
func (c *Client) WorkoutRoute(ctx context.Context, w store.Workout) ([]store.RoutePoint, error) {
	data, err := c.ExerciseTCX(ctx, "users/me/dataTypes/exercise/dataPoints/"+w.ID)
	if err != nil {
		return nil, fmt.Errorf("route: %w", err)
	}
	pts, err := parseTCX(data)
	if err != nil {
		return nil, fmt.Errorf("route: %w", err)
	}
	return SimplifyRoute(pts, 2), nil
}

func parseTCX(data []byte) ([]store.RoutePoint, error) {
	var doc struct {
		Points []struct {
			Lat  *float64 `xml:"Position>LatitudeDegrees"`
			Lon  *float64 `xml:"Position>LongitudeDegrees"`
			Dist *float64 `xml:"DistanceMeters"`
			Alt  float64  `xml:"AltitudeMeters"`
			HR   float64  `xml:"HeartRateBpm>Value"`
		} `xml:"Activities>Activity>Lap>Track>Trackpoint"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var pts []store.RoutePoint
	var dist float64
	for _, p := range doc.Points {
		if p.Lat == nil || p.Lon == nil {
			continue
		}
		if p.Dist != nil {
			dist = *p.Dist
		} else if n := len(pts); n > 0 {
			dist += haversine(pts[n-1][0], pts[n-1][1], *p.Lat, *p.Lon)
		}
		pts = append(pts, store.RoutePoint{*p.Lat, *p.Lon, dist, p.Alt, p.HR})
	}
	return pts, nil
}

// SimplifyRoute drops points that lie within tolerance metres of the line
// through their neighbours (Ramer–Douglas–Peucker), so the drawn track looks
// the same at a fraction of the size. Coordinates are rounded to ~1 m.
func SimplifyRoute(pts []store.RoutePoint, tolerance float64) []store.RoutePoint {
	if len(pts) < 3 {
		return roundRoute(pts)
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, len(pts) - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		maxD, idx := 0.0, -1
		for i := s.a + 1; i < s.b; i++ {
			if d := crossTrack(pts[i], pts[s.a], pts[s.b]); d > maxD {
				maxD, idx = d, i
			}
		}
		if idx >= 0 && maxD > tolerance {
			keep[idx] = true
			stack = append(stack, span{s.a, idx}, span{idx, s.b})
		}
	}
	var out []store.RoutePoint
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return roundRoute(out)
}

func roundRoute(pts []store.RoutePoint) []store.RoutePoint {
	out := make([]store.RoutePoint, len(pts))
	for i, p := range pts {
		out[i] = store.RoutePoint{math.Round(p[0]*1e5) / 1e5, math.Round(p[1]*1e5) / 1e5, math.Round(p[2]), math.Round(p[3]*10) / 10, p[4]}
	}
	return out
}

// crossTrack is the distance (m) of p from the segment a–b, on a local flat
// projection, which is exact enough for the few hundred metres involved.
func crossTrack(p, a, b store.RoutePoint) float64 {
	const m = 111320.0
	k := math.Cos(a[0] * math.Pi / 180)
	px, py := (p[1]-a[1])*m*k, (p[0]-a[0])*m
	bx, by := (b[1]-a[1])*m*k, (b[0]-a[0])*m
	l2 := bx*bx + by*by
	if l2 == 0 {
		return math.Hypot(px, py)
	}
	t := math.Max(0, math.Min(1, (px*bx+py*by)/l2))
	return math.Hypot(px-t*bx, py-t*by)
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// Sleeps returns the sleep sessions that ended in [from, to). Google
// recommends the reconcile method for sleep (it merges overlapping sources).
func (c *Client) Sleeps(ctx context.Context, from, to time.Time) ([]SleepSession, error) {
	f := fmt.Sprintf(`sleep.interval.end_time >= "%s" AND sleep.interval.end_time < "%s"`,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	raw, err := c.List(ctx, "sleep", ":reconcile", f, 25)
	if err != nil {
		return nil, fmt.Errorf("sleep: %w", err)
	}
	out := make([]SleepSession, 0, len(raw))
	for _, r := range raw {
		var p struct {
			Sleep struct {
				Interval interval `json:"interval"`
				Stages   []struct {
					StartTime time.Time `json:"startTime"`
					EndTime   time.Time `json:"endTime"`
					Type      string    `json:"type"`
				} `json:"stages"`
				ShortAwakenings []struct {
					StartTime time.Time `json:"startTime"`
					EndTime   time.Time `json:"endTime"`
				} `json:"shortAwakenings"`
				Metadata struct {
					MainSleep bool `json:"mainSleep"`
				} `json:"metadata"`
				Summary struct {
					Asleep        num `json:"minutesAsleep"`
					Awake         num `json:"minutesAwake"`
					ToFallAsleep  num `json:"minutesToFallAsleep"`
					AfterWakeUp   num `json:"minutesAfterWakeUp"`
					StagesSummary []struct {
						Type    string `json:"type"`
						Minutes num    `json:"minutes"`
					} `json:"stagesSummary"`
				} `json:"summary"`
			} `json:"sleep"`
		}
		if err := json.Unmarshal(r, &p); err != nil {
			return nil, fmt.Errorf("sleep: %w", err)
		}
		sl, sum := p.Sleep, p.Sleep.Summary
		s := SleepSession{Main: sl.Metadata.MainSleep, Sleep: store.Sleep{
			Start:        sl.Interval.StartTime,
			End:          sl.Interval.EndTime,
			UTCOffset:    int(seconds(sl.Interval.StartUTCOffset)),
			Asleep:       float64(sum.Asleep),
			Awake:        float64(sum.Awake),
			ToFallAsleep: float64(sum.ToFallAsleep),
			AfterWakeUp:  float64(sum.AfterWakeUp),
		}}
		for _, st := range sum.StagesSummary {
			switch st.Type {
			case "DEEP":
				s.Deep = float64(st.Minutes)
			case "LIGHT":
				s.Light = float64(st.Minutes)
			case "REM":
				s.REM = float64(st.Minutes)
			}
		}
		for _, st := range sl.Stages {
			s.Stages = append(s.Stages, store.SleepStage{Start: st.StartTime, End: st.EndTime, Stage: strings.ToLower(st.Type)})
		}
		for _, a := range sl.ShortAwakenings {
			s.Restless = append(s.Restless, store.SleepStage{Start: a.StartTime, End: a.EndTime, Stage: "restless"})
		}
		out = append(out, s)
	}
	return out, nil
}

// SleepSession is a night or a nap.
type SleepSession struct {
	store.Sleep
	Main bool
}

// Daily holds the once-a-day values of one date.
type Daily struct {
	RestingHR, HRV, Respiratory float64
	// HRVDeep is the RMSSD during deep sleep (ms), SleepHR the average heart
	// rate outside REM sleep and HRVEntropy Fitbit's entropy of the beat
	// intervals.
	HRVDeep, HRVEntropy, SleepHR float64
	SpO2, SpO2Low, SpO2High      float64
	SkinTemp, SkinBaseline       float64
	Zones                        *[3]float64
}

// Dailies returns the daily values for the dates in [from, to), keyed by
// date. A data type that fails is reported, the others are still returned.
func (c *Client) Dailies(ctx context.Context, from, to time.Time) (map[string]*Daily, error) {
	out := map[string]*Daily{}
	get := func(date gdate) *Daily {
		k := date.String()
		if out[k] == nil {
			out[k] = &Daily{}
		}
		return out[k]
	}
	var errs []string
	for _, dt := range []string{
		"daily-resting-heart-rate", "daily-heart-rate-variability", "daily-heart-rate-zones",
		"daily-respiratory-rate", "daily-oxygen-saturation", "daily-sleep-temperature-derivations",
	} {
		field := strings.ReplaceAll(dt, "-", "_") + ".date"
		f := fmt.Sprintf(`%s >= "%s" AND %s < "%s"`, field, from.Format(time.DateOnly), field, to.Format(time.DateOnly))
		raw, err := c.List(ctx, dt, "", f, 0)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", dt, err))
			continue
		}
		for _, r := range raw {
			var p struct {
				RHR *struct {
					Date gdate `json:"date"`
					BPM  num   `json:"beatsPerMinute"`
				} `json:"dailyRestingHeartRate"`
				HRV *struct {
					Date    gdate   `json:"date"`
					Avg     float64 `json:"averageHeartRateVariabilityMilliseconds"`
					Deep    float64 `json:"deepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds"`
					Entropy float64 `json:"entropy"`
					NonREM  num     `json:"nonRemHeartRateBeatsPerMinute"`
				} `json:"dailyHeartRateVariability"`
				Zones *struct {
					Date  gdate `json:"date"`
					Zones []struct {
						Type string `json:"heartRateZoneType"`
						Min  num    `json:"minBeatsPerMinute"`
					} `json:"heartRateZones"`
				} `json:"dailyHeartRateZones"`
				Resp *struct {
					Date gdate   `json:"date"`
					BPM  float64 `json:"breathsPerMinute"`
				} `json:"dailyRespiratoryRate"`
				SpO2 *struct {
					Date gdate   `json:"date"`
					Avg  float64 `json:"averagePercentage"`
					Low  float64 `json:"lowerBoundPercentage"`
					High float64 `json:"upperBoundPercentage"`
				} `json:"dailyOxygenSaturation"`
				Temp *struct {
					Date     gdate           `json:"date"`
					Nightly  float64         `json:"nightlyTemperatureCelsius"`
					Baseline FlexibleFloat64 `json:"baselineTemperatureCelsius"`
				} `json:"dailySleepTemperatureDerivations"`
			}
			if err := json.Unmarshal(r, &p); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", dt, err))
				break
			}
			switch {
			case p.RHR != nil:
				get(p.RHR.Date).RestingHR = float64(p.RHR.BPM)
			case p.HRV != nil:
				d := get(p.HRV.Date)
				d.HRV, d.HRVDeep, d.HRVEntropy, d.SleepHR = p.HRV.Avg, p.HRV.Deep, p.HRV.Entropy, float64(p.HRV.NonREM)
			case p.Zones != nil:
				var z [3]float64
				for _, zone := range p.Zones.Zones {
					switch zone.Type {
					case "MODERATE":
						z[0] = float64(zone.Min)
					case "VIGOROUS":
						z[1] = float64(zone.Min)
					case "PEAK":
						z[2] = float64(zone.Min)
					}
				}
				if z[0] > 0 && z[1] > 0 && z[2] > 0 {
					get(p.Zones.Date).Zones = &z
				}
			case p.Resp != nil:
				get(p.Resp.Date).Respiratory = p.Resp.BPM
			case p.SpO2 != nil:
				d := get(p.SpO2.Date)
				d.SpO2, d.SpO2Low, d.SpO2High = p.SpO2.Avg, p.SpO2.Low, p.SpO2.High
			case p.Temp != nil:
				baseline := float64(p.Temp.Baseline)
				d := get(p.Temp.Date)
				d.SkinTemp, d.SkinBaseline = p.Temp.Nightly, baseline
			}
		}
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("daily values: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// Flexible Convert float64
type FlexibleFloat64 float64

func (f *FlexibleFloat64) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)

	if bytes.Equal(data, []byte("null")) {
		*f = 0
		return nil
	}

	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			*f = 0
			return nil
		}
		*f = FlexibleFloat64(n)
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			*f = 0
			return nil
		}

		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("FlexibleFloat64: invalid numeric string %q: %w", s, err)
		}

		if math.IsNaN(n) || math.IsInf(n, 0) {
			*f = 0
			return nil
		}

		*f = FlexibleFloat64(n)
		return nil
	}

	return fmt.Errorf("FlexibleFloat64: expected number or numeric string, got %s", data)
}

// gdate is a google.type.Date.
type gdate struct{ Year, Month, Day int }

func (d gdate) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// num reads an int64, which the API sends as a JSON string ("84"), and also
// accepts a plain number.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	*n = num(f)
	return err
}

// seconds parses a protobuf duration ("1105.931s").
func seconds(d string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSuffix(d, "s"), 64)
	return f
}

// DailyTotals returns Fitbit's total calories burned and active zone minutes
// per day for the dates in [from, to), keyed by date. Total calories may only
// be rolled up 14 days at a time.
func (c *Client) DailyTotals(ctx context.Context, from, to time.Time) (map[string]*Totals, error) {
	out := map[string]*Totals{}
	get := func(d gdate) *Totals {
		k := d.String()
		if out[k] == nil {
			out[k] = &Totals{}
		}
		return out[k]
	}
	var errs []string
	for _, q := range []struct {
		dataType string
		days     int
	}{{"total-calories", 14}, {"active-zone-minutes", 90}} {
		for start := from; start.Before(to); start = start.AddDate(0, 0, q.days) {
			end := start.AddDate(0, 0, q.days)
			if end.After(to) {
				end = to
			}
			points, err := c.dailyRollUp(ctx, q.dataType, start, end)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", q.dataType, err))
				break
			}
			for _, p := range points {
				switch {
				case p.TotalCalories != nil:
					get(p.CivilStartTime.Date).Kcal = math.Round(p.TotalCalories.KcalSum)
				case p.ActiveZoneMinutes != nil:
					a := p.ActiveZoneMinutes
					get(p.CivilStartTime.Date).ZoneMinutes = &[3]float64{float64(a.FatBurn), float64(a.Cardio), float64(a.Peak)}
				}
			}
		}
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("daily totals: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// Totals are the day's rolled-up activity values.
type Totals struct {
	Kcal        float64
	ZoneMinutes *[3]float64 // fat burn, cardio, peak
}

type dailyRollupPoint struct {
	CivilStartTime struct {
		Date gdate `json:"date"`
	} `json:"civilStartTime"`
	TotalCalories *struct {
		KcalSum float64 `json:"kcalSum"`
	} `json:"totalCalories"`
	ActiveZoneMinutes *struct {
		FatBurn num `json:"sumInFatBurnHeartZone"`
		Cardio  num `json:"sumInCardioHeartZone"`
		Peak    num `json:"sumInPeakHeartZone"`
	} `json:"activeZoneMinutes"`
}

func (c *Client) dailyRollUp(ctx context.Context, dataType string, from, to time.Time) ([]dailyRollupPoint, error) {
	civil := func(t time.Time) map[string]any {
		return map[string]any{"date": map[string]int{"year": t.Year(), "month": int(t.Month()), "day": t.Day()}}
	}
	body := map[string]any{"range": map[string]any{"start": civil(from), "end": civil(to)}, "windowSizeDays": 1}
	var out []dailyRollupPoint
	for {
		data, err := c.Raw(ctx, http.MethodPost, "/users/me/dataTypes/"+dataType+"/dataPoints:dailyRollUp", nil, body)
		if err != nil {
			return nil, err
		}
		var page struct {
			Points        []dailyRollupPoint `json:"rollupDataPoints"`
			NextPageToken string             `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Points...)
		if page.NextPageToken == "" {
			return out, nil
		}
		body["pageToken"] = page.NextPageToken
	}
}

// Devices returns the paired trackers and scales.
func (c *Client) Devices(ctx context.Context) ([]store.Device, error) {
	data, err := c.Raw(ctx, http.MethodGet, "/users/me/pairedDevices", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	var res struct {
		Devices []struct {
			DeviceType    string    `json:"deviceType"`
			DeviceVersion string    `json:"deviceVersion"`
			BatteryLevel  float64   `json:"batteryLevel"`
			BatteryStatus string    `json:"batteryStatus"`
			LastSyncTime  time.Time `json:"lastSyncTime"`
		} `json:"pairedDevices"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	var out []store.Device
	for _, d := range res.Devices {
		out = append(out, store.Device{Type: d.DeviceType, Name: d.DeviceVersion, Battery: d.BatteryLevel, BatteryStatus: d.BatteryStatus, LastSync: d.LastSyncTime})
	}
	return out, nil
}
