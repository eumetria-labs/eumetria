package auth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withSlots runs the test with n hash slots, a short wait and hook in each
// hash, and puts the defaults back afterwards.
func withSlots(t *testing.T, n int, wait time.Duration, hook func()) {
	t.Helper()
	slots, w, h := hashSlots, hashWait, hashHook
	hashSlots, hashWait, hashHook = make(chan struct{}, n), wait, hook
	t.Cleanup(func() { hashSlots, hashWait, hashHook = slots, w, h })
}

// Pentest re-test N1: never more than cap(hashSlots) hashes at once, and
// the rest give up with ErrBusy instead of piling up.
func TestHashSlotsCapConcurrency(t *testing.T) {
	var running, most atomic.Int32
	withSlots(t, 3, 50*time.Millisecond, func() {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		running.Add(-1)
	})
	var wg sync.WaitGroup
	var busy atomic.Int32
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := CheckNoUser("whatever"); errors.Is(err, ErrBusy) {
				busy.Add(1)
			} else if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most.Load() > 3 {
		t.Errorf("%d hashes ran at once, want at most 3", most.Load())
	}
	if busy.Load() == 0 {
		t.Error("64 parallel checks with 3 slow slots: none was told busy")
	}
}

func TestAuthenticateBusy(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.CreateUser(ctx, "patrick", goodPassword, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	withSlots(t, 1, 10*time.Millisecond, nil)
	hashSlots <- struct{}{} // all taken
	defer func() { <-hashSlots }()
	for _, name := range []string{"patrick", "nobody"} {
		if _, err := s.Authenticate(ctx, name, goodPassword); !errors.Is(err, ErrBusy) {
			t.Errorf("Authenticate(%s) with no free slot = %v, want ErrBusy", name, err)
		}
	}
	if _, err := s.CreateUser(ctx, "anna", goodPassword+"!", RoleViewer); !errors.Is(err, ErrBusy) {
		t.Errorf("CreateUser with no free slot = %v, want ErrBusy", err)
	}
}
