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

const (
	slowAfter     = 10 // failures for a name within the window before it slows down
	pressureAlert = 50 // failures for a name within the window that go to the audit log
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
