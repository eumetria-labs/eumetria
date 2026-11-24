// Package notes keeps what people write about their trainings, one Markdown
// note per training (internal/sessions: PLAN phase 14), in the app's
// database. Who may read or write them is the server's business
// (cmd/eumetria, notes.go): a note is reached through a workout of its
// training, and the server makes sure that workout exists.
package notes

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/sessions"
)

var ErrNotFound = errors.New("no such note")

// Note is a training's note. WorkoutID is the training's first Fitbit
// workout ("" without), Workouts all of them. Date is the training's day;
// the server sets it to the workout's from the synced data.
type Note struct {
	SessionID int64          `json:"session_id"`
	WorkoutID string         `json:"workout_id,omitempty"`
	Workouts  []sessions.Ref `json:"workouts"`
	Date      string         `json:"date,omitempty"`
	Markdown  string         `json:"markdown"`
	CreatedBy string         `json:"created_by"`
	UpdatedBy string         `json:"updated_by"`
	// The address of the picture of who wrote it last, if they have one.
	UpdatedByAvatar string    `json:"updated_by_avatar,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Store reads and writes the notes.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// An author whose account was deleted is "" (the page says "deleted user").
const selectNote = `SELECT n.session_id, s.date, n.markdown, coalesce(c.name, ''), coalesce(u.name, ''), coalesce(u.id, 0), coalesce(p.avatar, 0), n.created_at, n.updated_at
	FROM training_notes n JOIN training_sessions s ON s.id = n.session_id
	LEFT JOIN users c ON c.id = n.created_by LEFT JOIN users u ON u.id = n.updated_by
	LEFT JOIN profiles p ON p.user_id = u.id`

type scanner interface{ Scan(...any) error }

func scanNote(row scanner) (Note, error) {
	var n Note
	var writer, avatar, created, updated int64
	err := row.Scan(&n.SessionID, &n.Date, &n.Markdown, &n.CreatedBy, &n.UpdatedBy, &writer, &avatar, &created, &updated)
	n.UpdatedByAvatar = auth.AvatarURL(writer, avatar)
	n.CreatedAt, n.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	n.Workouts = []sessions.Ref{}
	return n, err
}

// workouts fills in the notes' workouts.
func (s *Store) workouts(ctx context.Context, list []Note) error {
	if len(list) == 0 {
		return nil
	}
	at := map[int64]int{}
	for i, n := range list {
		at[n.SessionID] = i
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT session_id, source, workout_id FROM training_workouts ORDER BY session_id, rowid")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var r sessions.Ref
		if err := rows.Scan(&id, &r.Source, &r.ID); err != nil {
			return err
		}
		if i, ok := at[id]; ok {
			list[i].Workouts = append(list[i].Workouts, r)
		}
	}
	for i := range list {
		list[i].WorkoutID = sessions.Primary(list[i].Workouts)
	}
	return rows.Err()
}

// List returns every note, the oldest first.
func (s *Store) List(ctx context.Context) ([]Note, error) {
	rows, err := s.DB.QueryContext(ctx, selectNote+" ORDER BY n.created_at, n.session_id")
	if err != nil {
		return nil, err
	}
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, s.workouts(ctx, out)
}

// Put writes the note of a training for the user userID, a new one or over
// the one it has, and reports which. Two people saving at once can't fail:
// the later one wins.
func (s *Store) Put(ctx context.Context, sessionID int64, markdown string, userID int64) (Note, bool, error) {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO training_notes (session_id, markdown, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sessionID, markdown, userID, userID, now, now)
	if err != nil {
		return Note{}, false, err
	}
	added, err := res.RowsAffected()
	if err != nil {
		return Note{}, false, err
	}
	if added == 0 {
		_, err := s.DB.ExecContext(ctx, "UPDATE training_notes SET markdown = ?, updated_by = ?, updated_at = ? WHERE session_id = ?",
			markdown, userID, now, sessionID)
		if err != nil {
			return Note{}, false, err
		}
	}
	n, err := scanNote(s.DB.QueryRowContext(ctx, selectNote+" WHERE n.session_id = ?", sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound // deleted in between
	}
	if err != nil {
		return n, false, err
	}
	one := []Note{n}
	err = s.workouts(ctx, one)
	return one[0], added > 0, err
}

// Delete removes a training's note.
func (s *Store) Delete(ctx context.Context, sessionID int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM training_notes WHERE session_id = ?", sessionID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
