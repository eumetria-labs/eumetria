package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Profile is what a user says about themselves; all of it is optional.
type Profile struct {
	FirstName string `json:"first_name"`
	Surname   string `json:"surname"`
	Email     string `json:"email"`
	Instagram string `json:"instagram"`
}

var (
	ErrBadPersonName = errors.New("names: up to 64 characters")
	ErrBadEmail      = errors.New("not an e-mail address")
	ErrBadInstagram  = errors.New("Instagram handles: up to 30 letters, digits, . and _")
)

var instagramHandle = regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`)

func validPersonName(s string) bool {
	return utf8.RuneCountInString(s) <= 64 && !strings.ContainsFunc(s, unicode.IsControl)
}

// Clean returns p trimmed, without the @ of a handle, or why it is refused.
func (p Profile) Clean() (Profile, error) {
	p.FirstName, p.Surname = strings.TrimSpace(p.FirstName), strings.TrimSpace(p.Surname)
	p.Email = strings.TrimSpace(p.Email)
	p.Instagram = strings.TrimPrefix(strings.TrimSpace(p.Instagram), "@")
	if !validPersonName(p.FirstName) || !validPersonName(p.Surname) {
		return p, ErrBadPersonName
	}
	if p.Email != "" {
		// Only the address itself, not "Name <address>".
		a, err := mail.ParseAddress(p.Email)
		if err != nil || a.Address != p.Email || len(p.Email) > 254 {
			return p, ErrBadEmail
		}
	}
	if p.Instagram != "" && !instagramHandle.MatchString(p.Instagram) {
		return p, ErrBadInstagram
	}
	return p, nil
}

// AvatarURL is the address of a user's picture, "" without one. The version
// changes with the picture, so it can be cached for good.
func AvatarURL(userID, version int64) string {
	if version == 0 {
		return ""
	}
	return fmt.Sprintf("avatars/%d?v=%d", userID, version)
}

// Profile returns u's profile and the address of their picture.
func (s *Store) Profile(ctx context.Context, u *User) (Profile, string, error) {
	var p Profile
	var avatar int64
	err := s.DB.QueryRowContext(ctx, "SELECT first_name, surname, email, instagram, avatar FROM profiles WHERE user_id = ?", u.ID).
		Scan(&p.FirstName, &p.Surname, &p.Email, &p.Instagram, &avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, "", nil
	}
	return p, AvatarURL(u.ID, avatar), err
}

// SaveProfile stores p as u's profile; the picture and the e-mail address
// stay: the address changes only through SetEmail or a confirmation
// (email.go). p comes from Clean.
func (s *Store) SaveProfile(ctx context.Context, u *User, p Profile) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO profiles (user_id, first_name, surname, instagram) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET first_name = excluded.first_name, surname = excluded.surname,
		instagram = excluded.instagram`, u.ID, p.FirstName, p.Surname, p.Instagram)
	return err
}

// SetAvatar notes that u has a new picture, or none when has is false.
func (s *Store) SetAvatar(ctx context.Context, u *User, has bool) error {
	var version int64
	if has {
		version = s.now()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO profiles (user_id, avatar) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET avatar = excluded.avatar`, u.ID, version)
	return err
}

// TokenInfo is an API token as its owner sees it: never the token itself.
type TokenInfo struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	Expires   *time.Time `json:"expires,omitempty"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
}

// APITokens lists u's tokens, the newest first, expired ones too.
func (s *Store) APITokens(ctx context.Context, u *User) ([]TokenInfo, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name, scopes, created_at, expires_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY id DESC", u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	opt := func(v sql.NullInt64) *time.Time {
		if !v.Valid {
			return nil
		}
		t := time.Unix(v.Int64, 0).UTC()
		return &t
	}
	out := []TokenInfo{}
	for rows.Next() {
		var t TokenInfo
		var scopes string
		var created int64
		var expires, used sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &scopes, &created, &expires, &used); err != nil {
			return nil, err
		}
		t.Scopes, t.CreatedAt, t.Expires, t.LastUsed = strings.Fields(scopes), time.Unix(created, 0).UTC(), opt(expires), opt(used)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of u's tokens and returns its name.
func (s *Store) DeleteAPIToken(ctx context.Context, u *User, id int64) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, "DELETE FROM api_tokens WHERE id = ? AND user_id = ? RETURNING name", id, u.ID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}
