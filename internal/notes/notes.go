// Package notes keeps what people write about their workouts, one Markdown
// note per Fitbit workout, in the app's database. Who may read or write them
// is the server's business (cmd/eumetria, notes.go); it also makes sure a new
// note belongs to a workout that exists.
package notes

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"eumetria/internal/db"
)

var ErrNotFound = errors.New("no such note")

// Note is a workout's note. Date is the workout's day, which comes from the
// synced data: it is empty here, and for a workout that is gone.
type Note struct {
	WorkoutID string    `json:"workout_id"`
	Date      string    `json:"date,omitempty"`
	Markdown  string    `json:"markdown"`
	CreatedBy string    `json:"created_by"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store reads and writes the notes.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

const selectNote = `SELECT n.workout_id, n.markdown, c.name, u.name, n.created_at, n.updated_at
	FROM notes n JOIN users c ON c.id = n.created_by JOIN users u ON u.id = n.updated_by`

type scanner interface{ Scan(...any) error }

func scanNote(row scanner) (Note, error) {
	var n Note
	var created, updated int64
	err := row.Scan(&n.WorkoutID, &n.Markdown, &n.CreatedBy, &n.UpdatedBy, &created, &updated)
	n.CreatedAt, n.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return n, err
}

// List returns every note, the oldest first.
func (s *Store) List(ctx context.Context) ([]Note, error) {
	rows, err := s.DB.QueryContext(ctx, selectNote+" ORDER BY n.created_at, n.workout_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Put writes the note of a workout for the user userID, a new one or over
// the one it has, and reports which. Two people saving at once can't fail:
// the later one wins.
func (s *Store) Put(ctx context.Context, workoutID, markdown string, userID int64) (Note, bool, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO notes (workout_id, markdown, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, workoutID, markdown, userID, userID, now, now)
	if err != nil {
		return Note{}, false, err
	}
	added, err := res.RowsAffected()
	if err != nil {
		return Note{}, false, err
	}
	if added == 0 {
		_, err := s.DB.ExecContext(ctx, "UPDATE notes SET markdown = ?, updated_by = ?, updated_at = ? WHERE workout_id = ?",
			markdown, userID, now, workoutID)
		if err != nil {
			return Note{}, false, err
		}
	}
	n, err := scanNote(s.DB.QueryRowContext(ctx, selectNote+" WHERE n.workout_id = ?", workoutID))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound // deleted in between
	}
	return n, added > 0, err
}

// Delete removes a workout's note.
func (s *Store) Delete(ctx context.Context, workoutID string) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM notes WHERE workout_id = ?", workoutID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
