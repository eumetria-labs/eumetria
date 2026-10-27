package store

import "time"

// HealthDay is one day of Google Health (Fitbit) data. Daily values belong
// to the day they were measured for; the night's sleep belongs to the day it
// ended on.
type HealthDay struct {
	Date      string    `json:"date"`
	FetchedAt time.Time `json:"fetched_at"`

	Sleep     *Sleep  `json:"sleep,omitempty"`
	RestingHR float64 `json:"resting_hr,omitempty"` // bpm
	HRV       float64 `json:"hrv,omitempty"`        // average RMSSD during sleep, ms
	HRVDeep   float64 `json:"hrv_deep,omitempty"`   // RMSSD during deep sleep, ms
	// HRVEntropy is Fitbit's entropy of the night's beat intervals; SleepHR
	// the average heart rate outside REM sleep, bpm.
	HRVEntropy float64 `json:"hrv_entropy,omitempty"`
	SleepHR    float64 `json:"sleep_hr,omitempty"`
	// Breathing rate during sleep (breaths per minute) and blood oxygen (%).
	Respiratory float64 `json:"respiratory,omitempty"`
	SpO2        float64 `json:"spo2,omitempty"`
	SpO2Low     float64 `json:"spo2_low,omitempty"`
	SpO2High    float64 `json:"spo2_high,omitempty"`
	// Skin temperature during sleep and the personal baseline it is compared
	// with, °C.
	SkinTemp     float64 `json:"skin_temp,omitempty"`
	SkinBaseline float64 `json:"skin_baseline,omitempty"`
	// Zones are the lower bounds (bpm) of the moderate, vigorous and peak
	// heart rate zones that day; light is everything below moderate.
	Zones *[3]float64 `json:"zones,omitempty"`
	// TotalKcal is Fitbit's estimate of all calories burned that day,
	// resting metabolism included.
	TotalKcal float64 `json:"total_kcal,omitempty"`
	// ZoneMinutes are the active zone minutes in the fat burn, cardio and
	// peak zones (Fitbit counts cardio and peak minutes double).
	ZoneMinutes *[3]float64 `json:"zone_minutes,omitempty"`

	Workouts []Workout `json:"workouts,omitempty"`
}

type Sleep struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	UTCOffset    int       `json:"utc_offset"` // seconds, at the start
	Asleep       float64   `json:"asleep"`     // minutes
	Awake        float64   `json:"awake"`
	ToFallAsleep float64   `json:"to_fall_asleep,omitempty"`
	AfterWakeUp  float64   `json:"after_wake_up,omitempty"`
	// Minutes per stage; zero for a night without stage tracking.
	Deep  float64 `json:"deep,omitempty"`
	Light float64 `json:"light,omitempty"`
	REM   float64 `json:"rem,omitempty"`
	// Stages is the hypnogram: one segment per stage change.
	Stages []SleepStage `json:"stages,omitempty"`
	// Restless are the short awakenings Fitbit counts as restlessness; they
	// overlap the stages.
	Restless []SleepStage `json:"restless,omitempty"`
}

// SleepStage is a segment of the night; Stage is "awake", "light", "deep",
// "rem", or for nights without stages "asleep" / "restless".
type SleepStage struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Stage string    `json:"stage"`
}

// Workout is a Fitbit exercise session.
type Workout struct {
	ID         string    `json:"id"`
	UpdatedAt  time.Time `json:"updated_at"` // details are re-fetched when it changes
	Type       string    `json:"type"`       // e.g. "BIKING"
	Name       string    `json:"name"`       // as shown in the Fitbit app, may be localized
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	UTCOffset  int       `json:"utc_offset"` // seconds
	Method     string    `json:"method,omitempty"`
	ActiveSec  float64   `json:"active_sec"`
	Calories   float64   `json:"calories,omitempty"`
	AvgHR      float64   `json:"avg_hr,omitempty"`
	ZoneMin    float64   `json:"zone_minutes,omitempty"` // active zone minutes
	DistanceM  float64   `json:"distance_m,omitempty"`
	Steps      float64   `json:"steps,omitempty"`
	ElevationM float64   `json:"elevation_m,omitempty"`
	// ZoneSec is the time in the light, moderate, vigorous and peak zones.
	ZoneSec [4]float64 `json:"zone_sec"`
	// Pauses are [start, end] pairs.
	Pauses [][2]time.Time `json:"pauses,omitempty"`

	// HR is the heart rate per minute; nil until fetched.
	HR []HRMinute `json:"hr,omitempty"`
	// HasGPS marks a recorded route; Route is the simplified track (the
	// full one stays at Google), nil until fetched.
	HasGPS bool         `json:"has_gps,omitempty"`
	Route  []RoutePoint `json:"route,omitempty"`
	// Splits are the per-kilometre (or per-mile) laps of a GPS workout.
	Splits []Split `json:"splits,omitempty"`
	// DetailsVersion is DetailsVersion when HR (and the route) were
	// downloaded; 0 until then, older values are fetched again.
	DetailsVersion int `json:"details_version,omitempty"`
}

// DetailsVersion is bumped when workout details gain fields; workouts
// fetched with an older version download their details again.
const DetailsVersion = 2

// RoutePoint is latitude, longitude, distance from the start (m), altitude
// (m) and heart rate (bpm); altitude and heart rate are 0 when unknown.
type RoutePoint [5]float64

type Split struct {
	DistanceM float64 `json:"distance_m"`
	ActiveSec float64 `json:"active_sec"`
}

// Device is a paired Fitbit tracker or scale.
type Device struct {
	Type          string    `json:"type"` // TRACKER or SCALE
	Name          string    `json:"name"`
	Battery       float64   `json:"battery,omitempty"` // percent
	BatteryStatus string    `json:"battery_status,omitempty"`
	LastSync      time.Time `json:"last_sync,omitzero"`
}

type HRMinute struct {
	Start time.Time `json:"t"`
	Avg   float64   `json:"avg"`
	Min   float64   `json:"min"`
	Max   float64   `json:"max"`
}
