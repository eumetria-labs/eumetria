package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"eumetria/internal/store"
)

// A workout as the API returned it (2026-09-24 ride), with two of its splits.
const rideJSON = `{
  "name": "users/787/dataTypes/exercise/dataPoints/6762448280088628256",
  "dataSource": {"recordingMethod": "ACTIVELY_MEASURED", "platform": "FITBIT"},
  "exercise": {
    "interval": {"startTime": "2026-09-24T14:32:01Z", "startUtcOffset": "7200s", "endTime": "2026-09-24T15:01:05.980Z", "endUtcOffset": "7200s"},
    "exerciseType": "BIKING",
    "splits": [
      {"startTime": "2026-09-24T14:32:11Z", "activeDuration": "257.767502974s", "metricsSummary": {"distanceMillimeters": 1000000, "averageSpeedMillimetersPerSecond": 3879.46}, "splitType": "DISTANCE"},
      {"startTime": "2026-09-24T14:56:45.890114373Z", "activeDuration": "259.109885627s", "metricsSummary": {"distanceMillimeters": 691039.28759807535}, "splitType": "DISTANCE"}
    ],
    "metricsSummary": {
      "caloriesKcal": 254,
      "distanceMillimeters": 5691039.2875980753,
      "averageSpeedMillimetersPerSecond": 3263.21060068697,
      "averageHeartRateBeatsPerMinute": "83",
      "heartRateZoneDurations": {"lightTime": "1740s", "moderateTime": "0s", "vigorousTime": "0s", "peakTime": "0s"}
    },
    "exerciseMetadata": {"hasGps": true},
    "displayName": "Radfahren",
    "activeDuration": "1744.980s",
    "exerciseEvents": [
      {"eventTime": "2026-09-24T14:32:01Z", "exerciseEventType": "START"},
      {"eventTime": "2026-09-24T14:41:38Z", "exerciseEventType": "PAUSE"},
      {"eventTime": "2026-09-24T14:43:36Z", "exerciseEventType": "RESUME"},
      {"eventTime": "2026-09-24T15:03:06Z", "exerciseEventType": "PAUSE"},
      {"eventTime": "2026-09-24T15:03:07Z", "exerciseEventType": "STOP"}
    ],
    "updateTime": "2026-09-24T16:12:44.012351Z"
  }
}`

func TestParseWorkout(t *testing.T) {
	w, err := parseWorkout(json.RawMessage(rideJSON))
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != "6762448280088628256" || w.Type != "BIKING" || w.Name != "Radfahren" || w.Method != "ACTIVELY_MEASURED" {
		t.Errorf("identity: %+v", w)
	}
	if w.UTCOffset != 7200 || w.ActiveSec != 1744.98 || w.Calories != 254 || w.AvgHR != 83 || !w.HasGPS {
		t.Errorf("metrics: %+v", w)
	}
	if int(w.DistanceM) != 5691 {
		t.Errorf("distance = %v m, want 5691", w.DistanceM)
	}
	if len(w.Splits) != 2 || w.Splits[0] != (store.Split{DistanceM: 1000, ActiveSec: 258}) || w.Splits[1].DistanceM != 691 {
		t.Errorf("splits = %+v", w.Splits)
	}
	if w.ZoneSec != [4]float64{1740, 0, 0, 0} {
		t.Errorf("zones = %v", w.ZoneSec)
	}
	// The pause right before STOP is the end, not a pause.
	if len(w.Pauses) != 1 || w.Pauses[0][1].Sub(w.Pauses[0][0]) != 118*time.Second {
		t.Errorf("pauses = %v, want one of 1m58s", w.Pauses)
	}
}

func TestParseTCXAndSimplify(t *testing.T) {
	data, err := os.ReadFile("testdata/ride.tcx")
	if err != nil {
		t.Fatal(err)
	}
	pts, err := parseTCX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 4 { // the point without a position is skipped
		t.Fatalf("got %d points, want 4: %v", len(pts), pts)
	}
	if pts[3][2] != 44.5 {
		t.Errorf("distance of last point = %v", pts[3][2])
	}
	// The middle point of the straight northward stretch adds nothing; the
	// corner stays, as do both ends.
	got := SimplifyRoute(pts, 2)
	want := []store.RoutePoint{{48, 11, 0, 581, 84}, {48.0002, 11, 22, 0, 0}, {48.0002, 11.0003, 45, 0, 0}}
	if len(got) != len(want) {
		t.Fatalf("simplified = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDailyTotalsAndDevices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Range struct {
				Start struct{ Date gdate } `json:"start"`
			} `json:"range"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/users/me/dataTypes/total-calories/dataPoints:dailyRollUp":
			// Two requests: total calories are rolled up 14 days at a time.
			d := body.Range.Start.Date
			fmt.Fprintf(w, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":%d,"month":%d,"day":%d}},"totalCalories":{"kcalSum":2451.6}}]}`, d.Year, d.Month, d.Day)
		case "/users/me/dataTypes/active-zone-minutes/dataPoints:dailyRollUp":
			w.Write([]byte(`{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":10,"day":1}},"activeZoneMinutes":{"sumInFatBurnHeartZone":"12","sumInCardioHeartZone":"8","sumInPeakHeartZone":"0"}}]}`))
		case "/users/me/pairedDevices":
			w.Write([]byte(`{"pairedDevices":[{"deviceType":"TRACKER","deviceVersion":"Google Fitbit Air","batteryLevel":64,"batteryStatus":"Medium","lastSyncTime":"2026-10-16T08:01:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), tok: &Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got, err := c.DailyTotals(context.Background(), from, from.AddDate(0, 0, 20))
	if err != nil {
		t.Fatal(err)
	}
	if got["2026-10-01"] == nil || got["2026-10-01"].Kcal != 2452 || *got["2026-10-01"].ZoneMinutes != [3]float64{12, 8, 0} {
		t.Errorf("totals = %+v", got["2026-10-01"])
	}
	if got["2026-10-15"] == nil || got["2026-10-15"].Kcal != 2452 {
		t.Error("second 14-day chunk missing")
	}
	devs, err := c.Devices(context.Background())
	if err != nil || len(devs) != 1 || devs[0].Battery != 64 || devs[0].Name != "Google Fitbit Air" || devs[0].LastSync.IsZero() {
		t.Errorf("devices = %+v, %v", devs, err)
	}
}

func TestNum(t *testing.T) {
	var v struct{ A, B, C num }
	if err := json.Unmarshal([]byte(`{"A":"84","B":17.5,"C":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 84 || v.B != 17.5 || v.C != 0 {
		t.Errorf("got %+v", v)
	}
	if seconds("1105.931s") != 1105.931 || seconds("") != 0 {
		t.Error("seconds")
	}
}

// healthServer serves canned responses per data type (shapes from the real API).
func healthServer(t *testing.T) *Client {
	t.Helper()
	responses := map[string]string{
		"/users/me/dataTypes/daily-resting-heart-rate/dataPoints":     `{"dataPoints":[{"dailyRestingHeartRate":{"beatsPerMinute":"62","date":{"day":24,"month":9,"year":2026}}}]}`,
		"/users/me/dataTypes/daily-heart-rate-variability/dataPoints": `{"dataPoints":[{"dailyHeartRateVariability":{"averageHeartRateVariabilityMilliseconds":35.4,"date":{"day":24,"month":9,"year":2026},"deepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds":33.8,"entropy":2.92,"nonRemHeartRateBeatsPerMinute":"54"}}]}`,
		"/users/me/dataTypes/daily-heart-rate-zones/dataPoints": `{"dataPoints":[{"dailyHeartRateZones":{"date":{"day":24,"month":9,"year":2026},"heartRateZones":[
			{"heartRateZoneType":"LIGHT","maxBeatsPerMinute":"108","minBeatsPerMinute":"30"},
			{"heartRateZoneType":"MODERATE","maxBeatsPerMinute":"132","minBeatsPerMinute":"109"},
			{"heartRateZoneType":"VIGOROUS","maxBeatsPerMinute":"162","minBeatsPerMinute":"133"},
			{"heartRateZoneType":"PEAK","maxBeatsPerMinute":"220","minBeatsPerMinute":"163"}]}}]}`,
		"/users/me/dataTypes/daily-respiratory-rate/dataPoints":              `{"dataPoints":[{"dailyRespiratoryRate":{"breathsPerMinute":17.6,"date":{"day":24,"month":9,"year":2026}}}]}`,
		"/users/me/dataTypes/daily-oxygen-saturation/dataPoints":             `{"dataPoints":[{"dailyOxygenSaturation":{"averagePercentage":98.7,"date":{"day":24,"month":9,"year":2026},"lowerBoundPercentage":97.8,"upperBoundPercentage":99.1}}]}`,
		"/users/me/dataTypes/daily-sleep-temperature-derivations/dataPoints": `{"dataPoints":[{"dailySleepTemperatureDerivations":{"baselineTemperatureCelsius":33.43,"date":{"day":24,"month":9,"year":2026},"nightlyTemperatureCelsius":33.01}}]}`,
		"/users/me/dataTypes/sleep/dataPoints:reconcile": `{"dataPoints":[{"dataPointName":"users/787/dataTypes/sleep/dataPoints/1","sleep":{
			"interval":{"startTime":"2026-10-15T20:55:00Z","startUtcOffset":"7200s","endTime":"2026-10-16T02:57:00Z","endUtcOffset":"7200s"},
			"type":"STAGES",
			"stages":[{"startTime":"2026-10-15T20:55:00Z","endTime":"2026-10-15T21:06:00Z","type":"LIGHT"},{"startTime":"2026-10-15T21:06:00Z","endTime":"2026-10-15T21:13:00Z","type":"AWAKE"}],
			"metadata":{"stagesStatus":"SUCCEEDED","processed":true,"mainSleep":true},
			"shortAwakenings":[{"startTime":"2026-10-15T22:57:00Z","endTime":"2026-10-15T22:58:00Z","type":"LIGHT"},{"startTime":"2026-10-16T00:29:00Z","endTime":"2026-10-16T00:32:00Z","type":"LIGHT"}],
			"summary":{"minutesInSleepPeriod":"362","minutesAfterWakeUp":"0","minutesToFallAsleep":"0","minutesAsleep":"355","minutesAwake":"7",
				"stagesSummary":[{"type":"LIGHT","minutes":"198","count":"11"},{"type":"AWAKE","minutes":"7","count":"1"},{"type":"DEEP","minutes":"82","count":"7"},{"type":"REM","minutes":"75","count":"3"}]}}}]}`,
		"/users/me/dataTypes/heart-rate/dataPoints:rollUp": `{"rollupDataPoints":[
			{"startTime":"2026-10-16T03:59:00Z","endTime":"2026-10-16T04:00:00Z","heartRate":{"beatsPerMinuteAvg":82.06896551724138,"beatsPerMinuteMax":89,"beatsPerMinuteMin":79}},
			{"startTime":"2026-10-16T03:58:00Z","endTime":"2026-10-16T03:59:00Z","heartRate":{"beatsPerMinuteAvg":99.13333333333334,"beatsPerMinuteMax":108,"beatsPerMinuteMin":81}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client(), tok: &Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}}
}

func TestDailies(t *testing.T) {
	c := healthServer(t)
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	got, err := c.Dailies(context.Background(), from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	d := got["2026-09-24"]
	if d == nil || d.RestingHR != 62 || d.HRV != 35.4 || d.HRVDeep != 33.8 || d.HRVEntropy != 2.92 || d.SleepHR != 54 || d.Respiratory != 17.6 || d.SpO2 != 98.7 || d.SpO2Low != 97.8 ||
		d.SkinTemp != 33.01 || d.SkinBaseline != 33.43 || d.Zones == nil || *d.Zones != [3]float64{109, 133, 163} {
		t.Errorf("dailies = %+v (zones %v)", d, d.Zones)
	}
}

func TestSleeps(t *testing.T) {
	c := healthServer(t)
	got, err := c.Sleeps(context.Background(), time.Now(), time.Now())
	if err != nil || len(got) != 1 {
		t.Fatalf("Sleeps = %v, %v", got, err)
	}
	s := got[0]
	if !s.Main || s.Asleep != 355 || s.Awake != 7 || s.Deep != 82 || s.Light != 198 || s.REM != 75 || s.UTCOffset != 7200 {
		t.Errorf("sleep = %+v", s)
	}
	if len(s.Restless) != 2 || s.Restless[1].End.Sub(s.Restless[1].Start) != 3*time.Minute {
		t.Errorf("restless = %+v", s.Restless)
	}
	if len(s.Stages) != 2 || s.Stages[1].Stage != "awake" {
		t.Errorf("stages = %+v", s.Stages)
	}
}

func TestWorkoutHR(t *testing.T) {
	c := healthServer(t)
	got, err := c.WorkoutHR(context.Background(), time.Now(), time.Now())
	if err != nil || len(got) != 2 {
		t.Fatalf("WorkoutHR = %v, %v", got, err)
	}
	// Oldest first, averages rounded to 0.1.
	if got[0].Avg != 99.1 || got[0].Max != 108 || got[1].Avg != 82.1 {
		t.Errorf("hr = %+v", got)
	}
}
