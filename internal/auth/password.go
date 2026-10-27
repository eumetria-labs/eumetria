// Package auth holds the accounts of the app: password hashing and policy,
// session and API tokens, the login rate limiter and the database queries
// for users, sessions, tokens and the audit log.
package auth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id with OWASP's parameters for it: 19 MiB, 2 passes, 1 lane. A
// check takes about 30 ms, which is nothing for a login and a lot for
// someone guessing.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns the password's hash in the usual encoded form
// ($argon2id$v=19$m=…,t=…,p=…$salt$hash), which carries its parameters, so
// stronger ones later still check old hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches encoded, comparing in
// constant time.
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked against when a login names no user, so that answer
// takes as long as a wrong password and doesn't tell which names exist.
var dummyHash, _ = HashPassword("no user has this password, it only takes time")

// CheckNoUser spends the time of one password check and reports false.
func CheckNoUser(password string) bool {
	CheckPassword(dummyHash, password)
	return false
}

//go:embed common-passwords.txt
var commonList []byte

var common = func() map[string]bool {
	m := map[string]bool{}
	s := bufio.NewScanner(bytes.NewReader(commonList))
	for s.Scan() {
		if line := s.Text(); line != "" && !strings.HasPrefix(line, "# ") {
			m[line] = true
		}
	}
	return m
}()

// MinPasswordLength is counted in characters, not bytes.
const MinPasswordLength = 12

// Why a password is refused; the page shows its own text for each.
var (
	ErrPasswordShort  = errors.New("password: at least 12 characters")
	ErrPasswordLong   = errors.New("password: at most 1024 bytes")
	ErrPasswordCommon = errors.New("password: one of the most used passwords")
	ErrPasswordIsName = errors.New("password: the same as the user name")
)

// ValidatePassword applies the policy: long enough, not absurdly long, not
// a well-known password and not the user name. Length beats complexity
// rules (NIST SP 800-63B), so there are none.
func ValidatePassword(password, name string) error {
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return ErrPasswordShort
	case len(password) > 1024:
		return ErrPasswordLong
	case common[strings.ToLower(password)]:
		return ErrPasswordCommon
	case strings.EqualFold(password, name):
		return ErrPasswordIsName
	}
	return nil
}
