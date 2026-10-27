package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"eumetria/internal/db"
)

// Role is what a user may do. M5 replaces the two fixed ones with roles
// made of permissions.
type Role string

const (
	RoleAdmin  Role = "admin"  // everything, including sync and the audit log
	RoleViewer Role = "viewer" // reads the data
)

type User struct {
	ID       int64
	Name     string
	Role     Role
	Disabled bool
}

type Session struct {
	ID              int64
	UserID          int64
	Remember        bool
	CreatedAt       time.Time
	LastSeen        time.Time
	Expires         time.Time
	AuthenticatedAt time.Time // the sign-in; changing second factors needs it recent
	IP              string
	UserAgent       string
}

type APIToken struct {
	ID        int64
	UserID    int64
	Name      string
	Scopes    []string
	CreatedAt time.Time
	Expires   time.Time // zero: never
	LastUsed  time.Time // zero: not yet
}

// Has reports whether the token carries scope.
func (t *APIToken) Has(scope string) bool { return slices.Contains(t.Scopes, scope) }

type AuditEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
	IP     string    `json:"ip,omitempty"`
}

// How long a session lasts. With "Remember me" it ends after a week
// without use, or a month at most; without, after 12 hours either way
// (and the browser drops its cookie when it closes).
const (
	SessionIdle  = 7 * 24 * time.Hour
	SessionMax   = 30 * 24 * time.Hour
	SessionShort = 12 * time.Hour
	// Last use is written at most this often, not on every request.
	touchEvery = 5 * time.Minute
)

// Scopes an API token can carry.
var Scopes = []string{"read", "sync"}

var (
	ErrNotFound  = errors.New("not found")
	ErrNameTaken = errors.New("user name taken")
	ErrBadName   = errors.New("user name: 1 to 64 letters, digits and . _ - @, starting with a letter or digit")
	ErrBadScope  = errors.New("unknown scope")
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

// Store reads and writes the accounts in the app's database.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

func NewStore(d *db.DB) *Store { return &Store{DB: d, Now: time.Now} }

func (s *Store) now() int64 { return s.Now().Unix() }

// HasUsers reports whether any account exists; the app serves nothing
// until one does.
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n > 0, err
}

// CreateUser adds an account after checking the name and the password.
func (s *Store) CreateUser(ctx context.Context, name, password string, role Role) (*User, error) {
	if !validName.MatchString(name) {
		return nil, ErrBadName
	}
	if role != RoleAdmin && role != RoleViewer {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	if err := ValidatePassword(password, name); err != nil {
		return nil, err
	}
	return s.createUser(ctx, name, password, role)
}

// CreateDemoUser adds an account without the password policy, for -demo.
func (s *Store) CreateDemoUser(ctx context.Context, name, password string) (*User, error) {
	return s.createUser(ctx, name, password, RoleAdmin)
}

func (s *Store) createUser(ctx context.Context, name, password string, role Role) (*User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	res, err := s.DB.ExecContext(ctx, "INSERT INTO users (name, password_hash, role, created_at, password_at) VALUES (?, ?, ?, ?, ?)",
		name, hash, string(role), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Name: name, Role: role}, nil
}

const userCols = "id, name, role, disabled"

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var role string
	if err := row.Scan(&u.ID, &u.Name, &role, &u.Disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Role = Role(role)
	return u, nil
}

func (s *Store) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE name = ?", name))
}

// Authenticate returns the user when name and password match an enabled
// account. Every miss takes the time of a password check, whether the name
// exists or not.
func (s *Store) Authenticate(ctx context.Context, name, password string) (*User, error) {
	var hash string
	u := &User{}
	var role string
	err := s.DB.QueryRowContext(ctx, "SELECT "+userCols+", password_hash FROM users WHERE name = ?", name).
		Scan(&u.ID, &u.Name, &role, &u.Disabled, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		CheckNoUser(password)
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(hash, password) || u.Disabled {
		return nil, ErrNotFound
	}
	u.Role = Role(role)
	return u, nil
}

// SetPassword replaces a user's password and ends all their sessions.
func (s *Store) SetPassword(ctx context.Context, u *User, password string) error {
	if err := ValidatePassword(password, u.Name); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ?, password_at = ? WHERE id = ?", hash, s.now(), u.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID); err != nil {
		return err
	}
	return tx.Commit()
}

/* ---------- sessions ---------- */

// CreateSession starts a session for u and returns its token, which only
// the browser keeps (in a cookie).
func (s *Store) CreateSession(ctx context.Context, u *User, remember bool, ip, userAgent string) (string, *Session, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	now := s.Now()
	max := SessionShort
	if remember {
		max = SessionMax
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	sess := &Session{UserID: u.ID, Remember: remember, CreatedAt: now, LastSeen: now, Expires: now.Add(max), AuthenticatedAt: now, IP: ip, UserAgent: userAgent}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (user_id, token_hash, remember, created_at, last_seen_at, expires_at, authenticated_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, u.ID, hash, remember, now.Unix(), now.Unix(), sess.Expires.Unix(), now.Unix(), ip, userAgent)
	if err != nil {
		return "", nil, err
	}
	sess.ID, _ = res.LastInsertId()
	// Old sessions go when new ones come, so the table never grows.
	s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now.Unix())
	return token, sess, nil
}

// Session returns the live session of token and its user. An expired, idle
// or orphaned one is deleted and reported as ErrNotFound.
func (s *Store) Session(ctx context.Context, token string) (*Session, *User, error) {
	sess := &Session{}
	u := &User{}
	var role string
	var created, seen, expires, authed int64
	err := s.DB.QueryRowContext(ctx, `SELECT s.id, s.user_id, s.remember, s.created_at, s.last_seen_at, s.expires_at, s.authenticated_at, s.ip, s.user_agent,
			u.id, u.name, u.role, u.disabled
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, HashToken(token)).
		Scan(&sess.ID, &sess.UserID, &sess.Remember, &created, &seen, &expires, &authed, &sess.IP, &sess.UserAgent, &u.ID, &u.Name, &role, &u.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	u.Role = Role(role)
	sess.CreatedAt, sess.LastSeen, sess.Expires, sess.AuthenticatedAt = time.Unix(created, 0), time.Unix(seen, 0), time.Unix(expires, 0), time.Unix(authed, 0)
	now := s.Now()
	idle := SessionShort
	if sess.Remember {
		idle = SessionIdle
	}
	if u.Disabled || now.After(sess.Expires) || now.Sub(sess.LastSeen) > idle {
		s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", sess.ID)
		return nil, nil, ErrNotFound
	}
	if now.Sub(sess.LastSeen) > touchEvery {
		sess.LastSeen = now
		s.DB.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", now.Unix(), sess.ID)
	}
	return sess, u, nil
}

// Sessions lists u's live sessions, newest first.
func (s *Store) Sessions(ctx context.Context, u *User) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, remember, created_at, last_seen_at, expires_at, ip, user_agent
		FROM sessions WHERE user_id = ? AND expires_at >= ? ORDER BY last_seen_at DESC`, u.ID, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		var created, seen, expires int64
		if err := rows.Scan(&x.ID, &x.UserID, &x.Remember, &created, &seen, &expires, &x.IP, &x.UserAgent); err != nil {
			return nil, err
		}
		x.CreatedAt, x.LastSeen, x.Expires = time.Unix(created, 0), time.Unix(seen, 0), time.Unix(expires, 0)
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeleteSession ends one of u's sessions; another user's id is not found.
func (s *Store) DeleteSession(ctx context.Context, u *User, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ? AND user_id = ?", id, u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSessions ends all of u's sessions.
func (s *Store) DeleteSessions(ctx context.Context, u *User) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID)
	return err
}

/* ---------- API tokens ---------- */

// CreateAPIToken returns a new token for u with the given scopes; expires
// zero means it never expires. The token is shown once and kept only as
// its hash.
func (s *Store) CreateAPIToken(ctx context.Context, u *User, name string, scopes []string, expires time.Time) (string, error) {
	for _, sc := range scopes {
		if !slices.Contains(Scopes, sc) {
			return "", fmt.Errorf("%w: %q", ErrBadScope, sc)
		}
	}
	if len(scopes) == 0 {
		return "", fmt.Errorf("%w: none given", ErrBadScope)
	}
	raw, _, err := NewToken()
	if err != nil {
		return "", err
	}
	token := APITokenPrefix + raw
	var exp any
	if !expires.IsZero() {
		exp = expires.Unix()
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO api_tokens (user_id, name, token_hash, scopes, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		u.ID, name, HashToken(token), strings.Join(scopes, " "), s.now(), exp)
	return token, err
}

// APIToken returns the live token and its user.
func (s *Store) APIToken(ctx context.Context, token string) (*APIToken, *User, error) {
	if !strings.HasPrefix(token, APITokenPrefix) {
		return nil, nil, ErrNotFound
	}
	t := &APIToken{}
	u := &User{}
	var role, scopes string
	var created int64
	var expires, used sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT t.id, t.user_id, t.name, t.scopes, t.created_at, t.expires_at, t.last_used_at,
			u.id, u.name, u.role, u.disabled
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = ?`, HashToken(token)).
		Scan(&t.ID, &t.UserID, &t.Name, &scopes, &created, &expires, &used, &u.ID, &u.Name, &role, &u.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	u.Role = Role(role)
	t.Scopes = strings.Fields(scopes)
	t.CreatedAt = time.Unix(created, 0)
	if expires.Valid {
		t.Expires = time.Unix(expires.Int64, 0)
	}
	if used.Valid {
		t.LastUsed = time.Unix(used.Int64, 0)
	}
	now := s.Now()
	if u.Disabled || (!t.Expires.IsZero() && now.After(t.Expires)) {
		return nil, nil, ErrNotFound
	}
	if now.Sub(t.LastUsed) > touchEvery {
		s.DB.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", now.Unix(), t.ID)
	}
	return t, u, nil
}

/* ---------- audit log ---------- */

// Audit records what happened, who did it (u may be nil) and from where.
func (s *Store) Audit(ctx context.Context, u *User, actor, action, detail, ip string) error {
	var uid any
	if u != nil {
		uid, actor = u.ID, u.Name
	}
	if len(actor) > 64 {
		actor = actor[:64]
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO audit_log (at, user_id, actor, action, detail, ip) VALUES (?, ?, ?, ?, ?, ?)",
		s.now(), uid, actor, action, detail, ip)
	return err
}

// AuditLog returns the newest entries first, at most limit.
func (s *Store) AuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, at, actor, action, detail, ip FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
