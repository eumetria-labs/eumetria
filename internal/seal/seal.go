// Package seal encrypts stats.json with a shared password, so only people who
// know the password can read it, although the file itself is served to anyone.
// The page decrypts it in the browser with WebCrypto (site/js/lock.js).
//
// The key is PBKDF2-SHA256(password, salt) and the data AES-256-GCM. The salt
// is fixed rather than random per build: the key then stays the same between
// syncs, so a browser that remembers the key keeps working until the password
// changes. A fixed, site-specific salt still defeats precomputed tables; it
// only lets one guess be tested against every build at once, which matters
// little here because every build holds the same data.
package seal

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Iterations follows OWASP's 2023 recommendation for PBKDF2-SHA256. Phones
// derive the key once per password entry, not per page load.
const Iterations = 600_000

// Changing the salt or the name namespace changes every key: every
// remembered login stops working and every sealed file gets a new name.
var salt = []byte("eumetria/stats.json/v1")

// Envelope is the encrypted stats.json. Its "sealed" field is how the page
// tells it apart from plain stats.
type Envelope struct {
	Sealed     int    `json:"sealed"` // format version
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"` // base64, like IV and Data
	IV         []byte `json:"iv"`
	// Zip is "gzip" when the plaintext was compressed before encrypting.
	// Ciphertext doesn't compress, so the web server's gzip can't help.
	Zip  string `json:"zip,omitempty"`
	Data []byte `json:"data"` // ciphertext followed by the GCM tag
}

// Seal encrypts plain with password.
func Seal(plain []byte, password string) (*Envelope, error) {
	s, err := New(password)
	if err != nil {
		return nil, err
	}
	return s.Seal(plain)
}

// Sealer seals many files with one password and derives the key only once,
// which takes a noticeable fraction of a second.
type Sealer struct {
	gcm cipher.AEAD
	ns  []byte // see Name
}

// New derives the key for password.
func New(password string) (*Sealer, error) {
	if password == "" {
		return nil, errors.New("empty password")
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, Iterations, 32)
	if err != nil {
		return nil, err
	}
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	ns := sha256.Sum256(append([]byte("eumetria/name/"), key...))
	return &Sealer{gcm: gcm, ns: ns[:]}, nil
}

// Name returns a content hash for plain to name its sealed file by. It
// depends on the key, so a new password gives every file a new name (and
// the old files, readable only with the old key, get replaced), and it
// reveals nothing about the content.
func (s *Sealer) Name(plain []byte) []byte {
	h := sha256.New()
	h.Write(s.ns)
	h.Write(plain)
	return h.Sum(nil)
}

// Seal compresses plain and encrypts it with a fresh IV.
func (s *Sealer) Seal(plain []byte) (*Envelope, error) {
	var z bytes.Buffer
	zw := gzip.NewWriter(&z)
	if _, err := zw.Write(plain); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	iv := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return &Envelope{
		Sealed:     1,
		KDF:        "PBKDF2-SHA256",
		Iterations: Iterations,
		Salt:       salt,
		IV:         iv,
		Zip:        "gzip",
		Data:       s.gcm.Seal(nil, iv, z.Bytes(), nil),
	}, nil
}

// SealJSON marshals v and seals it.
func (s *Sealer) SealJSON(v any) (*Envelope, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.Seal(plain)
}

// Open decrypts e. The page does the same in JavaScript; Open exists for tests
// and for checking a deployed file by hand.
func Open(e *Envelope, password string) ([]byte, error) {
	if e.Sealed != 1 || e.KDF != "PBKDF2-SHA256" {
		return nil, fmt.Errorf("unsupported envelope (sealed=%d, kdf=%q)", e.Sealed, e.KDF)
	}
	gcm, err := newGCM(password, e.Salt, e.Iterations)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, e.IV, e.Data, nil)
	if err != nil {
		return nil, errors.New("wrong password or damaged file")
	}
	switch e.Zip {
	case "":
		return plain, nil
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(plain))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(zr)
	default:
		return nil, fmt.Errorf("unsupported compression %q", e.Zip)
	}
}

// SealJSON marshals v and seals it.
func SealJSON(v any, password string) (*Envelope, error) {
	s, err := New(password)
	if err != nil {
		return nil, err
	}
	return s.SealJSON(v)
}

func newGCM(password string, salt []byte, iter int) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iter, 32)
	if err != nil {
		return nil, err
	}
	return gcmFor(key)
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
