package store

// Questions to the synced data in the database, for what needs more than
// the page's statistics (the insights and the coach's tools to come).
// Dates are "YYYY-MM-DD", both ends included.

import (
	"context"
	"database/sql"
)

// DayMetrics is one day's numbers from Yazio and Google Health; a value
// the day doesn't have is zero (Weight and EnergyGoal: nil).
type DayMetrics struct {
	Date                                string
	Energy, Protein, Carb, Fat          float64 // eaten, kcal and g
	EnergyGoal                          *float64
	Steps, ActivityEnergy, Water        float64
	Weight                              *float64 // kg
	RestingHR, HRV                      float64  // bpm, ms
	Asleep, Awake, Deep, Light, REM     float64  // minutes
	Respiratory, SpO2, SkinTemp         float64
	TotalKcal, VO2Max                   float64
	Workouts                            int
	WorkoutMinutes, WorkoutCalories     float64
	WorkoutZoneMinutes, WorkoutDistance float64 // min, m
}

// DailyMetrics lists the days from from to to that have Yazio or Google
// Health data, oldest first.
func DailyMetrics(ctx context.Context, db *sql.DB, from, to string) ([]DayMetrics, error) {
	rows, err := db.QueryContext(ctx, `
		WITH days AS (
			SELECT date FROM yazio_days WHERE date BETWEEN ?1 AND ?2
			UNION SELECT date FROM health_days WHERE date BETWEEN ?1 AND ?2
		), w AS (
			SELECT date, count(*) n, sum(active_sec) / 60.0 minutes, sum(calories) kcal, sum(zone_min) zone, sum(distance_m) dist
			FROM workouts WHERE date BETWEEN ?1 AND ?2 GROUP BY date
		)
		SELECT days.date,
			coalesce(y.energy, 0), coalesce(y.protein, 0), coalesce(y.carb, 0), coalesce(y.fat, 0), y.energy_goal,
			coalesce(y.steps, 0), coalesce(y.activity_energy, 0), coalesce(y.water, 0), y.weight,
			coalesce(h.resting_hr, 0), coalesce(h.hrv, 0), coalesce(h.asleep, 0), coalesce(h.awake, 0), coalesce(h.deep, 0),
			coalesce(h.light, 0), coalesce(h.rem, 0), coalesce(h.respiratory, 0), coalesce(h.spo2, 0), coalesce(h.skin_temp, 0),
			coalesce(h.total_kcal, 0), coalesce(h.vo2max, 0),
			coalesce(w.n, 0), coalesce(w.minutes, 0), coalesce(w.kcal, 0), coalesce(w.zone, 0), coalesce(w.dist, 0)
		FROM days
		LEFT JOIN yazio_days y ON y.date = days.date
		LEFT JOIN health_days h ON h.date = days.date
		LEFT JOIN w ON w.date = days.date
		ORDER BY days.date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayMetrics
	for rows.Next() {
		var d DayMetrics
		var goal, weight sql.NullFloat64
		if err := rows.Scan(&d.Date, &d.Energy, &d.Protein, &d.Carb, &d.Fat, &goal, &d.Steps, &d.ActivityEnergy, &d.Water, &weight,
			&d.RestingHR, &d.HRV, &d.Asleep, &d.Awake, &d.Deep, &d.Light, &d.REM, &d.Respiratory, &d.SpO2, &d.SkinTemp,
			&d.TotalKcal, &d.VO2Max, &d.Workouts, &d.WorkoutMinutes, &d.WorkoutCalories, &d.WorkoutZoneMinutes, &d.WorkoutDistance); err != nil {
			return nil, err
		}
		if goal.Valid {
			d.EnergyGoal = &goal.Float64
		}
		if weight.Valid {
			d.Weight = &weight.Float64
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// EntryRow is one logged food entry.
type EntryRow struct {
	Date, Daytime, Name, Unit  string
	Amount                     float64
	Energy, Protein, Carb, Fat float64
	Fiber, Sugar, Saturated    *float64 // nil when Yazio doesn't know it
	AI                         bool
	RecipeID, EAN              string
}

// Entries lists the food entries from from to to, in the order logged;
// with name, only the entries of that food.
func Entries(ctx context.Context, db *sql.DB, from, to, name string) ([]EntryRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT date, daytime, name, unit, amount, energy, protein, carb, fat, fiber, sugar, saturated, ai, recipe_id, ean
		FROM food_entries WHERE date BETWEEN ? AND ? AND (? = '' OR name = ?)
		ORDER BY date, position`, from, to, name, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryRow
	for rows.Next() {
		var e EntryRow
		var fiber, sugar, saturated sql.NullFloat64
		if err := rows.Scan(&e.Date, &e.Daytime, &e.Name, &e.Unit, &e.Amount, &e.Energy, &e.Protein, &e.Carb, &e.Fat,
			&fiber, &sugar, &saturated, &e.AI, &e.RecipeID, &e.EAN); err != nil {
			return nil, err
		}
		e.Fiber, e.Sugar, e.Saturated = ptr(fiber), ptr(sugar), ptr(saturated)
		out = append(out, e)
	}
	return out, rows.Err()
}

// WorkoutRow is one Fitbit workout's summary.
type WorkoutRow struct {
	ID, Date, Type, Name, Start, End string
	ActiveSec, Calories, AvgHR       float64
	ZoneMinutes, DistanceM           float64
}

// Workouts lists the workouts from from to to, by start.
func Workouts(ctx context.Context, db *sql.DB, from, to string) ([]WorkoutRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, date, type, name, start, end, active_sec, calories, avg_hr, zone_min, distance_m
		FROM workouts WHERE date BETWEEN ? AND ? ORDER BY start`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkoutRow
	for rows.Next() {
		var w WorkoutRow
		if err := rows.Scan(&w.ID, &w.Date, &w.Type, &w.Name, &w.Start, &w.End, &w.ActiveSec, &w.Calories, &w.AvgHR, &w.ZoneMinutes, &w.DistanceM); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}
