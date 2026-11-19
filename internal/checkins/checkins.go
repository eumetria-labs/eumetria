// Package checkins keeps the daily check-in (how the day felt: energy,
// soreness, stress and mood from 1 to 5, and a short note) and the day's
// tags (sick, travel, alcohol, …) that explain an unusual day. Who may read
// or write them is the server's business (cmd/eumetria, checkins.go).
package checkins

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

var ErrInvalid = errors.New("invalid check-in")

// Tags are the tags a day can have: a fixed list, so patterns stay
// comparable. Auto are those the page also sets by itself (alcohol from the
// foods logged); removing one keeps it away (Dismissed).
var (
	Tags = []string{"alcohol", "sick", "travel", "day_off", "late_meal", "caffeine_late", "work_stress", "bad_room", "injury"}
	Auto = []string{"alcohol"}
	// Skip are the tags that keep a day out of the scores' baselines.
	Skip = []string{"sick", "travel"}
)

const MaxNote = 200

// Checkin is one day's check-in and tags. A value not given is nil.
type Checkin struct {
	Date      string    `json:"date"`
	Energy    *int      `json:"energy,omitempty"`
	Soreness  *int      `json:"soreness,omitempty"`
	Stress    *int      `json:"stress,omitempty"`
	Mood      *int      `json:"mood,omitempty"`
	Note      string    `json:"note,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Dismissed []string  `json:"dismissed,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is what people send for a day.
type Input struct {
	Energy    *int     `json:"energy"`
	Soreness  *int     `json:"soreness"`
	Stress    *int     `json:"stress"`
	Mood      *int     `json:"mood"`
	Note      string   `json:"note"`
	Tags      []string `json:"tags"`
	Dismissed []string `json:"dismissed"`
}

// Clean checks in and returns it tidied: values from 1 to 5, the note's
// white space collapsed, known tags once each in the list's order, and only
// automatic tags dismissed (and not also set).
func (in Input) Clean() (Input, error) {
	for _, v := range []*int{in.Energy, in.Soreness, in.Stress, in.Mood} {
		if v != nil && (*v < 1 || *v > 5) {
			return in, fmt.Errorf("%w: values from 1 to 5", ErrInvalid)
		}
	}
	in.Note = strings.Join(strings.Fields(in.Note), " ")
	if utf8.RuneCountInString(in.Note) > MaxNote || strings.ContainsFunc(in.Note, unicode.IsControl) {
		return in, fmt.Errorf("%w: note: at most %d characters", ErrInvalid, MaxNote)
	}
	pick := func(list, allowed []string) ([]string, error) {
		var out []string
		for _, t := range allowed {
			if slices.Contains(list, t) {
				out = append(out, t)
			}
		}
		for _, t := range list {
			if !slices.Contains(allowed, t) {
				return nil, fmt.Errorf("%w: no tag %q", ErrInvalid, t)
			}
		}
		return out, nil
	}
	var err error
	if in.Tags, err = pick(in.Tags, Tags); err != nil {
		return in, err
	}
	if in.Dismissed, err = pick(in.Dismissed, Auto); err != nil {
		return in, err
	}
	in.Dismissed = slices.DeleteFunc(in.Dismissed, func(t string) bool { return slices.Contains(in.Tags, t) })
	return in, nil
}

// ValidDate reports whether s is a day ("YYYY-MM-DD").
func ValidDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// List returns the check-ins from from to to (both included), oldest first.
func (s *Store) List(ctx context.Context, from, to string) ([]Checkin, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.date, c.energy, c.soreness, c.stress, c.mood, c.note, coalesce(u.name, ''), c.updated_at
		FROM checkins c LEFT JOIN users u ON u.id = c.updated_by WHERE c.date BETWEEN ? AND ? ORDER BY c.date`, from, to)
	if err != nil {
		return nil, err
	}
	var out []Checkin
	byDate := map[string]int{}
	for rows.Next() {
		var c Checkin
		var e, so, st, m sql.NullInt64
		var at int64
		if err := rows.Scan(&c.Date, &e, &so, &st, &m, &c.Note, &c.UpdatedBy, &at); err != nil {
			rows.Close()
			return nil, err
		}
		c.Energy, c.Soreness, c.Stress, c.Mood = intPtr(e), intPtr(so), intPtr(st), intPtr(m)
		c.UpdatedAt = time.Unix(at, 0).UTC()
		byDate[c.Date] = len(out)
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	tags, err := s.DB.QueryContext(ctx, `SELECT date, tag, source FROM day_tags WHERE date BETWEEN ? AND ? ORDER BY date, tag`, from, to)
	if err != nil {
		return nil, err
	}
	defer tags.Close()
	for tags.Next() {
		var date, tag, source string
		if err := tags.Scan(&date, &tag, &source); err != nil {
			return nil, err
		}
		i, ok := byDate[date]
		if !ok {
			continue // tags are written with their check-in
		}
		if source == "dismissed" {
			out[i].Dismissed = append(out[i].Dismissed, tag)
		} else {
			out[i].Tags = append(out[i].Tags, tag)
		}
	}
	return out, tags.Err()
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// Put stores date's check-in and tags (in from Clean), replacing what the
// day had.
func (s *Store) Put(ctx context.Context, date string, in Input, userID int64) (Checkin, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Checkin{}, err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO checkins (date, energy, soreness, stress, mood, note, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (date) DO UPDATE SET energy = excluded.energy, soreness = excluded.soreness, stress = excluded.stress,
			mood = excluded.mood, note = excluded.note, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		date, nullInt(in.Energy), nullInt(in.Soreness), nullInt(in.Stress), nullInt(in.Mood), in.Note, userID, userID, now, now); err != nil {
		return Checkin{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM day_tags WHERE date = ?`, date); err != nil {
		return Checkin{}, err
	}
	for source, list := range map[string][]string{"user": in.Tags, "dismissed": in.Dismissed} {
		for _, t := range list {
			if _, err := tx.ExecContext(ctx, `INSERT INTO day_tags (date, tag, source) VALUES (?, ?, ?)`, date, t, source); err != nil {
				return Checkin{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Checkin{}, err
	}
	list, err := s.List(ctx, date, date)
	if err != nil || len(list) == 0 {
		return Checkin{}, err
	}
	return list[0], nil
}

// Delete removes date's check-in and tags; false when it had none.
func (s *Store) Delete(ctx context.Context, date string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM checkins WHERE date = ?`, date)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM day_tags WHERE date = ?`, date); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, tx.Commit()
}

// SkipDays are the days tagged sick or travel: out of the scores' baselines.
func (s *Store) SkipDays(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT date FROM day_tags WHERE source = 'user' AND tag IN ('sick', 'travel')`) // Skip
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = true
	}
	return out, rows.Err()
}

// SkipOf are the days in list tagged sick or travel (SkipDays without a
// database: the demo).
func SkipOf(list []Checkin) map[string]bool {
	out := map[string]bool{}
	for _, c := range list {
		for _, t := range c.Tags {
			if slices.Contains(Skip, t) {
				out[c.Date] = true
			}
		}
	}
	return out
}
