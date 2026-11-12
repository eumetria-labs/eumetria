package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Known devices (pentest re-test N2). A browser that signed in to an
// account gets a token naming the account, signed with the server's
// device key. Under a guessing attack on the name, sign-ins without one
// share a small budget, while the owner's browsers go on as usual. The
// signature covers when the password was set, so a new password makes
// every older device token worthless.

// DeviceMax is how long a device token is good for.
const DeviceMax = 365 * 24 * time.Hour

// serverKey returns the key called name, made on first use.
func (s *Store) serverKey(ctx context.Context, name string) ([]byte, error) {
	var key []byte
	err := s.DB.QueryRowContext(ctx, "SELECT key FROM server_keys WHERE name = ?", name).Scan(&key)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// Two at once: the first one stays, and both use it.
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO server_keys (name, key) VALUES (?, ?) ON CONFLICT (name) DO NOTHING", name, key); err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT key FROM server_keys WHERE name = ?", name).Scan(&key)
	return key, err
}

func (s *Store) deviceMAC(ctx context.Context, id, issued int64) ([]byte, error) {
	key, err := s.serverKey(ctx, "device")
	if err != nil {
		return nil, err
	}
	var passwordAt int64
	if err := s.DB.QueryRowContext(ctx, "SELECT password_at FROM users WHERE id = ?", id).Scan(&passwordAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "device|%d|%d|%d", id, issued, passwordAt)
	return m.Sum(nil), nil
}

// DeviceToken returns a new device token for u.
func (s *Store) DeviceToken(ctx context.Context, u *User) (string, error) {
	issued := s.now()
	mac, err := s.deviceMAC(ctx, u.ID, issued)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(u.ID, 10) + "." + strconv.FormatInt(issued, 10) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// DeviceUser returns the account a device token was made for, when it is
// genuine, not too old and the password hasn't changed since; else 0.
func (s *Store) DeviceUser(ctx context.Context, token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	id, err1 := strconv.ParseInt(parts[0], 10, 64)
	issued, err2 := strconv.ParseInt(parts[1], 10, 64)
	got, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || id <= 0 {
		return 0
	}
	if age := s.Now().Sub(time.Unix(issued, 0)); age < 0 || age > DeviceMax {
		return 0
	}
	want, err := s.deviceMAC(ctx, id, issued)
	if err != nil || !hmac.Equal(got, want) {
		return 0
	}
	return id
}
