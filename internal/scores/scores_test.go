package scores

import (
	"math"
	"testing"
	"time"

	"eumetria/internal/store"
)

func date(k int) string {
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, k).Format(time.DateOnly)
}

// night returns a staged night from 23:00 to 07:00 local (UTC+1) with the
// given minutes asleep and awake, a quarter deep and a fifth REM.
func night(k int, asleep, awake float64) *store.Sleep {
	start := time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC).AddDate(0, 0, k-1)
	return &store.Sleep{Start: start, End: start.Add(8 * time.Hour), UTCOffset: 3600,
		Asleep: asleep, Awake: awake, Deep: asleep / 4, REM: asleep / 5, Light: asleep - asleep/4 - asleep/5}
}

// history is 30 steady days: HRV around 40, resting HR around 60, with a
// small swing so the spread isn't the floor everywhere.
func history() map[string]*store.HealthDay {
	h := map[string]*store.HealthDay{}
	for k := range 30 {
		sw := float64(k%3 - 1) // -1, 0, 1
		h[date(k)] = &store.HealthDay{Date: date(k), HRV: 40 + 3*sw, RestingHR: 60 + sw, SleepHR: 55 + sw, Respiratory: 15,
			SpO2: 97, ZoneMinutes: &[3]float64{20, 0, 0}, Sleep: night(k, 450, 30)}
	}
	return h
}

func TestRecoveryNormalDayScoresAbout80(t *testing.T) {
	h := history()
	h[date(30)] = &store.HealthDay{Date: date(30), HRV: 40, RestingHR: 60, SleepHR: 55, Respiratory: 15}
	s := Compute(h, 0)[date(30)]
	if s == nil || s.Recovery == nil || s.Recovery.Value != 80 {
		t.Fatalf("recovery = %+v", s)
	}
	if len(s.Recovery.Parts) != 4 {
		t.Errorf("parts = %+v", s.Recovery.Parts)
	}
}

func TestRecoveryGoodAndBadDays(t *testing.T) {
	h := history()
	// Spread: MAD of HRV {37,40,43} is 3, × 1.4826 ≈ 4.45 ms.
	h[date(30)] = &store.HealthDay{Date: date(30), HRV: 49, RestingHR: 58}
	good := Compute(h, 0)[date(30)].Recovery
	if good.Value <= 90 {
		t.Errorf("better HRV and lower resting HR: %d", good.Value)
	}
	h[date(30)] = &store.HealthDay{Date: date(30), HRV: 28, RestingHR: 64, SkinTemp: 34.2, SkinBaseline: 33.4}
	d := Compute(h, 0)[date(30)]
	bad := d.Recovery
	if bad.Value >= 60 || len(bad.Flags) != 1 || bad.Flags[0] != "temp" {
		t.Errorf("worse day with fever: %+v", bad)
	}
	if d.Readiness == nil || d.Readiness.Value >= ReadyEasy {
		t.Errorf("readiness with the temperature up: %+v", d.Readiness)
	}
}

func TestRecoveryNeedsABaseline(t *testing.T) {
	h := map[string]*store.HealthDay{}
	for k := range 10 {
		h[date(k)] = &store.HealthDay{Date: date(k), HRV: 40, RestingHR: 60}
	}
	if s := Compute(h, 0)[date(9)]; s != nil && s.Recovery != nil {
		t.Errorf("recovery after 9 days: %+v", s.Recovery)
	}
}

func TestSleepScore(t *testing.T) {
	h := history()
	// 8 h asleep of 8 h 20 in bed, a regular night: everything full except
	// efficiency (96 % ≥ 92 %: full too) → 100.
	h[date(30)] = &store.HealthDay{Date: date(30), Sleep: night(30, 480, 20)}
	if s := Compute(h, 480)[date(30)].Sleep; s.Value != 100 {
		t.Errorf("full night: %+v", s)
	}
	// 6 h of an 8 h goal: duration 25 of 50 points.
	h[date(30)].Sleep = night(30, 360, 20)
	s := Compute(h, 480)[date(30)].Sleep
	if p := s.Parts[0]; p.Key != "duration" || p.Points != 25 {
		t.Errorf("duration part: %+v", p)
	}
	if s.Value != 75 {
		t.Errorf("6 h night: %d", s.Value)
	}
	// Three hours late: no regularity points.
	late := night(30, 480, 20)
	late.Start, late.End = late.Start.Add(3*time.Hour), late.End.Add(3*time.Hour)
	h[date(30)].Sleep = late
	if s := Compute(h, 480)[date(30)].Sleep; s.Value != 80 {
		t.Errorf("late night: %+v", s)
	}
}

func TestSleepWithoutStagesOrHistory(t *testing.T) {
	s := &store.Sleep{Start: time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC), Asleep: 480, Awake: 0}
	h := map[string]*store.HealthDay{date(1): {Date: date(1), Sleep: s}}
	got := Compute(h, 480)[date(1)].Sleep
	// Duration 50 + efficiency 30, no regularity yet: 80 of 80.
	if got.Value != 100 || len(got.Parts) != 2 || got.Parts[1].Max != 30 {
		t.Errorf("no stages, first night: %+v", got)
	}
}

func TestMidpointAcrossMidnight(t *testing.T) {
	// 23:00–07:00 local: middle at 03:00, 15 h after noon.
	if m := midpoint(night(0, 1, 0)); m != 900 {
		t.Errorf("midpoint = %v", m)
	}
}

func TestLoad(t *testing.T) {
	h := map[string]*store.HealthDay{}
	// 10 zone minutes a day for three weeks, then 20.
	for k := range 28 {
		z := 10.0
		if k >= 21 {
			z = 20
		}
		h[date(k)] = &store.HealthDay{Date: date(k), ZoneMinutes: &[3]float64{z, 0, 0}}
	}
	// A worn rest day counts as 0; an unworn one isn't there.
	h[date(28)] = &store.HealthDay{Date: date(28), RestingHR: 60}
	got := Compute(h, 0)
	if got[date(19)] != nil && got[date(19)].Load != nil {
		t.Error("load before 21 worn days")
	}
	if l := got[date(27)].Load; l.Acute != 140 || l.Chronic != 88 || l.Ratio != 1.6 {
		t.Errorf("load = %+v", l)
	}
	if l := got[date(28)].Load; l.Acute != 120 {
		t.Errorf("rest day load = %+v", l)
	}
}

func TestReadiness(t *testing.T) {
	d := &Day{Recovery: &Score{Value: 80}, Sleep: &Score{Value: 90}, Load: &Load{Ratio: 1}}
	if r := readiness(d); r.Value != 87 { // 40 + 27 + 20
		t.Errorf("readiness = %+v", r)
	}
	d.Load = &Load{Ratio: 1.8}
	if r := readiness(d); r.Value != 75 { // 40 + 27 + 8
		t.Errorf("overreaching: %+v", r)
	}
	d.Sleep, d.Load = nil, nil
	if r := readiness(d); r.Value != 80 {
		t.Errorf("recovery alone: %+v", r)
	}
	if math.Abs(loadPoints(0.5)-80) > 1e-9 || loadPoints(0.65) != 90 || loadPoints(1.1) != 100 {
		t.Errorf("loadPoints: %v %v", loadPoints(0.5), loadPoints(0.65))
	}
}
