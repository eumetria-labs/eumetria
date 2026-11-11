package auth

import (
	"sync"
	"time"
)

// Limiter counts failed sign-ins per key (an account name or an IP
// address). Max failures within Window lock the key for Lock; each further
// lockout within a day doubles that, up to MaxLock. With Max 0 it never
// locks and only counts (Failures). The counts live in memory: a restart
// forgets them, which costs a guesser a restart of ours.
//
// Reserve counts a try before it is checked, so parallel tries can't all
// pass the check before any of them counts (pentest F16).
type Limiter struct {
	Max     int
	Window  time.Duration
	Lock    time.Duration
	MaxLock time.Duration
	Now     func() time.Time

	mu sync.Mutex
	m  map[string]*limitState
}

type limitState struct {
	fails    int
	first    time.Time // of the failures counted now
	last     time.Time // the latest failure
	until    time.Time // locked until
	lockouts int       // within the last day
}

// NewLimiter returns a limiter with the clock of the machine.
func NewLimiter(max int, window, lock, maxLock time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, Lock: lock, MaxLock: maxLock, Now: time.Now}
}

// Blocked returns how long key stays locked; 0 means it may try.
func (l *Limiter) Blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.m[key]; s != nil {
		if d := s.until.Sub(l.Now()); d > 0 {
			return d
		}
	}
	return 0
}

// Fail counts a failure for key and locks it when it has too many.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fail(key)
}

// Failures is how many failures key has within the window.
func (l *Limiter) Failures(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.m[key]; s != nil && l.Now().Sub(s.first) <= l.Window {
		return s.fails
	}
	return 0
}

// Reservation is a try Reserve counted already; Done says how it went.
type Reservation struct {
	l      *Limiter
	key    string
	locked bool      // this try locked the key
	until  time.Time // the lock it set
}

// Reserve counts a try for key as a failure before it is made, or returns
// how long key stays locked (and a nil Reservation). Done(true) takes the
// failure back when the try worked.
func (l *Limiter) Reserve(key string) (*Reservation, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.m[key]; s != nil {
		if d := s.until.Sub(l.Now()); d > 0 {
			return nil, d
		}
	}
	s := l.fail(key)
	r := &Reservation{l: l, key: key}
	if s.until.After(l.Now()) {
		r.locked, r.until = true, s.until
	}
	return r, 0
}

// Done ends a reservation: a try that worked doesn't count as a failure.
func (r *Reservation) Done(ok bool) {
	if r == nil || !ok {
		return
	}
	l := r.l
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.m[r.key]
	if s == nil {
		return
	}
	switch {
	case r.locked && s.until.Equal(r.until):
		// The lock this try set, and nothing locked it again since.
		s.until, s.fails = time.Time{}, l.Max-1
		s.lockouts = max(0, s.lockouts-1)
	case s.fails > 0:
		s.fails--
	}
}

func (l *Limiter) fail(key string) *limitState {
	now := l.Now()
	if l.m == nil {
		l.m = map[string]*limitState{}
	}
	if len(l.m) > 10_000 {
		l.prune(now)
	}
	s := l.m[key]
	if s == nil {
		s = &limitState{}
		l.m[key] = s
	}
	if now.Sub(s.last) > 24*time.Hour {
		s.lockouts = 0
	}
	if now.Sub(s.first) > l.Window {
		s.fails, s.first = 0, now
	}
	s.fails++
	s.last = now
	if l.Max > 0 && s.fails >= l.Max {
		d := l.Lock << s.lockouts
		if d > l.MaxLock || d <= 0 {
			d = l.MaxLock
		}
		s.lockouts++
		s.until = now.Add(d)
		s.fails, s.first = 0, now
	}
	return s
}

// Reset forgets key's failures, after it signed in.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// prune drops keys that are neither locked nor failed within a day, so a
// flood of made-up names can't fill the memory.
func (l *Limiter) prune(now time.Time) {
	for k, s := range l.m {
		if now.After(s.until) && now.Sub(s.last) > 24*time.Hour {
			delete(l.m, k)
		}
	}
}
