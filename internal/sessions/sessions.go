// Package sessions keeps training sessions: what was done, set by set
// (lift, warm-up or work, reps × kg, RPE). Optional: personal bests
// (internal/lifts) and benchmark results stay as they are. A work set that
// beats a lift's best can be saved as a best along with the session; the
// best then links to the session, and stays when the session goes. Who may
// read or write them is the server's business (cmd/eumetria, sessions.go).
package sessions

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
	"eumetria/internal/lifts"
)

var (
	ErrNotFound = errors.New("no such session")
	ErrInvalid  = errors.New("invalid session")
)

// The limits of a session.
const (
	MaxSets  = 80
	MaxReps  = 100
	MaxTitle = 48
	MaxNote  = 500
	MaxKg    = lifts.MaxKg
)

const (
	Warmup = "warmup"
	Work   = "work"
)

// Set is one set: Kg 0 is bodyweight.
type Set struct {
	Lift string   `json:"lift"`
	Type string   `json:"type"`
	Reps int      `json:"reps"`
	Kg   float64  `json:"kg"`
	RPE  *float64 `json:"rpe,omitempty"`
	Note string   `json:"note,omitempty"`
}

// Session is one session with its sets in order, and the bests saved from
// it (lift entry ids).
type Session struct {
	ID            int64     `json:"id"`
	Date          string    `json:"date"`
	FitbitWorkout string    `json:"fitbit_workout,omitempty"`
	Title         string    `json:"title,omitempty"`
	Note          string    `json:"note,omitempty"`
	RPE           *int      `json:"rpe,omitempty"`
	Sets          []Set     `json:"sets"`
	Bests         []int64   `json:"bests,omitempty"`
	CreatedBy     string    `json:"created_by"`
	UpdatedBy     string    `json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Input is what people send. Bests are indexes into Sets: work sets to save
// as personal bests too.
type Input struct {
	Date          string `json:"date"`
	FitbitWorkout string `json:"fitbit_workout"`
	Title         string `json:"title"`
	Note          string `json:"note"`
	RPE           *int   `json:"rpe"`
	Sets          []Set  `json:"sets"`
	Bests         []int  `json:"bests"`
}

func tidy(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// Clean checks in and returns it tidied: names and notes trimmed, weights
// rounded to 10 g, the set type work unless warm-up, each best a work set
// once. A date may be at most a day after today (time zones).
func (in Input) Clean(now time.Time) (Input, error) {
	bad := func(what string) (Input, error) { return in, fmt.Errorf("%w: %s", ErrInvalid, what) }
	in.Title, in.Note = tidy(in.Title), strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Title) > MaxTitle || hasControl(in.Title) {
		return bad(fmt.Sprintf("title: at most %d characters", MaxTitle))
	}
	if utf8.RuneCountInString(in.Note) > MaxNote || strings.ContainsFunc(in.Note, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		return bad(fmt.Sprintf("note: at most %d characters", MaxNote))
	}
	if in.RPE != nil && (*in.RPE < 1 || *in.RPE > 10) {
		return bad("RPE: 1 to 10")
	}
	day, err := time.Parse(time.DateOnly, in.Date)
	if err != nil || day.Year() < 1950 || day.After(now.UTC().AddDate(0, 0, 1)) {
		return bad("date: YYYY-MM-DD, not in the future")
	}
	in.FitbitWorkout = strings.TrimSpace(in.FitbitWorkout)
	if len(in.FitbitWorkout) > lifts.MaxWorkoutID || strings.ContainsFunc(in.FitbitWorkout, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return bad("Fitbit workout: an id")
	}
	if len(in.Sets) == 0 || len(in.Sets) > MaxSets {
		return bad(fmt.Sprintf("sets: 1 to %d", MaxSets))
	}
	for i := range in.Sets {
		s := &in.Sets[i]
		s.Lift, s.Note = tidy(s.Lift), tidy(s.Note)
		if s.Lift == "" || utf8.RuneCountInString(s.Lift) > lifts.MaxName || hasControl(s.Lift) {
			return bad(fmt.Sprintf("set %d: a lift name of 1 to %d characters", i+1, lifts.MaxName))
		}
		if s.Type != Warmup {
			s.Type = Work
		}
		if s.Reps < 1 || s.Reps > MaxReps {
			return bad(fmt.Sprintf("set %d: reps 1 to %d", i+1, MaxReps))
		}
		if math.IsNaN(s.Kg) || s.Kg < 0 || s.Kg > MaxKg {
			return bad(fmt.Sprintf("set %d: weight 0 to %d kg", i+1, MaxKg))
		}
		s.Kg = math.Round(s.Kg*100) / 100
		if s.RPE != nil && (*s.RPE < 1 || *s.RPE > 10) {
			return bad(fmt.Sprintf("set %d: RPE 1 to 10", i+1))
		}
		if utf8.RuneCountInString(s.Note) > lifts.MaxNote || hasControl(s.Note) {
			return bad(fmt.Sprintf("set %d: a note of at most %d characters", i+1, lifts.MaxNote))
		}
	}
	var bests []int
	for _, b := range in.Bests {
		if b < 0 || b >= len(in.Sets) || in.Sets[b].Type != Work || in.Sets[b].Kg <= 0 || in.Sets[b].Reps > lifts.MaxReps {
			return bad("bests: work sets with a weight and up to 20 reps")
		}
		dup := false
		for _, x := range bests {
			dup = dup || x == b
		}
		if !dup {
			bests = append(bests, b)
		}
	}
	in.Bests = bests
	return in, nil
}

// Store reads and writes the sessions.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// List returns every session, the oldest first, with its sets.
func (s *Store) List(ctx context.Context) ([]Session, error) {
	return s.query(ctx, "")
}

func (s *Store) get(ctx context.Context, id int64) (Session, error) {
	list, err := s.query(ctx, " WHERE s.id = ?", id)
	if err != nil {
		return Session{}, err
	}
	if len(list) == 0 {
		return Session{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, s.date, s.fitbit_workout, s.title, s.note, s.rpe, coalesce(c.name, ''), coalesce(u.name, ''), s.created_at, s.updated_at
		FROM training_sessions s LEFT JOIN users c ON c.id = s.created_by LEFT JOIN users u ON u.id = s.updated_by`+where+` ORDER BY s.date, s.id`, args...)
	if err != nil {
		return nil, err
	}
	out := []Session{}
	byID := map[int64]int{}
	for rows.Next() {
		var x Session
		var rpe sql.NullInt64
		var created, updated int64
		if err := rows.Scan(&x.ID, &x.Date, &x.FitbitWorkout, &x.Title, &x.Note, &rpe, &x.CreatedBy, &x.UpdatedBy, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		if rpe.Valid {
			v := int(rpe.Int64)
			x.RPE = &v
		}
		x.CreatedAt, x.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		x.Sets = []Set{}
		byID[x.ID] = len(out)
		out = append(out, x)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	filter := ""
	if where != "" {
		filter = " WHERE session_id = ?"
	}
	sets, err := s.DB.QueryContext(ctx, `SELECT session_id, lift, set_type, reps, kg, rpe, note FROM training_sets`+filter+` ORDER BY session_id, position`, args...)
	if err != nil {
		return nil, err
	}
	for sets.Next() {
		var id int64
		var st Set
		var rpe sql.NullFloat64
		if err := sets.Scan(&id, &st.Lift, &st.Type, &st.Reps, &st.Kg, &rpe, &st.Note); err != nil {
			sets.Close()
			return nil, err
		}
		if rpe.Valid {
			v := rpe.Float64
			st.RPE = &v
		}
		if i, ok := byID[id]; ok {
			out[i].Sets = append(out[i].Sets, st)
		}
	}
	if err := sets.Close(); err != nil {
		return nil, err
	}
	bests, err := s.DB.QueryContext(ctx, `SELECT session_id, id FROM lifts WHERE session_id IS NOT NULL`+strings.Replace(filter, "WHERE", "AND", 1)+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer bests.Close()
	for bests.Next() {
		var sid, lid int64
		if err := bests.Scan(&sid, &lid); err != nil {
			return nil, err
		}
		if i, ok := byID[sid]; ok {
			out[i].Bests = append(out[i].Bests, lid)
		}
	}
	return out, bests.Err()
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// writeSets replaces a session's sets, and saves its bests as lift entries
// linked to it.
func (s *Store) writeSets(ctx context.Context, tx *sql.Tx, id int64, in Input, userID, now int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM training_sets WHERE session_id = ?`, id); err != nil {
		return err
	}
	for i, st := range in.Sets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO training_sets (session_id, position, lift, set_type, reps, kg, rpe, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, i, st.Lift, st.Type, st.Reps, st.Kg, nullFloat(st.RPE), st.Note); err != nil {
			return err
		}
	}
	for _, b := range in.Bests {
		st := in.Sets[b]
		if _, err := tx.ExecContext(ctx, `INSERT INTO lifts (lift, kg, reps, date, note, fitbit_workout, session_id, created_by, updated_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, st.Lift, st.Kg, st.Reps, in.Date, st.Note, in.FitbitWorkout, id, userID, userID, now, now); err != nil {
			return err
		}
	}
	return nil
}

// Add stores a new session (in from Clean) for the user userID.
func (s *Store) Add(ctx context.Context, in Input, userID int64) (Session, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO training_sessions (date, fitbit_workout, title, note, rpe, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.Date, in.FitbitWorkout, in.Title, in.Note, nullInt(in.RPE), userID, userID, now, now)
	if err != nil {
		return Session{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Session{}, err
	}
	if err := s.writeSets(ctx, tx, id, in, userID, now); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return s.get(ctx, id)
}

// Update replaces a session's fields and sets (in from Clean); bests saved
// before stay, new ones are added.
func (s *Store) Update(ctx context.Context, id int64, in Input, userID int64) (Session, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE training_sessions SET date = ?, fitbit_workout = ?, title = ?, note = ?, rpe = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
		in.Date, in.FitbitWorkout, in.Title, in.Note, nullInt(in.RPE), userID, now, id)
	if err != nil {
		return Session{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Session{}, err
	}
	if err := s.writeSets(ctx, tx, id, in, userID, now); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return s.get(ctx, id)
}

// Delete removes a session and its sets and returns it as it was; the bests
// saved from it stay, without the link.
func (s *Store) Delete(ctx context.Context, id int64) (Session, error) {
	x, err := s.get(ctx, id)
	if err != nil {
		return x, err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM training_sessions WHERE id = ?`, id)
	if err != nil {
		return x, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return x, ErrNotFound
	}
	return x, err
}

// Volume is a session's work: sets, reps and kg × reps of the work sets
// (warm-ups left out), per lift.
type Volume struct {
	Sets int     `json:"sets"`
	Reps int     `json:"reps"`
	Kg   float64 `json:"kg"`
}

// Volumes sums the work sets of a session per lift.
func (x Session) Volumes() map[string]Volume {
	out := map[string]Volume{}
	for _, st := range x.Sets {
		if st.Type != Work {
			continue
		}
		v := out[st.Lift]
		v.Sets++
		v.Reps += st.Reps
		v.Kg += st.Kg * float64(st.Reps)
		out[st.Lift] = v
	}
	return out
}
