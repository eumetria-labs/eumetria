package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Invitations: an admin makes an account with someone's e-mail address
// instead of a password; they get a link, choose their password, and with
// that the address counts as confirmed (they got the mail).

// InviteTTL is how long an invitation link works.
const InviteTTL = 72 * time.Hour

// CreateInvited adds an account whose password nobody knows: it is set
// through the invitation (AcceptInvite).
func (s *Store) CreateInvited(ctx context.Context, name string, role Role) (*User, error) {
	random, _, err := NewToken()
	if err != nil {
		return nil, err
	}
	return s.CreateUser(ctx, name, random, role)
}

// NewInvite makes an invitation for u to email and returns the link's token;
// an earlier one stops working.
func (s *Store) NewInvite(ctx context.Context, u *User, email string) (string, time.Time, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.Now()
	expires := now.Add(InviteTTL)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO invites (user_id, email, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, token_hash = excluded.token_hash,
		created_at = excluded.created_at, expires_at = excluded.expires_at`, u.ID, email, hash, now.Unix(), expires.Unix())
	return token, expires, err
}

// InviteEmail is the address u was invited at, "" without an invitation.
func (s *Store) InviteEmail(ctx context.Context, u *User) (string, error) {
	var email string
	err := s.DB.QueryRowContext(ctx, "SELECT email FROM invites WHERE user_id = ?", u.ID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return email, err
}

// Invite is the account a valid invitation token is for (ErrNotFound if
// none, used or expired).
func (s *Store) Invite(ctx context.Context, token string) (*User, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, "SELECT user_id FROM invites WHERE token_hash = ? AND expires_at > ?", HashToken(token), s.now()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u, err := s.userByID(ctx, id)
	if err == nil && u.Disabled {
		return nil, ErrNotFound
	}
	return u, err
}

// AcceptInvite sets the password of the account token invites to, makes
// the invited address its confirmed one and uses the invitation up.
func (s *Store) AcceptInvite(ctx context.Context, token, password string) (*User, error) {
	u, err := s.Invite(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(password, u.Name); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var email string
	// Taking it out first makes a second, parallel use fail.
	if err := tx.QueryRowContext(ctx, "DELETE FROM invites WHERE user_id = ? AND token_hash = ? RETURNING email", u.ID, HashToken(token)).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ?, password_at = ? WHERE id = ?", hash, now, u.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (user_id, email, email_verified_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, email_verified_at = excluded.email_verified_at`, u.ID, email, now); err != nil {
		return nil, err
	}
	return u, tx.Commit()
}
