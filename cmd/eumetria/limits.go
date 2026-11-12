package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"eumetria/internal/auth"
)

// Limits on guessing (pentest F2, F16, F20).
//
// A wrong password or second factor counts against the address (byIP: 20
// lock it) and against the account name from that network (byName: 5 lock
// the name for that /24 or /64 only). Anyone can type any name, so a name
// is never locked for everyone: that would let a stranger lock its owner
// out. Instead, many failures for one name from anywhere slow its sign-ins
// down a little (nameLoad), and from pressureAlert on the audit log says
// so, once per window.
//
// A try is counted before it is checked (auth.Limiter.Reserve), so parallel
// requests can't all get past the check before any of them counts.
//
// The per-network lock alone puts no cap on guesses from many networks
// (pentest re-test N2). So once a name is under pressure (more than
// slowAfter failures in the window), sign-ins for it from browsers that
// never signed in to it (no device cookie) run one at a time (nameGate)
// and share unknownBudget tries an hour, from wherever they come. The
// owner's own browsers carry the cookie and are not held up.

const (
	slowAfter = 10 // failures for a name within the window before it slows down
	// failures for a name within the window that go to the audit log: below
	// slowAfter+unknownBudget, which is about the most an attacker gets.
	pressureAlert = 25
)

// unknownBudget is how many sign-ins an hour a name under pressure gets
// from browsers without its device cookie; gateWait how long one waits for
// its turn.
const (
	unknownBudget = 20
	gateWait      = 10 * time.Second
)

// How much longer each further failure makes a sign-in wait, and the most.
// Variables, so tests can make them short.
var (
	slowStep = 250 * time.Millisecond
	slowMax  = 5 * time.Second
)

// subnet is the network an address belongs to for the name limit: a /24
// for IPv4, a /64 for IPv6 (what one household or host usually has).
func subnet(ip string) string {
	addr := net.ParseIP(ip)
	switch {
	case addr == nil:
		return ip
	case addr.To4() != nil:
		return addr.Mask(net.CIDRMask(24, 32)).String() + "/24"
	default:
		return addr.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
}

func nameKey(name, ip string) string { return "name:" + strings.ToLower(name) + "|" + subnet(ip) }
func loadKey(name string) string     { return "name:" + strings.ToLower(name) }

// attempt is one counted try (Reserve) against several limits.
type attempt []*auth.Reservation

// done says how the try went: one that worked doesn't count.
func (t attempt) done(ok bool) {
	for _, r := range t {
		r.Done(ok)
	}
}

type limitKey struct {
	l   *auth.Limiter
	key string
}

// try counts a try for the address and, with a name, for that name from the
// address's network, and for the extra keys (in byName). When one of them
// is locked it answers 429 itself and returns false.
func (a *app) try(w http.ResponseWriter, ip, name string, extra ...string) (attempt, bool) {
	keys := []limitKey{{a.byIP, "ip:" + ip}}
	if name != "" {
		keys = append(keys, limitKey{a.byName, nameKey(name, ip)})
	}
	for _, k := range extra {
		keys = append(keys, limitKey{a.byName, k})
	}
	var t attempt
	for _, k := range keys {
		r, wait := k.l.Reserve(k.key)
		if wait > 0 {
			t.done(true) // the ones reserved so far don't count
			locked(w, wait)
			return nil, false
		}
		t = append(t, r)
	}
	return t, true
}

// blocked answers 429 when the address is locked, without counting a try.
func (a *app) blocked(w http.ResponseWriter, ip string) bool {
	if d := a.byIP.Blocked("ip:" + ip); d > 0 {
		locked(w, d)
		return true
	}
	return false
}

func locked(w http.ResponseWriter, d time.Duration) {
	secs := int(d.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "locked", "retry_after": secs})
}

// slowDown makes a sign-in for name wait a little when it failed many
// times lately, from wherever.
func (a *app) slowDown(ctx context.Context, name string) {
	n := a.nameLoad.Failures(loadKey(name))
	if n <= slowAfter {
		return
	}
	d := min(slowMax, time.Duration(n-slowAfter)*slowStep)
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// pressured reports whether name failed more than slowAfter times lately,
// from anywhere.
func (a *app) pressured(name string) bool { return a.nameLoad.Failures(loadKey(name)) > slowAfter }

// unknownTry counts a sign-in for a pressured name from an unknown device
// against the name's budget for those (returned, for done), or answers 429
// itself when that is used up.
func (a *app) unknownTry(w http.ResponseWriter, name string) (attempt, bool) {
	r, wait := a.unknownDevice.Reserve(loadKey(name))
	if wait > 0 {
		locked(w, wait)
		return nil, false
	}
	return attempt{r}, true
}

/* ---------- known devices ---------- */

// deviceCookie marks a browser that signed in to an account before
// (auth.DeviceToken). It is HttpOnly and only says which account; it signs
// nobody in.
const deviceCookie = "__Host-eumetria_device"

// rememberDevice hands the browser a device cookie for u after a sign-in.
func (a *app) rememberDevice(w http.ResponseWriter, ctx context.Context, u *auth.User) {
	token, err := a.store.DeviceToken(ctx, u)
	if err != nil {
		log.Printf("device cookie: %v", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: token, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(auth.DeviceMax.Seconds())})
}

// knownDevice reports whether the request carries a genuine device cookie
// for the account called name.
func (a *app) knownDevice(req *http.Request, name string) bool {
	c, err := req.Cookie(deviceCookie)
	if err != nil {
		return false
	}
	id := a.store.DeviceUser(req.Context(), c.Value)
	if id == 0 {
		return false
	}
	u, err := a.store.UserByID(req.Context(), id)
	return err == nil && strings.EqualFold(u.Name, name)
}

/* ---------- one at a time ---------- */

// gates lets one holder per key in at a time; the others wait their turn.
type gates struct {
	mu sync.Mutex
	m  map[string]*gate
}

type gate struct {
	ch    chan struct{}
	users int // holding or waiting; the gate goes when none are left
}

// enter waits up to wait for key's turn; false when it didn't come.
func (g *gates) enter(ctx context.Context, key string, wait time.Duration) bool {
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]*gate{}
	}
	e := g.m[key]
	if e == nil {
		e = &gate{ch: make(chan struct{}, 1)}
		g.m[key] = e
	}
	e.users++
	g.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case e.ch <- struct{}{}:
		return true
	case <-t.C:
	case <-ctx.Done():
	}
	g.done(key, e)
	return false
}

// leave ends key's turn; only after enter returned true.
func (g *gates) leave(key string) {
	g.mu.Lock()
	e := g.m[key]
	g.mu.Unlock()
	<-e.ch
	g.done(key, e)
}

func (g *gates) done(key string, e *gate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e.users--; e.users == 0 {
		delete(g.m, key)
	}
}

// nameFailed counts a failure for name from anywhere; the one that reaches
// pressureAlert goes to the audit log, for the admins to see.
func (a *app) nameFailed(ctx context.Context, name, ip string) {
	key := loadKey(name)
	a.nameLoad.Fail(key)
	if a.nameLoad.Failures(key) == pressureAlert {
		a.audit_(ctx, nil, truncate(name, 64), "login_pressure", strconv.Itoa(pressureAlert)+" failures in 15 min", ip)
		// The account's owner hears of it by mail, at most once a day.
		if u, err := a.store.UserByName(ctx, name); err == nil && a.mailDaily.Failures("pressure:"+strconv.FormatInt(u.ID, 10)) == 0 {
			a.mailDaily.Fail("pressure:" + strconv.FormatInt(u.ID, 10))
			a.alert(ctx, u, "login_pressure", "", "")
		}
	}
}

// auditLoop keeps the audit log from growing without bound (pentest F20):
// at start and every night it drops what is older than auth.AuditKeep and
// all but the newest auth.AuditMaxRows entries.
func auditLoop(ctx context.Context, s *auth.Store) {
	for {
		n, err := s.PruneAudit(ctx)
		if err != nil {
			log.Printf("audit log: %v", err)
		} else if n > 0 {
			log.Printf("audit log: removed %d old entries", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextAt(time.Now(), 3, 20))):
		}
	}
}

/* ---------- the front proxy ---------- */

// trustedProxies are the addresses whose X-Real-IP is believed: the front
// proxy (nginx), from TRUSTED_PROXY (setTrustedProxies). Empty: any
// loopback or private address, which is how Docker's port forwarding
// delivers nginx's requests (the bridge's gateway, e.g. 172.17.0.1); the
// container's port is bound to 127.0.0.1 on the host, so nothing else gets
// there, but another container on the same network could (pentest F8).
var trustedProxies []netip.Prefix

// setTrustedProxies reads TRUSTED_PROXY: addresses or networks, separated
// by commas or spaces.
func setTrustedProxies(list string) error {
	trustedProxies = nil
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		p, err := netip.ParsePrefix(f)
		if err != nil {
			a, aerr := netip.ParseAddr(f)
			if aerr != nil {
				return fmt.Errorf("TRUSTED_PROXY: %q is no address or network", f)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		trustedProxies = append(trustedProxies, p.Masked())
	}
	return nil
}

var warnedProxy atomic.Bool

// fromProxy reports whether the request came through the front proxy, whose
// X-Real-IP is then believed.
func fromProxy(req *http.Request) bool {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if len(trustedProxies) == 0 {
		return addr.IsLoopback() || addr.IsPrivate()
	}
	for _, p := range trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	// Someone else sends X-Real-IP: a wrong TRUSTED_PROXY, most likely. Say
	// so once, so it shows in the log.
	if req.Header.Get("X-Real-IP") != "" && warnedProxy.CompareAndSwap(false, true) {
		log.Printf("WARNING: X-Real-IP from %s, which TRUSTED_PROXY doesn't name: ignored. Is TRUSTED_PROXY nginx's address as the container sees it?", addr)
	}
	return false
}
