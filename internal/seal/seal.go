// Package seal encrypts the secrets the app keeps in its database with
// SETTINGS_KEY (AES-256-GCM): the settings' passwords and API keys
// (internal/settings) and the authenticator apps' shared secrets
// (internal/auth). A copy of the database or a backup alone then gives
// none of them away; the key isn't in either.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Prefix starts every sealed value; the version of the format.
const Prefix = "v1:"

var ErrBadKey = errors.New("SETTINGS_KEY must be 64 hex digits: openssl rand -hex 32")

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// FromHex makes a Box from SETTINGS_KEY as openssl rand -hex 32 makes it.
// An empty key is no Box (nil) and no error: secrets then stay unsealed.
func FromHex(hexKey string) (*Box, error) {
	if hexKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Sealed reports whether stored is a sealed value.
func Sealed(stored string) bool { return strings.HasPrefix(stored, Prefix) }

// Seal encrypts plain. context (e.g. the setting's name, or the user a
// secret belongs to) is authenticated with it, so a sealed value can't be
// moved to another row.
func (b *Box) Seal(plain, context string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return Prefix + base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), []byte(context))), nil
}

// Open decrypts what Seal made with the same context.
func (b *Box) Open(stored, context string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, Prefix))
	if err != nil {
		return "", err
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("too short")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(context))
	return string(plain), err
}
