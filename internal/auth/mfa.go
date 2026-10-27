package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

/* ---------- authenticator app (TOTP, RFC 6238) ---------- */

// The settings every authenticator app understands: SHA-1, 6 digits, 30 s.
const (
	totpStep   = 30
	totpDigits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret in base32, as apps take it.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// TOTPURI is what the QR code holds: the app adds an entry named after
// the site and the user.
func TOTPURI(secret, account string) string {
	label := url.PathEscape("Eumetria:" + account)
	q := url.Values{"secret": {secret}, "issuer": {"Eumetria"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000)
}

// TOTPCode is the code an app shows for secret at t (for tests, and for
// anything that has to act as the app).
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpCode(key, t.Unix()/totpStep), nil
}

// CheckTOTP finds the time step code belongs to, allowing one step of
// clock drift either way. A step at or before lastStep doesn't count, so a
// code works only once even within its 30 seconds.
func CheckTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	code = strings.ReplaceAll(code, " ", "")
	if err != nil || len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpStep
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		if step > lastStep && subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// SetTOTP turns the authenticator app on with secret; lastStep is the step
// of the code that confirmed it.
func (s *Store) SetTOTP(ctx context.Context, u *User, secret string, lastStep int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO totp (user_id, secret, last_step, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, last_step = excluded.last_step, created_at = excluded.created_at`,
		u.ID, secret, lastStep, s.now())
	return err
}

func (s *Store) DeleteTOTP(ctx context.Context, u *User) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM totp WHERE user_id = ?", u.ID)
	return err
}

// UseTOTP checks a code from u's app and uses it up.
func (s *Store) UseTOTP(ctx context.Context, u *User, code string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var secret string
	var last int64
	err = tx.QueryRowContext(ctx, "SELECT secret, last_step FROM totp WHERE user_id = ?", u.ID).Scan(&secret, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	step, ok := CheckTOTP(secret, code, s.Now(), last)
	if !ok {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE totp SET last_step = ? WHERE user_id = ?", step, u.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

/* ---------- recovery codes ---------- */

// Ten codes of 12 characters from an alphabet without look-alikes (no 0/o,
// 1/l/i): 60 bits each, more than enough behind the sign-in limits.
const (
	recoveryCount    = 10
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
)

// normalizeCode accepts a code as typed: any case, with or without dashes
// and spaces.
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(code))
}

// NewRecoveryCodes replaces u's recovery codes with ten new ones and
// returns them; only their hashes are kept.
func (s *Store) NewRecoveryCodes(ctx context.Context, u *User) ([]string, error) {
	codes := make([]string, recoveryCount)
	for i := range codes {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		for j := range b {
			b[j] = recoveryAlphabet[int(b[j])%len(recoveryAlphabet)]
		}
		codes[i] = string(b[0:4]) + "-" + string(b[4:8]) + "-" + string(b[8:12])
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE user_id = ?", u.ID); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)", u.ID, HashToken(normalizeCode(c))); err != nil {
			return nil, err
		}
	}
	return codes, tx.Commit()
}

// UseRecoveryCode checks code against u's unused codes and uses it up.
func (s *Store) UseRecoveryCode(ctx context.Context, u *User, code string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, "UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL",
		s.now(), u.ID, HashToken(normalizeCode(code)))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

/* ---------- passkeys and security keys (WebAuthn) ---------- */

// Key is a registered passkey or security key.
type Key struct {
	ID           int64
	Name         string
	Passwordless bool
	CreatedAt    time.Time
	LastUsed     time.Time // zero: not yet
	Credential   webauthn.Credential
}

// WebAuthnHandle returns u's user handle for passkeys, making one on first use.
func (s *Store) WebAuthnHandle(ctx context.Context, u *User) ([]byte, error) {
	var h []byte
	if err := s.DB.QueryRowContext(ctx, "SELECT webauthn_handle FROM users WHERE id = ?", u.ID).Scan(&h); err != nil {
		return nil, err
	}
	if len(h) > 0 {
		return h, nil
	}
	h = make([]byte, 32)
	if _, err := rand.Read(h); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET webauthn_handle = ? WHERE id = ? AND webauthn_handle IS NULL", h, u.ID)
	return h, err
}

// UserByHandle finds the user a passkey belongs to.
func (s *Store) UserByHandle(ctx context.Context, handle []byte) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE webauthn_handle = ?", handle))
}

func (s *Store) Keys(ctx context.Context, u *User) ([]Key, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name, passwordless, created_at, last_used_at, data FROM webauthn_credentials WHERE user_id = ? ORDER BY id", u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var k Key
		var created int64
		var used sql.NullInt64
		var data string
		if err := rows.Scan(&k.ID, &k.Name, &k.Passwordless, &created, &used, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &k.Credential); err != nil {
			return nil, err
		}
		k.CreatedAt = time.Unix(created, 0)
		if used.Valid {
			k.LastUsed = time.Unix(used.Int64, 0)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) AddKey(ctx context.Context, u *User, name string, c *webauthn.Credential, passwordless bool) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO webauthn_credentials (user_id, credential_id, name, data, passwordless, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		u.ID, c.ID, name, string(data), passwordless, s.now())
	return err
}

// KeyUsed stores what a sign-in changed (the signature counter, flags).
func (s *Store) KeyUsed(ctx context.Context, u *User, c *webauthn.Credential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE webauthn_credentials SET data = ?, last_used_at = ? WHERE user_id = ? AND credential_id = ?",
		string(data), s.now(), u.ID, c.ID)
	return err
}

func (s *Store) DeleteKey(ctx context.Context, u *User, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?", id, u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ---------- all of them ---------- */

// Factors says which second factors u has.
type Factors struct {
	TOTP         bool `json:"totp"`
	Keys         int  `json:"keys"`
	RecoveryLeft int  `json:"recovery_left"`
}

// Any reports whether u has a second factor; recovery codes alone don't
// count, they stand in for a lost one.
func (f Factors) Any() bool { return f.TOTP || f.Keys > 0 }

func (s *Store) Factors(ctx context.Context, u *User) (Factors, error) {
	var f Factors
	err := s.DB.QueryRowContext(ctx, `SELECT
			EXISTS (SELECT 1 FROM totp WHERE user_id = ?1),
			(SELECT count(*) FROM webauthn_credentials WHERE user_id = ?1),
			(SELECT count(*) FROM recovery_codes WHERE user_id = ?1 AND used_at IS NULL)`, u.ID).
		Scan(&f.TOTP, &f.Keys, &f.RecoveryLeft)
	return f, err
}

// ResetFactors removes all of u's second factors and ends their sessions,
// for when every one of them is lost (-reset-second-factors).
func (s *Store) ResetFactors(ctx context.Context, u *User) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM webauthn_credentials WHERE user_id = ?",
		"DELETE FROM totp WHERE user_id = ?",
		"DELETE FROM recovery_codes WHERE user_id = ?",
		"DELETE FROM sessions WHERE user_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, u.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetAuthenticated marks the session as just signed in again.
func (s *Store) SetAuthenticated(ctx context.Context, sess *Session) error {
	sess.AuthenticatedAt = s.Now()
	_, err := s.DB.ExecContext(ctx, "UPDATE sessions SET authenticated_at = ? WHERE id = ?", sess.AuthenticatedAt.Unix(), sess.ID)
	return err
}
