package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// E-mail addresses: what a profile shows (Profile.Email), whether its user
// confirmed it, and a new one waiting for confirmation. Alerts and links
// go only to a confirmed address, so whoever briefly has someone's session
// can't point them elsewhere unnoticed (PLAN phase 6).

// EmailTTL is how long a confirmation link works.
const EmailTTL = 24 * time.Hour

// EmailState is where u's mail goes.
type EmailState struct {
	Email    string `json:"email"`
	Verified bool   `json:"email_verified"`
	Pending  string `json:"email_pending,omitempty"` // waiting for its confirmation
}

// Email returns u's address, whether it is confirmed, and one waiting.
func (s *Store) Email(ctx context.Context, u *User) (EmailState, error) {
	var st EmailState
	var at int64
	err := s.DB.QueryRowContext(ctx, "SELECT email, email_verified_at FROM profiles WHERE user_id = ?", u.ID).Scan(&st.Email, &at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	st.Verified = st.Email != "" && at > 0
	err = s.DB.QueryRowContext(ctx, "SELECT email FROM email_changes WHERE user_id = ? AND expires_at > ?", u.ID, s.now()).Scan(&st.Pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	return st, nil
}

// VerifiedEmail is u's confirmed address, "" without one.
func (s *Store) VerifiedEmail(ctx context.Context, u *User) (string, error) {
	st, err := s.Email(ctx, u)
	if !st.Verified {
		return "", err
	}
	return st.Email, err
}

// SetEmail makes email u's address, not confirmed ("" removes it), and
// drops one waiting: for when no confirmation can be sent.
func (s *Store) SetEmail(ctx context.Context, u *User, email string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (user_id, email, email_verified_at) VALUES (?, ?, 0)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, email_verified_at = 0`, u.ID, email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM email_changes WHERE user_id = ?", u.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RequestEmail lets email wait for its confirmation and returns the token
// for the link; an earlier one stops working.
func (s *Store) RequestEmail(ctx context.Context, u *User, email string) (string, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", err
	}
	now := s.Now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO email_changes (user_id, email, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, token_hash = excluded.token_hash,
		created_at = excluded.created_at, expires_at = excluded.expires_at`, u.ID, email, hash, now.Unix(), now.Add(EmailTTL).Unix())
	return token, err
}

// ConfirmEmail makes the address waiting under token u's confirmed one. It
// returns the address, or "" when the token isn't valid (any more).
func (s *Store) ConfirmEmail(ctx context.Context, u *User, token string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var email string
	err = tx.QueryRowContext(ctx, "SELECT email FROM email_changes WHERE user_id = ? AND token_hash = ? AND expires_at > ?",
		u.ID, HashToken(token), s.now()).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (user_id, email, email_verified_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, email_verified_at = excluded.email_verified_at`, u.ID, email, s.now()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM email_changes WHERE user_id = ?", u.ID); err != nil {
		return "", err
	}
	return email, tx.Commit()
}
