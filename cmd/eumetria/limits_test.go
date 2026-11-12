package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"eumetria/internal/auth"
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

// Many failures for one name from many networks: from browsers that never
// signed in to it, only a budget's worth are checked, one at a time, and
// the audit log says so once. The owner's own browser still gets in
// (pentest F2, re-test N2).
func TestNamePressure(t *testing.T) {
	step, most := slowStep, slowMax
	slowStep, slowMax = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { slowStep, slowMax = step, most })
	e := newTestEnv(t, true)
	owner := e.signIn(t, "anna", viewerPassword, false) // now carries the device cookie

	codes := map[int]int{}
	for i := range 100 {
		// 4 per network: below the name lock, below the address lock.
		codes[e.wrongLogin(t, "anna", fmt.Sprintf("198.51.%d.%d", i/4, i%4+1))]++
	}
	if checked := codes[401]; checked > slowAfter+1+unknownBudget || codes[429] == 0 {
		t.Errorf("100 guesses from 25 networks: %v, want at most %d checked", codes, slowAfter+1+unknownBudget)
	}
	log, _ := e.store.AuditLog(context.Background(), 1000)
	if n := strings.Count(jsonOf(t, log), "login_pressure"); n != 1 {
		t.Errorf("%d login_pressure events, want 1", n)
	}
	right := `{"name":"anna","password":"` + viewerPassword + `"}`
	// Even the right password from a new browser waits for the hour now…
	if res, _ := e.do(t, e.client(), "POST", "/api/login", right, sameSite, header("X-Real-IP", "192.0.2.1")); res.StatusCode != 429 {
		t.Errorf("right password from an unknown device under attack = %d, want 429", res.StatusCode)
	}
	// …and a cookie made for someone else or forged doesn't help…
	for _, value := range []string{"1.2.3", "garbage", deviceTokenFor(t, e, "coach")} {
		c := e.client()
		c.Jar.SetCookies(mustURL(t, e.srv.URL), []*http.Cookie{{Name: deviceCookie, Value: value, Path: "/"}})
		if res, _ := e.do(t, c, "POST", "/api/login", right, sameSite, header("X-Real-IP", "192.0.2.2")); res.StatusCode != 429 {
			t.Errorf("device cookie %q for anna = %d, want 429", value, res.StatusCode)
		}
	}
	// …but the owner's browser goes on as usual, at once.
	start := time.Now()
	if res, body := e.do(t, owner, "POST", "/api/login", right, sameSite, header("X-Real-IP", "192.0.2.3")); res.StatusCode != 200 {
		t.Errorf("owner's browser under attack = %d %s", res.StatusCode, body)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("owner's browser waited %v", d)
	}
}

func deviceTokenFor(t *testing.T, e *testEnv, name string) string {
	u, err := e.store.UserByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	token, err := e.store.DeviceToken(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func mustURL(t *testing.T, raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A new password makes older device cookies worthless.
func TestDeviceTokenEndsWithPassword(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	u, _ := e.store.UserByName(ctx, "anna")
	token := deviceTokenFor(t, e, "anna")
	if e.store.DeviceUser(ctx, token) != u.ID {
		t.Fatal("a fresh device token isn't taken")
	}
	e.now = e.now.Add(time.Second)
	if err := e.store.SetPassword(ctx, u, "a brand new long password", 0, auth.Revoke{}); err != nil {
		t.Fatal(err)
	}
	if e.store.DeviceUser(ctx, token) != 0 {
		t.Error("a device token outlives a new password")
	}
	e.now = e.now.Add(auth.DeviceMax + time.Hour)
	if e.store.DeviceUser(ctx, deviceTokenFor(t, e, "anna")) == 0 {
		t.Error("a fresh token at a later time isn't taken")
	}
}

// Under pressure, sign-ins from unknown devices run one at a time.
func TestNameGate(t *testing.T) {
	var g gates
	ctx := context.Background()
	if !g.enter(ctx, "a", time.Second) {
		t.Fatal("an empty gate didn't let in")
	}
	if g.enter(ctx, "a", 20*time.Millisecond) {
		t.Error("a second holder got in")
	}
	if !g.enter(ctx, "b", time.Second) {
		t.Error("another key had to wait")
	}
	g.leave("b")
	go func() { time.Sleep(20 * time.Millisecond); g.leave("a") }()
	if !g.enter(ctx, "a", time.Second) {
		t.Error("the next one didn't get its turn")
	}
	g.leave("a")
	if len(g.m) != 0 {
		t.Errorf("gates left behind: %v", g.m)
	}
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

// Pentest re-test N1: with every password check slot taken a sign-in is
// answered 503 at once, and it counts as no failure, so a flood can't use
// up the owner's lock either.
func TestBusySignInCountsNothing(t *testing.T) {
	e := newTestEnv(t, true)
	release := auth.HoldHashSlots(10 * time.Millisecond)
	for range 10 {
		res, body := e.do(t, e.client(), "POST", "/api/login", `{"name":"anna","password":"wrong wrong wrong"}`, sameSite, header("X-Real-IP", "203.0.113.7"))
		if res.StatusCode != 503 || !strings.Contains(body, "busy") || res.Header.Get("Retry-After") == "" {
			t.Fatalf("sign-in with no free slot = %d %s", res.StatusCode, body)
		}
	}
	release()
	res, body := e.do(t, e.client(), "POST", "/api/login", `{"name":"anna","password":"`+viewerPassword+`"}`, sameSite, header("X-Real-IP", "203.0.113.7"))
	if res.StatusCode != 200 {
		t.Errorf("anna after 10 busy answers = %d %s, want 200", res.StatusCode, body)
	}
}
