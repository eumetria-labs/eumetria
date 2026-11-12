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
	"time"
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

// A hash needs argonMemory while it runs, and the container has little
// memory: a few dozen at once kill it (pentest re-test N1). So only
// hashSlots run at the same time, every caller included, and one that
// can't get a slot within hashWait gives up with ErrBusy instead of
// queueing without end. Variables, so tests can change them.
var (
	hashSlots = make(chan struct{}, 4)
	hashWait  = 2 * time.Second
	// hashHook, when set, runs inside the slot (tests: to count or to slow).
	hashHook func()
)

// HoldHashSlots takes every free slot until release is called, so that
// checks wait and give up after wait: for tests outside this package.
func HoldHashSlots(wait time.Duration) (release func()) {
	slots, w := hashSlots, hashWait
	hashSlots, hashWait = make(chan struct{}, 1), wait
	hashSlots <- struct{}{}
	return func() { hashSlots, hashWait = slots, w }
}

// ErrBusy says that too many password checks are running: try again soon.
// It is no answer about the password.
var ErrBusy = errors.New("password: too many checks at once")

// argonKey runs argon2id in a slot.
func argonKey(password, salt []byte, passes, memory uint32, threads uint8, keyLen uint32) ([]byte, error) {
	slots := hashSlots
	t := time.NewTimer(hashWait)
	defer t.Stop()
	select {
	case slots <- struct{}{}:
	case <-t.C:
		return nil, ErrBusy
	}
	defer func() { <-slots }()
	if hashHook != nil {
		hashHook()
	}
	return argon2.IDKey(password, salt, passes, memory, threads, keyLen), nil
}

// HashPassword returns the password's hash in the usual encoded form
// ($argon2id$v=19$m=…,t=…,p=…$salt$hash), which carries its parameters, so
// stronger ones later still check old hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := argonKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches encoded, comparing in
// constant time. Its error is ErrBusy only.
func CheckPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, nil
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, nil
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, nil
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, nil
	}
	got, err := argonKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is checked against when a login names no user, so that answer
// takes as long as a wrong password and doesn't tell which names exist.
var dummyHash, _ = HashPassword("no user has this password, it only takes time")

// CheckNoUser spends the time of one password check. Its error is ErrBusy
// only.
func CheckNoUser(password string) error {
	_, err := CheckPassword(dummyHash, password)
	return err
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
