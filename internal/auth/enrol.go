package auth

import (
	"context"
	"time"
)

// Enrolment links (pentest F19). An account that must have a second factor
// but has none yet can only set up its first one with a link an admin (or
// the command line) made: whoever only knows the password, maybe a
// phisher or someone who saw the initial password, can't make the account
// theirs by setting up their own factor first.

// EnrolTTL is how long an enrolment link works.
const EnrolTTL = 24 * time.Hour

// NewEnrolment makes a one-time enrolment token for u, valid for EnrolTTL.
// Older tokens of u that weren't used stop working: only the newest link
// counts.
func (s *Store) NewEnrolment(ctx context.Context, u *User) (token string, expires time.Time, err error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.Now()
	expires = now.Add(EnrolTTL)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM enrolments WHERE user_id = ? AND used_at IS NULL", u.ID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO enrolments (user_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)",
		u.ID, hash, now.Unix(), expires.Unix()); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, tx.Commit()
}

// EnrolmentValid reports whether token is an unused, unexpired enrolment
// token of u.
func (s *Store) EnrolmentValid(ctx context.Context, u *User, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM enrolments
		WHERE user_id = ? AND token_hash = ? AND used_at IS NULL AND expires_at > ?`, u.ID, HashToken(token), s.now()).Scan(&n)
	return n > 0, err
}

// UseEnrolment uses up token, if it is still valid for u; false if not.
func (s *Store) UseEnrolment(ctx context.Context, u *User, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	now := s.now()
	res, err := s.DB.ExecContext(ctx, `UPDATE enrolments SET used_at = ?
		WHERE user_id = ? AND token_hash = ? AND used_at IS NULL AND expires_at > ?`, now, u.ID, HashToken(token), now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

/* ---------- security notices ---------- */

// Notice is a change to how an account signs in, shown to its user until
// they dismiss it (pentest F18/F19): a key or an app someone else added
// doesn't go unnoticed.
type Notice struct {
	ID     int64     `json:"id"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
	IP     string    `json:"ip,omitempty"`
	At     time.Time `json:"at"`
}

// AddNotice records a notice for u about a change made in the session
// with key session (Session.Key; "": not in one of u's sessions).
func (s *Store) AddNotice(ctx context.Context, u *User, kind, detail, ip, session string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO security_notices (user_id, kind, detail, ip, session, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		u.ID, kind, detail, ip, session, s.now())
	return err
}

// Notices returns u's notices they haven't dismissed, newest first (at most
// 20), leaving out the changes made in session: whoever made them knows.
func (s *Store) Notices(ctx context.Context, u *User, session string) ([]Notice, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, detail, ip, created_at FROM security_notices
		WHERE user_id = ? AND seen_at IS NULL AND (session = '' OR session != ?)
		ORDER BY created_at DESC, id DESC LIMIT 20`, u.ID, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		var at int64
		if err := rows.Scan(&n.ID, &n.Kind, &n.Detail, &n.IP, &at); err != nil {
			return nil, err
		}
		n.At = time.Unix(at, 0).UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

// SeeNotices dismisses u's notices up to id (all of them with 0).
func (s *Store) SeeNotices(ctx context.Context, u *User, upTo int64) error {
	q, args := "UPDATE security_notices SET seen_at = ? WHERE user_id = ? AND seen_at IS NULL", []any{s.now(), u.ID}
	if upTo > 0 {
		q += " AND id <= ?"
		args = append(args, upTo)
	}
	_, err := s.DB.ExecContext(ctx, q, args...)
	return err
}
