package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// An encrypted archive: a header, then the data in chunks of up to 1 MiB, so
// it never has to fit in memory. Each chunk is AES-256-GCM under a key that
// argon2id makes from the passphrase, its nonce the chunk's number and whether
// it is the last. So chunks can't be reordered or dropped, and a file cut off
// anywhere fails instead of restoring half of the data.
//
//	magic | salt 16 | time, memory KiB, threads uint32 | generation length uint8,
//	generation | nonce prefix 7 | { length uint32, sealed chunk } ...
//
// The generation is the day the passphrase was set: when several have been
// used, it says which one to try. It isn't secret, and it is authenticated
// with the rest of the header.

const magic = "EUMETRIA-BACKUP-1\n"

const (
	chunkSize  = 1 << 20
	argonTime  = 3
	argonMem   = 64 << 10 // KiB; the container has 256 MiB
	argonLanes = 1
	saltSize   = 16
	prefixSize = 7
	maxMem     = 1 << 20 // KiB: what a header may ask for
)

// ErrPassphrase means the passphrase doesn't open the archive, or the archive
// was changed: the two can't be told apart.
var ErrPassphrase = errors.New("wrong passphrase, or the file is damaged")

// ErrNotBackup means the file isn't an encrypted archive.
var ErrNotBackup = errors.New("not an encrypted Eumetria backup")

type header struct {
	salt, prefix    []byte
	time, mem, lane uint32
	generation      string
	raw             []byte
}

func (h *header) gcm(passphrase string) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), h.salt, h.time, h.mem, uint8(h.lane), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(prefix []byte, n uint32, last bool) []byte {
	out := make([]byte, 12)
	copy(out, prefix)
	binary.BigEndian.PutUint32(out[prefixSize:], n)
	if last {
		out[11] = 1
	}
	return out
}

type writer struct {
	w      io.Writer
	gcm    cipher.AEAD
	h      *header
	aad    []byte
	buf    []byte
	n      uint32
	closed bool
}

// NewWriter encrypts what is written to it into w. Close finishes the file.
func NewWriter(w io.Writer, passphrase, generation string) (io.WriteCloser, error) {
	if len(generation) > 255 {
		return nil, errors.New("generation too long")
	}
	h := &header{salt: make([]byte, saltSize), prefix: make([]byte, prefixSize), time: argonTime, mem: argonMem, lane: argonLanes, generation: generation}
	if _, err := rand.Read(h.salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(h.prefix); err != nil {
		return nil, err
	}
	raw := append([]byte(magic), h.salt...)
	raw = binary.BigEndian.AppendUint32(raw, h.time)
	raw = binary.BigEndian.AppendUint32(raw, h.mem)
	raw = binary.BigEndian.AppendUint32(raw, h.lane)
	raw = append(raw, byte(len(generation)))
	raw = append(raw, generation...)
	raw = append(raw, h.prefix...)
	gcm, err := h.gcm(passphrase)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &writer{w: w, gcm: gcm, h: h, aad: sum[:], buf: make([]byte, 0, chunkSize)}, nil
}

func (e *writer) flush(last bool) error {
	sealed := e.gcm.Seal(nil, nonce(e.h.prefix, e.n, last), e.buf, e.aad)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(sealed)))
	if _, err := e.w.Write(size[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(sealed); err != nil {
		return err
	}
	e.n++
	e.buf = e.buf[:0]
	return nil
}

func (e *writer) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		// A full chunk stays until more comes, so the last one is known.
		if len(e.buf) == chunkSize {
			if err := e.flush(false); err != nil {
				return 0, err
			}
		}
		n := min(chunkSize-len(e.buf), len(p))
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
	}
	return total, nil
}

func (e *writer) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

func readHeader(r *bufio.Reader) (*header, error) {
	raw := make([]byte, len(magic)+saltSize+12+1)
	if _, err := io.ReadFull(r, raw); err != nil || string(raw[:len(magic)]) != magic {
		return nil, ErrNotBackup
	}
	h := &header{salt: raw[len(magic) : len(magic)+saltSize]}
	rest := raw[len(magic)+saltSize:]
	h.time, h.mem, h.lane = binary.BigEndian.Uint32(rest), binary.BigEndian.Uint32(rest[4:]), binary.BigEndian.Uint32(rest[8:])
	if h.time < 1 || h.time > 64 || h.mem < 8 || h.mem > maxMem || h.lane < 1 || h.lane > 64 {
		return nil, ErrNotBackup
	}
	tail := make([]byte, int(rest[12])+prefixSize)
	if _, err := io.ReadFull(r, tail); err != nil {
		return nil, ErrNotBackup
	}
	h.generation = string(tail[:len(tail)-prefixSize])
	h.prefix = tail[len(tail)-prefixSize:]
	h.raw = append(raw, tail...)
	return h, nil
}

// Generation reads which passphrase an archive needs: the day it was set.
func Generation(r io.Reader) (string, error) {
	h, err := readHeader(bufio.NewReader(r))
	if err != nil {
		return "", err
	}
	return h.generation, nil
}

// Decrypt writes the archive in r to w. The output counts only without an
// error: what comes before a damaged chunk has been written by then.
func Decrypt(r io.Reader, w io.Writer, passphrase string) error {
	br := bufio.NewReaderSize(r, 1<<16)
	h, err := readHeader(br)
	if err != nil {
		return err
	}
	gcm, err := h.gcm(passphrase)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(h.raw)
	limit := uint32(chunkSize + gcm.Overhead())
	for n := uint32(0); ; n++ {
		var size [4]byte
		if _, err := io.ReadFull(br, size[:]); err != nil {
			return fmt.Errorf("%w: it ends early", ErrPassphrase)
		}
		length := binary.BigEndian.Uint32(size[:])
		if length < uint32(gcm.Overhead()) || length > limit {
			return ErrPassphrase
		}
		sealed := make([]byte, length)
		if _, err := io.ReadFull(br, sealed); err != nil {
			return fmt.Errorf("%w: it ends early", ErrPassphrase)
		}
		// The last chunk is the one nothing follows.
		_, peek := br.Peek(1)
		last := errors.Is(peek, io.EOF)
		plain, err := gcm.Open(nil, nonce(h.prefix, n, last), sealed, sum[:])
		if err != nil {
			return ErrPassphrase
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
		if last {
			return nil
		}
	}
}
