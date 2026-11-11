package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubnet(t *testing.T) {
	for ip, want := range map[string]string{
		"203.0.113.7":          "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"not an ip":            "not an ip",
	} {
		if got := subnet(ip); got != want {
			t.Errorf("subnet(%s) = %s, want %s", ip, got, want)
		}
	}
}

func (e *testEnv) wrongLogin(t *testing.T, name, ip string) int {
	t.Helper()
	res, _ := e.do(t, e.client(), "POST", "/api/login", `{"name":"`+name+`","password":"wrong wrong wrong"}`, sameSite, header("X-Real-IP", ip))
	return res.StatusCode
}

// Wrong passwords lock a name only for the network they came from: a
// stranger can't lock its owner out (pentest F2).
func TestALockedNameStillSignsInElsewhere(t *testing.T) {
	e := newTestEnv(t, true)
	for range 5 {
		e.wrongLogin(t, "anna", "203.0.113.7")
	}
	if code := e.wrongLogin(t, "anna", "203.0.113.99"); code != 429 {
		t.Errorf("same /24 after 5 failures = %d, want 429", code)
	}
	res, body := e.do(t, e.client(), "POST", "/api/login", `{"name":"anna","password":"`+viewerPassword+`"}`, sameSite, header("X-Real-IP", "198.51.100.1"))
	if res.StatusCode != 200 {
		t.Errorf("anna from another network = %d %s", res.StatusCode, body)
	}
}

// Many failures for one name from many networks slow it down and show up
// in the audit log once.
func TestNamePressure(t *testing.T) {
	step, most := slowStep, slowMax
	slowStep, slowMax = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { slowStep, slowMax = step, most })
	e := newTestEnv(t, true)
	for i := range pressureAlert + 5 {
		// 4 per network: below the name lock, below the address lock.
		if code := e.wrongLogin(t, "patrick", fmt.Sprintf("198.51.%d.%d", i/4, i%4+1)); code != 401 {
			t.Fatalf("try %d = %d, want 401: never locked for everyone", i, code)
		}
	}
	log, _ := e.store.AuditLog(context.Background(), 1000)
	if n := strings.Count(jsonOf(t, log), "login_pressure"); n != 1 {
		t.Errorf("%d login_pressure events, want 1", n)
	}
	// The owner still gets in.
	e.signIn(t, "patrick", adminPassword, false)
}

// Parallel wrong passwords: only the limit's worth are checked (pentest F16).
func TestParallelGuessesStopAtTheLimit(t *testing.T) {
	e := newTestEnv(t, true)
	var mu sync.Mutex
	var wg sync.WaitGroup
	codes := map[int]int{}
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code := e.wrongLogin(t, "anna", "203.0.113.7")
			mu.Lock()
			codes[code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[401] != 5 || codes[429] != 35 {
		t.Errorf("answers = %v, want 5×401 and 35×429", codes)
	}
}

// X-Real-IP counts only from the front proxy: with TRUSTED_PROXY, only
// from what it names (pentest F8).
func TestTrustedProxy(t *testing.T) {
	t.Cleanup(func() { setTrustedProxies("") })
	ask := func(remote string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote + ":5000"
		req.Header.Set("X-Real-IP", "203.0.113.9")
		return clientIP(req)
	}
	// Without it: loopback and private addresses, as before.
	for remote, want := range map[string]string{"127.0.0.1": "203.0.113.9", "172.30.90.1": "203.0.113.9", "198.51.100.1": "198.51.100.1"} {
		if got := ask(remote); got != want {
			t.Errorf("unset, from %s: %s, want %s", remote, got, want)
		}
	}
	if err := setTrustedProxies("172.30.90.1, ::1"); err != nil {
		t.Fatal(err)
	}
	for remote, want := range map[string]string{"172.30.90.1": "203.0.113.9", "172.30.90.2": "172.30.90.2", "127.0.0.1": "127.0.0.1"} {
		if got := ask(remote); got != want {
			t.Errorf("TRUSTED_PROXY set, from %s: %s, want %s", remote, got, want)
		}
	}
	if err := setTrustedProxies("10.0.0.0/8"); err != nil || ask("10.1.2.3") != "203.0.113.9" {
		t.Errorf("a network: %v", err)
	}
	if err := setTrustedProxies("nginx"); err == nil {
		t.Error("a name was taken as an address")
	}
}
