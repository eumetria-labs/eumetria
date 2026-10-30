package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// What the admin screens (PLAN M5b) read and change about other users.

var (
	ErrSelf      = errors.New("admins can't do that to their own account")
	ErrLastAdmin = errors.New("that would leave no admin")
)

// UserInfo is a user as the admin screens list them.
type UserInfo struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Role                Role       `json:"role"`
	Disabled            bool       `json:"disabled"`
	RequireSecondFactor bool       `json:"require_second_factor"`
	SecondFactor        bool       `json:"second_factor"`
	CreatedAt           time.Time  `json:"created_at"`
	LastSeen            *time.Time `json:"last_seen,omitempty"`
	Sessions            int        `json:"sessions"`
}

// UserList lists every account with what the admin needs to know about it.
func (s *Store) UserList(ctx context.Context) ([]UserInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.name, coalesce(r.name, ''), u.disabled, u.require_second_factor, u.created_at,
			EXISTS (SELECT 1 FROM totp WHERE user_id = u.id) OR EXISTS (SELECT 1 FROM webauthn_credentials WHERE user_id = u.id),
			(SELECT max(last_seen_at) FROM sessions WHERE user_id = u.id),
			(SELECT count(*) FROM sessions WHERE user_id = u.id AND expires_at >= ?)
		FROM users u`+roleJoin+` ORDER BY u.name`, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserInfo
	for rows.Next() {
		var x UserInfo
		var role string
		var created int64
		var seen *int64
		if err := rows.Scan(&x.ID, &x.Name, &role, &x.Disabled, &x.RequireSecondFactor, &created, &x.SecondFactor, &seen, &x.Sessions); err != nil {
			return nil, err
		}
		x.Role, x.CreatedAt = Role(role), time.Unix(created, 0).UTC()
		if seen != nil {
			t := time.Unix(*seen, 0).UTC()
			x.LastSeen = &t
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// UserByID returns the account with id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) { return s.userByID(ctx, id) }

// SetDisabled turns an account off (its sessions end and its tokens stop
// working) or on again.
func (s *Store) SetDisabled(ctx context.Context, u *User, disabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET disabled = ? WHERE id = ?", disabled, u.ID); err != nil {
		return err
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetRequireSecondFactor asks u to set up a second factor before they get
// any data, or stops asking.
func (s *Store) SetRequireSecondFactor(ctx context.Context, u *User, require bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET require_second_factor = ? WHERE id = ?", require, u.ID)
	return err
}

// AdminsLeft counts the enabled accounts with the admin scope, as they
// would be if u had the role role and were disabled or not.
func (s *Store) AdminsLeft(ctx context.Context, u *User, role Role, disabled bool) (int, error) {
	users, err := s.Users(ctx)
	if err != nil {
		return 0, err
	}
	roles, err := s.Roles(ctx)
	if err != nil {
		return 0, err
	}
	admin := map[Role]bool{}
	for _, r := range roles {
		admin[Role(strings.ToLower(string(r.Name)))] = slices.Contains(r.Scopes, ScopeAdmin)
	}
	n := 0
	for _, x := range users {
		r, off := x.Role, x.Disabled
		if x.ID == u.ID {
			r, off = role, disabled
		}
		if !off && admin[Role(strings.ToLower(string(r)))] {
			n++
		}
	}
	return n, nil
}

// SessionInfo is a session as the admin screens list them.
type SessionInfo struct {
	ID        int64     `json:"id"`
	User      string    `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	Expires   time.Time `json:"expires"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
}

// AllSessions lists every live session, the most recently used first.
func (s *Store) AllSessions(ctx context.Context) ([]SessionInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, u.name, s.created_at, s.last_seen_at, s.expires_at, s.ip, s.user_agent
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.expires_at >= ? ORDER BY s.last_seen_at DESC`, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var x SessionInfo
		var created, seen, expires int64
		if err := rows.Scan(&x.ID, &x.User, &created, &seen, &expires, &x.IP, &x.UserAgent); err != nil {
			return nil, err
		}
		x.CreatedAt, x.LastSeen, x.Expires = time.Unix(created, 0).UTC(), time.Unix(seen, 0).UTC(), time.Unix(expires, 0).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

// EndSession ends anyone's session and returns whose it was.
func (s *Store) EndSession(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, "SELECT u.name FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?", id).Scan(&name)
	if err != nil {
		return "", ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return name, err
}
