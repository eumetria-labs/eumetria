package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewToken returns a random token (32 bytes, base64url) and the hash the
// database keeps instead of it. Someone who reads the database can't use
// what's in it, and a token is never shown again after it was handed out.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is the database key of a token. Tokens are random and long, so
// a plain SHA-256 is enough; they need no slow hash like passwords do.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// API tokens start with this, so they are easy to recognise (and to find
// with secret scanners) when one ends up somewhere it shouldn't.
const APITokenPrefix = "eum_"
