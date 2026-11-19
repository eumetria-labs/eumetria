// Package phases keeps the phases (a cut, a bulk, …: stretches of days with
// a purpose) and events (one day: a competition, a race) people note on the
// page, so the charts can shade and mark them. Who may read or write them
// is the server's business (cmd/eumetria, phases.go).
package phases

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"eumetria/internal/db"
)

var (
	ErrNotFound = errors.New("no such phase")
	ErrInvalid  = errors.New("invalid phase")
)

// The kinds. Event is one day; the others are phases.
const (
	Cut      = "cut"
	Bulk     = "bulk"
	Maintain = "maintain"
	Other    = "other"
	Event    = "event"
	// Vacation is time off: its days are free days for the chronotype
	// (stats/sleeptiming.go), shaded on the charts like the other phases.
	Vacation = "vacation"
)

var Kinds = []string{Cut, Bulk, Maintain, Other, Event, Vacation}

// The limits of an entry.
const (
	MaxLabel = 48
	MaxNote  = 200
	MaxDays  = 731 // two years
)

// Phase is one phase or event. End is "" while a phase runs; an event's
// End is its Start.
type Phase struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label,omitempty"`
	Start     string    `json:"start"`
	End       string    `json:"end"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is what people send.
type Input struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Start string `json:"start"`
	End   string `json:"end"`
	Note  string `json:"note"`
}

// Clean checks in and returns it tidied: label and note trimmed with their
// inner white space collapsed. "other" and events need a label. An event
// may lie ahead (the competition to train for), up to two years; a phase
// starts at most a day after today (time zones), ends on or after its start
// and lasts at most two years.
func (in Input) Clean(now time.Time) (Input, error) {
	in.Label = strings.Join(strings.Fields(in.Label), " ")
	in.Note = strings.Join(strings.Fields(in.Note), " ")
	if !slices.Contains(Kinds, in.Kind) {
		return in, fmt.Errorf("%w: kind: one of %s", ErrInvalid, strings.Join(Kinds, ", "))
	}
	if utf8.RuneCountInString(in.Label) > MaxLabel || hasControl(in.Label) || (in.Label == "" && (in.Kind == Other || in.Kind == Event)) {
		return in, fmt.Errorf("%w: label: at most %d characters, needed for %s and %s", ErrInvalid, MaxLabel, Other, Event)
	}
	if utf8.RuneCountInString(in.Note) > MaxNote || hasControl(in.Note) {
		return in, fmt.Errorf("%w: note: at most %d characters", ErrInvalid, MaxNote)
	}
	today := now.UTC().Truncate(24 * time.Hour)
	start, err := time.Parse(time.DateOnly, in.Start)
	if err != nil || start.Year() < 1950 {
		return in, fmt.Errorf("%w: start: YYYY-MM-DD", ErrInvalid)
	}
	if in.Kind == Event {
		if start.After(today.AddDate(0, 0, MaxDays)) {
			return in, fmt.Errorf("%w: an event at most two years ahead", ErrInvalid)
		}
		in.End = in.Start
		return in, nil
	}
	// A vacation is planned: it may lie ahead, and it ends.
	if in.Kind == Vacation {
		if start.After(today.AddDate(0, 0, MaxDays)) || in.End == "" {
			return in, fmt.Errorf("%w: a vacation at most two years ahead, with an end", ErrInvalid)
		}
	} else if start.After(today.AddDate(0, 0, 1)) {
		return in, fmt.Errorf("%w: a phase starts today at the latest", ErrInvalid)
	}
	if in.End == "" {
		return in, nil
	}
	end, err := time.Parse(time.DateOnly, in.End)
	if err != nil || end.Before(start) || end.Sub(start) > MaxDays*24*time.Hour {
		return in, fmt.Errorf("%w: end: YYYY-MM-DD, on or after the start, at most two years later", ErrInvalid)
	}
	return in, nil
}

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// Store reads and writes the phases.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// An author whose account was deleted is "".
const selectPhase = `SELECT p.id, p.kind, p.label, p.start_date, p.end_date, p.note, coalesce(c.name, ''), coalesce(u.name, ''), p.created_at, p.updated_at
	FROM phases p LEFT JOIN users c ON c.id = p.created_by LEFT JOIN users u ON u.id = p.updated_by`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Phase, error) {
	var p Phase
	var created, updated int64
	err := row.Scan(&p.ID, &p.Kind, &p.Label, &p.Start, &p.End, &p.Note, &p.CreatedBy, &p.UpdatedBy, &created, &updated)
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return p, err
}

// List returns every phase and event, the earliest first.
func (s *Store) List(ctx context.Context) ([]Phase, error) {
	rows, err := s.DB.QueryContext(ctx, selectPhase+" ORDER BY p.start_date, p.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Phase{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) get(ctx context.Context, id int64) (Phase, error) {
	p, err := scan(s.DB.QueryRowContext(ctx, selectPhase+" WHERE p.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}

// Add stores a new one for the user userID. in must be Clean.
func (s *Store) Add(ctx context.Context, in Input, userID int64) (Phase, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO phases (kind, label, start_date, end_date, note, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.Kind, in.Label, in.Start, in.End, in.Note, userID, userID, now, now)
	if err != nil {
		return Phase{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Phase{}, err
	}
	return s.get(ctx, id)
}

// Update changes one. in must be Clean.
func (s *Store) Update(ctx context.Context, id int64, in Input, userID int64) (Phase, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE phases SET kind = ?, label = ?, start_date = ?, end_date = ?, note = ?, updated_by = ?, updated_at = ?
		WHERE id = ?`, in.Kind, in.Label, in.Start, in.End, in.Note, userID, s.Now().Unix(), id)
	if err != nil {
		return Phase{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return Phase{}, err
	}
	return s.get(ctx, id)
}

// Delete removes one and returns it as it was.
func (s *Store) Delete(ctx context.Context, id int64) (Phase, error) {
	p, err := s.get(ctx, id)
	if err != nil {
		return p, err
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM phases WHERE id = ?", id)
	if err != nil {
		return p, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return p, ErrNotFound
	}
	return p, err
}
