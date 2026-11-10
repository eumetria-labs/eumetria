// Package wods keeps benchmark workouts (Fran, Cindy, a WOD the box
// repeats, …) and their results in the app's database. People create a
// workout once, with how it is scored, and note a result each time they do
// it; the page shows the best result as the personal best. Who may read or
// write them is the server's business (cmd/eumetria, wods.go).
package wods

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"eumetria/internal/db"
)

var (
	ErrNotFound   = errors.New("no such workout or result")
	ErrInvalid    = errors.New("invalid workout or result")
	ErrNameTaken  = errors.New("a workout of that name exists")
	ErrHasResults = errors.New("the workout has results: its scoring can't change")
)

// How a workout is scored (see the migration, 0010_wods.sql).
const (
	Time   = "time"   // Value: seconds, the fewer the better
	Rounds = "rounds" // Value: full rounds, Reps: the reps after them
	Reps   = "reps"   // Value: reps
	Load   = "load"   // Value: kg
)

// The limits of a workout and a result.
const (
	MaxName        = 48
	MaxDescription = 1000
	MaxNote        = 200
	MaxSeconds     = 24 * 60 * 60
	MaxRounds      = 999 // also the most reps after the rounds
	MaxReps        = 99999
	MaxKg          = 500
	MaxWorkoutID   = 128 // characters of a Fitbit workout's id
)

// Workout is a benchmark workout. Description is the workout itself, as the
// whiteboard says it ("21-15-9: Thrusters 43 kg, Pull-ups").
type Workout struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Scoring     string    `json:"scoring"`
	TimeCap     int       `json:"time_cap,omitempty"` // seconds; workouts for time only
	Description string    `json:"description,omitempty"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkoutInput is what people send for a workout.
type WorkoutInput struct {
	Name        string `json:"name"`
	Scoring     string `json:"scoring"`
	TimeCap     int    `json:"time_cap"`
	Description string `json:"description"`
}

// Result is one go at a workout.
type Result struct {
	ID      int64   `json:"id"`
	Workout int64   `json:"workout"`
	Value   float64 `json:"value"`
	Reps    int     `json:"reps,omitempty"`
	Scaled  bool    `json:"scaled,omitempty"`
	Capped  bool    `json:"capped,omitempty"` // not finished in the time cap: Value is the reps done
	// The Fitbit workout it was done in, if it is linked to one.
	FitbitWorkout string    `json:"fitbit_workout,omitempty"`
	Date          string    `json:"date"`
	Note          string    `json:"note,omitempty"`
	CreatedBy     string    `json:"created_by"`
	UpdatedBy     string    `json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ResultInput is what people send for a result.
type ResultInput struct {
	Value  float64 `json:"value"`
	Reps   int     `json:"reps"`
	Scaled bool    `json:"scaled"`
	Capped bool    `json:"capped"`
	// A Fitbit workout's id; the server checks it and takes its date.
	FitbitWorkout string `json:"fitbit_workout"`
	Date          string `json:"date"`
	Note          string `json:"note"`
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasControl(s string, newlines bool) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && !(newlines && r == '\n') }) >= 0
}

// Clean checks in and returns it tidied: the name on one line, the
// description with its lines trimmed and at most one empty line in a row.
func (in WorkoutInput) Clean() (WorkoutInput, error) {
	in.Name = oneLine(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > MaxName || hasControl(in.Name, false) {
		return in, fmt.Errorf("%w: name: 1 to %d characters", ErrInvalid, MaxName)
	}
	switch in.Scoring {
	case Time, Rounds, Reps, Load:
	default:
		return in, fmt.Errorf("%w: scoring: time, rounds, reps or load", ErrInvalid)
	}
	if in.Scoring != Time {
		in.TimeCap = 0
	}
	if in.TimeCap < 0 || in.TimeCap > MaxSeconds {
		return in, fmt.Errorf("%w: time cap: up to 24 h", ErrInvalid)
	}
	lines := strings.Split(strings.ReplaceAll(in.Description, "\r\n", "\n"), "\n")
	var kept []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" && (len(kept) == 0 || kept[len(kept)-1] == "") {
			continue
		}
		kept = append(kept, l)
	}
	in.Description = strings.TrimSpace(strings.Join(kept, "\n"))
	if utf8.RuneCountInString(in.Description) > MaxDescription || hasControl(in.Description, true) {
		return in, fmt.Errorf("%w: description: at most %d characters", ErrInvalid, MaxDescription)
	}
	return in, nil
}

// Clean checks a result of a workout scored by scoring. A date may be at
// most a day after today (time zones).
func (in ResultInput) Clean(scoring string, now time.Time) (ResultInput, error) {
	in.Note = oneLine(in.Note)
	if utf8.RuneCountInString(in.Note) > MaxNote || hasControl(in.Note, false) {
		return in, fmt.Errorf("%w: note: at most %d characters", ErrInvalid, MaxNote)
	}
	whole := in.Value == math.Trunc(in.Value)
	if math.IsNaN(in.Value) || math.IsInf(in.Value, 0) {
		return in, fmt.Errorf("%w: no number", ErrInvalid)
	}
	if scoring != Rounds {
		in.Reps = 0
	}
	if scoring != Time {
		in.Capped = false
	}
	in.FitbitWorkout = strings.TrimSpace(in.FitbitWorkout)
	if len(in.FitbitWorkout) > MaxWorkoutID || strings.IndexFunc(in.FitbitWorkout, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return in, fmt.Errorf("%w: Fitbit workout: an id of up to %d characters", ErrInvalid, MaxWorkoutID)
	}
	switch {
	case in.Capped:
		if !whole || in.Value < 0 || in.Value > MaxReps {
			return in, fmt.Errorf("%w: reps done in the time cap: 0 to %d", ErrInvalid, MaxReps)
		}
	case scoring == Time:
		if !whole || in.Value < 1 || in.Value > MaxSeconds {
			return in, fmt.Errorf("%w: time: 1 s to 24 h, in whole seconds", ErrInvalid)
		}
	case scoring == Rounds:
		if !whole || in.Value < 0 || in.Value > MaxRounds || in.Reps < 0 || in.Reps > MaxRounds || in.Value+float64(in.Reps) == 0 {
			return in, fmt.Errorf("%w: rounds and reps: 0 to %d each, not both 0", ErrInvalid, MaxRounds)
		}
	case scoring == Reps:
		if !whole || in.Value < 1 || in.Value > MaxReps {
			return in, fmt.Errorf("%w: reps: 1 to %d", ErrInvalid, MaxReps)
		}
	case scoring == Load:
		if in.Value <= 0 || in.Value > MaxKg {
			return in, fmt.Errorf("%w: weight: more than 0 and at most %d kg", ErrInvalid, MaxKg)
		}
		in.Value = math.Round(in.Value*100) / 100
	default:
		return in, fmt.Errorf("%w: unknown scoring %q", ErrInvalid, scoring)
	}
	day, err := time.Parse(time.DateOnly, in.Date)
	if err != nil || day.Year() < 1950 || day.After(now.UTC().AddDate(0, 0, 1)) {
		return in, fmt.Errorf("%w: date: YYYY-MM-DD, not in the future", ErrInvalid)
	}
	return in, nil
}

// Better reports whether result a beats b for a workout scored by scoring.
// A finished time beats every capped result; capped ones count their reps.
func Better(scoring string, a, b Result) bool {
	switch scoring {
	case Time:
		if a.Capped != b.Capped {
			return !a.Capped
		}
		if a.Capped {
			return a.Value > b.Value
		}
		return a.Value < b.Value
	case Rounds:
		return a.Value > b.Value || a.Value == b.Value && a.Reps > b.Reps
	default:
		return a.Value > b.Value
	}
}

// Format writes a result as people say it: "4:12", "capped, 180 reps",
// "12 rounds + 5 reps", "150 reps", "80 kg".
func Format(scoring string, r Result) string {
	switch {
	case r.Capped:
		return fmt.Sprintf("capped, %g reps", r.Value)
	case scoring == Time:
		s := int(r.Value)
		if s >= 3600 {
			return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
		}
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	case scoring == Rounds:
		if r.Reps == 0 {
			return fmt.Sprintf("%g rounds", r.Value)
		}
		return fmt.Sprintf("%g rounds + %d reps", r.Value, r.Reps)
	case scoring == Reps:
		return fmt.Sprintf("%g reps", r.Value)
	default:
		return fmt.Sprintf("%g kg", r.Value)
	}
}

// Store reads and writes workouts and results.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

type scanner interface{ Scan(...any) error }

const selectWorkout = `SELECT w.id, w.name, w.scoring, w.time_cap, w.description, c.name, u.name, w.created_at, w.updated_at
	FROM wods w JOIN users c ON c.id = w.created_by JOIN users u ON u.id = w.updated_by`

func scanWorkout(row scanner) (Workout, error) {
	var w Workout
	var created, updated int64
	err := row.Scan(&w.ID, &w.Name, &w.Scoring, &w.TimeCap, &w.Description, &w.CreatedBy, &w.UpdatedBy, &created, &updated)
	w.CreatedAt, w.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return w, err
}

const selectResult = `SELECT r.id, r.wod_id, r.value, r.reps, r.scaled, r.capped, r.fitbit_workout, r.date, r.note, c.name, u.name, r.created_at, r.updated_at
	FROM wod_results r JOIN users c ON c.id = r.created_by JOIN users u ON u.id = r.updated_by`

func scanResult(row scanner) (Result, error) {
	var r Result
	var created, updated int64
	err := row.Scan(&r.ID, &r.Workout, &r.Value, &r.Reps, &r.Scaled, &r.Capped, &r.FitbitWorkout, &r.Date, &r.Note, &r.CreatedBy, &r.UpdatedBy, &created, &updated)
	r.CreatedAt, r.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return r, err
}

func list[T any](ctx context.Context, d *db.DB, query string, scan func(scanner) (T, error)) ([]T, error) {
	rows, err := d.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Workouts returns every workout by name.
func (s *Store) Workouts(ctx context.Context) ([]Workout, error) {
	return list(ctx, s.DB, selectWorkout+" ORDER BY w.name COLLATE NOCASE", scanWorkout)
}

// Results returns every result, the oldest first.
func (s *Store) Results(ctx context.Context) ([]Result, error) {
	return list(ctx, s.DB, selectResult+" ORDER BY r.date, r.id", scanResult)
}

// Workout returns one workout.
func (s *Store) Workout(ctx context.Context, id int64) (Workout, error) {
	w, err := scanWorkout(s.DB.QueryRowContext(ctx, selectWorkout+" WHERE w.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return w, err
}

func (s *Store) result(ctx context.Context, id int64) (Result, error) {
	r, err := scanResult(s.DB.QueryRowContext(ctx, selectResult+" WHERE r.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

func unique(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrNameTaken
	}
	return err
}

// AddWorkout stores a new workout. in must be Clean.
func (s *Store) AddWorkout(ctx context.Context, in WorkoutInput, userID int64) (Workout, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO wods (name, scoring, time_cap, description, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, in.Name, in.Scoring, in.TimeCap, in.Description, userID, userID, now, now)
	if err != nil {
		return Workout{}, unique(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Workout{}, err
	}
	return s.Workout(ctx, id)
}

// UpdateWorkout changes a workout. Its scoring stays once it has results,
// which it would turn into other numbers. in must be Clean.
func (s *Store) UpdateWorkout(ctx context.Context, id int64, in WorkoutInput, userID int64) (Workout, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workout{}, err
	}
	defer tx.Rollback()
	var scoring string
	if err := tx.QueryRowContext(ctx, "SELECT scoring FROM wods WHERE id = ?", id).Scan(&scoring); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return Workout{}, err
	}
	if scoring != in.Scoring {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM wod_results WHERE wod_id = ?", id).Scan(&n); err != nil {
			return Workout{}, err
		}
		if n > 0 {
			return Workout{}, ErrHasResults
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE wods SET name = ?, scoring = ?, time_cap = ?, description = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
		in.Name, in.Scoring, in.TimeCap, in.Description, userID, s.Now().Unix(), id); err != nil {
		return Workout{}, unique(err)
	}
	if err := tx.Commit(); err != nil {
		return Workout{}, err
	}
	return s.Workout(ctx, id)
}

// DeleteWorkout removes a workout with its results and returns it as it
// was, with the number of results.
func (s *Store) DeleteWorkout(ctx context.Context, id int64) (Workout, int, error) {
	w, err := s.Workout(ctx, id)
	if err != nil {
		return w, 0, err
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM wod_results WHERE wod_id = ?", id).Scan(&n); err != nil {
		return w, 0, err
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM wods WHERE id = ?", id)
	if err != nil {
		return w, 0, err
	}
	if k, err := res.RowsAffected(); err == nil && k == 0 {
		return w, 0, ErrNotFound
	}
	return w, n, err
}

// AddResult stores a result of the workout wod. in must be Clean for its
// scoring.
func (s *Store) AddResult(ctx context.Context, wod int64, in ResultInput, userID int64) (Result, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO wod_results (wod_id, value, reps, scaled, capped, fitbit_workout, date, note, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, wod, in.Value, in.Reps, in.Scaled, in.Capped, in.FitbitWorkout, in.Date, in.Note, userID, userID, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			err = ErrNotFound
		}
		return Result{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Result{}, err
	}
	return s.result(ctx, id)
}

// ResultWorkout returns the workout of a result.
func (s *Store) ResultWorkout(ctx context.Context, id int64) (Workout, error) {
	var wod int64
	err := s.DB.QueryRowContext(ctx, "SELECT wod_id FROM wod_results WHERE id = ?", id).Scan(&wod)
	if errors.Is(err, sql.ErrNoRows) {
		return Workout{}, ErrNotFound
	}
	if err != nil {
		return Workout{}, err
	}
	return s.Workout(ctx, wod)
}

// UpdateResult changes a result. in must be Clean for its workout's scoring.
func (s *Store) UpdateResult(ctx context.Context, id int64, in ResultInput, userID int64) (Result, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE wod_results SET value = ?, reps = ?, scaled = ?, capped = ?, fitbit_workout = ?, date = ?, note = ?, updated_by = ?, updated_at = ?
		WHERE id = ?`, in.Value, in.Reps, in.Scaled, in.Capped, in.FitbitWorkout, in.Date, in.Note, userID, s.Now().Unix(), id)
	if err != nil {
		return Result{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Result{}, err
	}
	return s.result(ctx, id)
}

// DeleteResult removes a result and returns it as it was.
func (s *Store) DeleteResult(ctx context.Context, id int64) (Result, error) {
	r, err := s.result(ctx, id)
	if err != nil {
		return r, err
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM wod_results WHERE id = ?", id)
	if err != nil {
		return r, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return r, ErrNotFound
	}
	return r, err
}
