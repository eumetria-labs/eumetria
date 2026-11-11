// Package lifts keeps personal bests in barbell and gymnastics lifts (Power
// Clean, Strict Press, Split Jerk, …) in the app's database. Each entry is
// one result on one day; the page shows a lift's heaviest one as its best
// and the rest as its history. Who may read or write them is the server's
// business (cmd/eumetria, lifts.go).
package lifts

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
	ErrNotFound = errors.New("no such entry")
	ErrInvalid  = errors.New("invalid entry")
)

// The limits of an entry.
const (
	MaxName = 48  // characters of a lift's name
	MaxNote = 200 // characters of a note
	MaxKg   = 500
	MaxReps = 20
)

// Entry is one result. Kg is the weight lifted, for Reps repetitions.
type Entry struct {
	ID        int64     `json:"id"`
	Lift      string    `json:"lift"`
	Kg        float64   `json:"kg"`
	Reps      int       `json:"reps"`
	Date      string    `json:"date"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is what people send for an entry.
type Input struct {
	Lift string  `json:"lift"`
	Kg   float64 `json:"kg"`
	Reps int     `json:"reps"`
	Date string  `json:"date"`
	Note string  `json:"note"`
}

// Clean checks in and returns it tidied: names and notes trimmed with their
// inner white space collapsed, the weight rounded to 10 g, no reps meaning
// one. A date may be at most a day after today (time zones).
func (in Input) Clean(now time.Time) (Input, error) {
	in.Lift = strings.Join(strings.Fields(in.Lift), " ")
	in.Note = strings.Join(strings.Fields(in.Note), " ")
	if in.Lift == "" || utf8.RuneCountInString(in.Lift) > MaxName || hasControl(in.Lift) {
		return in, fmt.Errorf("%w: lift name: 1 to %d characters", ErrInvalid, MaxName)
	}
	if utf8.RuneCountInString(in.Note) > MaxNote || hasControl(in.Note) {
		return in, fmt.Errorf("%w: note: at most %d characters", ErrInvalid, MaxNote)
	}
	if math.IsNaN(in.Kg) || in.Kg <= 0 || in.Kg > MaxKg {
		return in, fmt.Errorf("%w: weight: more than 0 and at most %d kg", ErrInvalid, MaxKg)
	}
	in.Kg = math.Round(in.Kg*100) / 100
	if in.Reps == 0 {
		in.Reps = 1
	}
	if in.Reps < 1 || in.Reps > MaxReps {
		return in, fmt.Errorf("%w: reps: 1 to %d", ErrInvalid, MaxReps)
	}
	day, err := time.Parse(time.DateOnly, in.Date)
	if err != nil || day.Year() < 1950 || day.After(now.UTC().AddDate(0, 0, 1)) {
		return in, fmt.Errorf("%w: date: YYYY-MM-DD, not in the future", ErrInvalid)
	}
	return in, nil
}

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// Store reads and writes the entries.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// An author whose account was deleted is "".
const selectEntry = `SELECT l.id, l.lift, l.kg, l.reps, l.date, l.note, coalesce(c.name, ''), coalesce(u.name, ''), l.created_at, l.updated_at
	FROM lifts l LEFT JOIN users c ON c.id = l.created_by LEFT JOIN users u ON u.id = l.updated_by`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Entry, error) {
	var e Entry
	var created, updated int64
	err := row.Scan(&e.ID, &e.Lift, &e.Kg, &e.Reps, &e.Date, &e.Note, &e.CreatedBy, &e.UpdatedBy, &created, &updated)
	e.CreatedAt, e.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return e, err
}

// List returns every entry, by lift and then the oldest first.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, selectEntry+" ORDER BY l.lift COLLATE NOCASE, l.date, l.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) get(ctx context.Context, id int64) (Entry, error) {
	e, err := scan(s.DB.QueryRowContext(ctx, selectEntry+" WHERE l.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return e, err
}

// Add stores a new entry for the user userID. in must be Clean.
func (s *Store) Add(ctx context.Context, in Input, userID int64) (Entry, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO lifts (lift, kg, reps, date, note, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.Lift, in.Kg, in.Reps, in.Date, in.Note, userID, userID, now, now)
	if err != nil {
		return Entry{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Entry{}, err
	}
	return s.get(ctx, id)
}

// Update changes an entry. in must be Clean.
func (s *Store) Update(ctx context.Context, id int64, in Input, userID int64) (Entry, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE lifts SET lift = ?, kg = ?, reps = ?, date = ?, note = ?, updated_by = ?, updated_at = ?
		WHERE id = ?`, in.Lift, in.Kg, in.Reps, in.Date, in.Note, userID, s.Now().Unix(), id)
	if err != nil {
		return Entry{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Entry{}, err
	}
	return s.get(ctx, id)
}

// Delete removes an entry and returns it as it was.
func (s *Store) Delete(ctx context.Context, id int64) (Entry, error) {
	e, err := s.get(ctx, id)
	if err != nil {
		return e, err
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM lifts WHERE id = ?", id)
	if err != nil {
		return e, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return e, ErrNotFound
	}
	return e, err
}
