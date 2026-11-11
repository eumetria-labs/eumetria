package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"rsc.io/qr"

	"eumetria/internal/auth"
)

// Second factors: passkeys and security keys (WebAuthn), an authenticator
// app (TOTP) and recovery codes.
//
// Signing in with a password and a second factor takes two requests: the
// password earns a ticket (5 minutes, 5 tries), the second factor turns the
// ticket into a session. A passkey that verified its user (fingerprint,
// face, PIN) signs in on its own, without the password.
//
// Changing one's factors needs a recent proof of who one is: the sign-in, or
// confirming it again (stepup.go). Admins without a second factor can read
// but not sync or see the audit log.

const (
	ticketTTL = 5 * time.Minute
	freshFor  = 5 * time.Minute // how long a sign-in or step-up counts as recent
)

/* ---------- what lives between two requests ---------- */

// pending is a ticket or a WebAuthn ceremony, kept in memory between the
// request that starts it and the one that finishes it. A restart forgets
// them, which only means starting over.
type pending struct {
	kind     pendingKind
	until    time.Time
	ip       string // who started it, when that was before any sign-in
	user     *auth.User
	remember bool
	tries    int
	wa       *webauthn.SessionData // the challenge of a WebAuthn ceremony
	secret   string                // a TOTP secret waiting for its first code
	name     string                // a key's name waiting for its registration
	keyOnly  bool                  // a security key for the second step only, not a passkey
	enrol    string                // the enrolment link a first factor is set up with
}

// pendingKind tells what a pending entry is. Each request only finds the
// kind it works with: a sign-in that starts with a passkey can't take (and
// so cancel) someone's TOTP set-up, nor use a Google sign-in's state as a
// ticket.
type pendingKind int

const (
	pendingTicket       pendingKind = iota + 1 // the password was right; a second factor follows
	pendingPasskeyLogin                        // a sign-in with a passkey alone
	pendingKeyAdd                              // a key being added to a signed-in account
	pendingTOTPAdd                             // an authenticator app being set up
	pendingGoogle                              // the state of a Google sign-in
	pendingReauth                              // a key confirming who a signed-in user is
)

// How many entries the store keeps: in all, and per address for the ones
// anyone can start without signing in. Each one is small, but a passkey
// sign-in needs nothing to begin.
const (
	pendingMax      = 10_000
	pendingMaxPerIP = 20
)

var errPendingFull = errors.New("too many sign-ins in progress")

type pendingStore struct {
	mu sync.Mutex
	m  map[string]*pending
}

// put keeps p as kind under key, or under a new random id when key is "".
// ip, when not "", counts it against the address that started it.
func (s *pendingStore) put(key string, kind pendingKind, ip string, p *pending) (string, error) {
	if key == "" {
		b := make([]byte, 24)
		rand.Read(b)
		key = base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now()
	p.kind, p.ip, p.until = kind, ip, now.Add(ticketTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*pending{}
	}
	fromIP := 0
	for k, x := range s.m {
		if now.After(x.until) {
			delete(s.m, k)
		} else if ip != "" && x.ip == ip {
			fromIP++
		}
	}
	if _, replaces := s.m[key]; !replaces && (len(s.m) >= pendingMax || ip != "" && fromIP >= pendingMaxPerIP) {
		return "", errPendingFull
	}
	s.m[key] = p
	return key, nil
}

// entry is what is kept under key, if it is of kind and still valid. The
// caller holds the lock.
func (s *pendingStore) entry(key string, kind pendingKind) *pending {
	p := s.m[key]
	if p == nil || p.kind != kind {
		return nil
	}
	if time.Now().After(p.until) {
		delete(s.m, key)
		return nil
	}
	return p
}

// get returns a copy of what is kept under key as kind, or nil.
func (s *pendingStore) get(key string, kind pendingKind) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entry(key, kind)
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

// take is get, and removes it. Something of another kind under key stays.
func (s *pendingStore) take(key string, kind pendingKind) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entry(key, kind)
	if p != nil {
		delete(s.m, key)
	}
	return p
}

// set changes what is kept under key as kind, keeping its expiry.
func (s *pendingStore) set(key string, kind pendingKind, update func(*pending)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.entry(key, kind); p != nil {
		update(p)
	}
}

// try counts a try of a ticket before it is checked, so parallel tries can't
// all get past the limit (pentest F16). The sixth ends it.
func (s *pendingStore) try(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entry(key, pendingTicket)
	if p == nil {
		return false
	}
	if p.tries++; p.tries > 5 {
		delete(s.m, key)
		return false
	}
	return true
}

// failed ends a ticket once five tries failed.
func (s *pendingStore) failed(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.entry(key, pendingTicket); p != nil && p.tries >= 5 {
		delete(s.m, key)
	}
}

// pendingSalt keys a session's set-ups, so nobody can name them from the
// session's number.
var pendingSalt = func() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}()

// sessionKey is where the session's set-up of what is kept.
func sessionKey(what string, p *principal) string {
	m := hmac.New(sha256.New, pendingSalt)
	m.Write([]byte(what + ":" + strconv.FormatInt(p.session.ID, 10)))
	return what + ":" + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

/* ---------- WebAuthn ---------- */

// relyingParty is the WebAuthn configuration for the site: its host is the
// relying party ID, its origin the only one accepted. That is the public
// URL from the settings; without one, the site as the request names it
// (siteURL). Browsers allow WebAuthn only over https, or on plain
// http://localhost, so that decides the scheme; behind a proxy that ends
// TLS (nginx) the request itself is plain http and can't tell.
func (a *app) relyingParty(req *http.Request) (*webauthn.WebAuthn, error) {
	// The public URL from the settings, or the request's own address.
	origin := a.siteURL(req)
	u, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if rp, ok := a.relyingParties.Load(origin); ok {
		return rp.(*webauthn.WebAuthn), nil
	}
	rp, err := webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "Eumetria", RPOrigins: []string{origin}})
	if err != nil {
		return nil, err
	}
	a.relyingParties.Store(origin, rp)
	return rp, nil
}

// waUser is a user as go-webauthn wants it.
type waUser struct {
	u      *auth.User
	handle []byte
	keys   []auth.Key
}

func (w *waUser) WebAuthnID() []byte          { return w.handle }
func (w *waUser) WebAuthnName() string        { return w.u.Name }
func (w *waUser) WebAuthnDisplayName() string { return w.u.Name }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(w.keys))
	for i, k := range w.keys {
		out[i] = k.Credential
	}
	return out
}

func (a *app) waUser(ctx context.Context, u *auth.User) (*waUser, error) {
	handle, err := a.store.WebAuthnHandle(ctx, u)
	if err != nil {
		return nil, err
	}
	keys, err := a.store.Keys(ctx, u)
	// The clone warning says something about one sign-in, not about the key
	// for good: go-webauthn sets it when the counter doesn't go up, and
	// keys saved before it was checked may carry an old one (cloned).
	for i := range keys {
		keys[i].Credential.Authenticator.CloneWarning = false
	}
	return &waUser{u: u, handle: handle, keys: keys}, err
}

/* ---------- signing in ---------- */

// methods are the second factors u can sign in with.
func methods(f auth.Factors) []string {
	var m []string
	if f.Keys > 0 {
		m = append(m, "key")
	}
	if f.TOTP {
		m = append(m, "totp")
	}
	if f.RecoveryLeft > 0 {
		m = append(m, "recovery")
	}
	return m
}

// signIn starts a session for u (replacing one this browser may still
// have) and answers with who is signed in.
func (a *app) signIn(w http.ResponseWriter, req *http.Request, u *auth.User, remember bool, how string) {
	if a.startSession(w, req, u, remember, how) {
		a.writeMe(w, req.Context(), u, "", remember)
	}
}

// startSession is signIn without the answer; false when it has answered
// with an error.
func (a *app) startSession(w http.ResponseWriter, req *http.Request, u *auth.User, remember bool, how string) bool {
	ctx := req.Context()
	if c, err := req.Cookie(sessionCookie); err == nil {
		if old, ou, err := a.store.Session(ctx, c.Value); err == nil && ou.ID == u.ID {
			a.store.DeleteSession(ctx, u, old.ID)
		}
	}
	ip := clientIP(req)
	token, _, err := a.store.CreateSession(ctx, u, remember, ip, req.UserAgent())
	if err != nil {
		log.Printf("login: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return false
	}
	a.byName.Reset(nameKey(u.Name, ip))
	setSessionCookie(w, token, remember)
	a.audit_(ctx, u, "", "login", how, ip)
	return true
}

type ticketRequest struct {
	Ticket string `json:"ticket"`
	Code   string `json:"code,omitempty"`
	Kind   string `json:"kind,omitempty"` // totp or recovery
}

// loginCode finishes a sign-in with a code from the app or a recovery code.
func (a *app) loginCode(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	var in ticketRequest
	if !readJSON(w, req, &in) {
		return
	}
	t := a.pending.get(in.Ticket, pendingTicket)
	if t == nil || t.user == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	if in.Kind != "totp" && in.Kind != "recovery" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	ip := clientIP(req)
	tries, ok := a.secondFactorTry(w, in.Ticket, t.user, ip)
	if !ok {
		return
	}
	var err error
	if in.Kind == "totp" {
		ok, err = a.store.UseTOTP(ctx, t.user, in.Code)
	} else {
		ok, err = a.store.UseRecoveryCode(ctx, t.user, in.Code)
	}
	tries.done(ok || err != nil)
	if err != nil {
		log.Printf("login: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if !ok {
		a.secondFactorFailed(ctx, in.Ticket, t.user, in.Kind, ip)
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.pending.take(in.Ticket, pendingTicket)
	if in.Kind == "recovery" {
		f, _ := a.store.Factors(ctx, t.user)
		a.audit_(ctx, t.user, "", "recovery_code_used", strconv.Itoa(f.RecoveryLeft)+" left", ip)
	}
	a.signIn(w, req, t.user, t.remember, in.Kind)
}

// secondFactorTry counts a try of a ticket's second factor: against the
// ticket (5 tries) and like a password (try). If it may not go ahead, it
// has answered.
func (a *app) secondFactorTry(w http.ResponseWriter, ticket string, u *auth.User, ip string) (attempt, bool) {
	if !a.pending.try(ticket) {
		writeError(w, http.StatusUnauthorized, "expired")
		return nil, false
	}
	return a.try(w, ip, u.Name)
}

func (a *app) secondFactorFailed(ctx context.Context, ticket string, u *auth.User, kind, ip string) {
	a.pending.failed(ticket)
	a.nameFailed(ctx, u.Name, ip)
	log.Printf("login: wrong %s for %q from %s", kind, u.Name, ip)
	a.audit_(ctx, u, "", "second_factor_failed", kind, ip)
}

// loginKeyBegin asks the browser for a signature from one of the user's keys.
func (a *app) loginKeyBegin(w http.ResponseWriter, req *http.Request, _ *principal) {
	var in ticketRequest
	if !readJSON(w, req, &in) {
		return
	}
	t := a.pending.get(in.Ticket, pendingTicket)
	if t == nil || t.user == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, t.user)
	if !ok {
		return
	}
	opts, sd, err := rp.BeginLogin(wu, secondFactorUV)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_keys")
		return
	}
	a.pending.set(in.Ticket, pendingTicket, func(p *pending) { p.wa = sd })
	writeJSON(w, http.StatusOK, opts.Response)
}

type credentialRequest struct {
	Ticket string `json:"ticket,omitempty"`
	Enrol  string `json:"enrol,omitempty"` // an enrolment link's token (stepup.go)
	// With key/begin: "key" for a security key that is only the second
	// step after the password (nothing stored on it); otherwise a passkey.
	Kind       string          `json:"kind,omitempty"`
	ID         string          `json:"id,omitempty"`
	Remember   bool            `json:"remember,omitempty"`
	Name       string          `json:"name,omitempty"`
	Credential json.RawMessage `json:"credential"`
}

func (a *app) loginKeyFinish(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) {
		return
	}
	t := a.pending.get(in.Ticket, pendingTicket)
	if t == nil || t.user == nil || t.wa == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, t.user)
	if !ok {
		return
	}
	tries, ok := a.secondFactorTry(w, in.Ticket, t.user, clientIP(req))
	if !ok {
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = rp.ValidateLogin(wu, *t.wa, parsed)
	}
	if err == nil && a.cloned(ctx, req, t.user, wu, cred) {
		err = errCloned
	}
	tries.done(err == nil)
	if err != nil {
		a.secondFactorFailed(ctx, in.Ticket, t.user, "key", clientIP(req))
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.pending.take(in.Ticket, pendingTicket)
	a.store.KeyUsed(ctx, t.user, cred)
	a.signIn(w, req, t.user, t.remember, "key")
}

// ceremony is the relying party and the WebAuthn view of u; on an error
// it has answered already.
func (a *app) ceremony(w http.ResponseWriter, req *http.Request, u *auth.User) (*webauthn.WebAuthn, *waUser, bool) {
	rp, err := a.relyingParty(req)
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusBadRequest, "webauthn_unavailable")
		return nil, nil, false
	}
	wu, err := a.waUser(req.Context(), u)
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return nil, nil, false
	}
	return rp, wu, true
}

// loginPasskeyBegin starts a sign-in without name or password: the browser
// offers the passkeys it has for this site.
func (a *app) loginPasskeyBegin(w http.ResponseWriter, req *http.Request, _ *principal) {
	if !a.hasAccounts(req.Context()) {
		writeError(w, http.StatusServiceUnavailable, "setup")
		return
	}
	// A locked address can't start one either (pentest F20).
	if a.blocked(w, clientIP(req)) {
		return
	}
	rp, err := a.relyingParty(req)
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusBadRequest, "webauthn_unavailable")
		return
	}
	opts, sd, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	id, err := a.pending.put("", pendingPasskeyLogin, clientIP(req), &pending{wa: sd})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "busy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "options": opts.Response})
}

func (a *app) loginPasskeyFinish(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) {
		return
	}
	ip := clientIP(req)
	tries, ok := a.try(w, ip, "")
	if !ok {
		return
	}
	t := a.pending.take(in.ID, pendingPasskeyLogin)
	if t == nil || t.wa == nil {
		tries.done(true)
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	rp, err := a.relyingParty(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "webauthn_unavailable")
		return
	}
	var who *auth.User
	find := func(_, handle []byte) (webauthn.User, error) {
		u, err := a.store.UserByHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		who = u
		return a.waUser(ctx, u)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		_, cred, err = rp.ValidatePasskeyLogin(find, *t.wa, parsed)
	}
	// Without user verification a key is only the second of two factors.
	if err == nil && !cred.Flags.UserVerified {
		err = errors.New("no user verification")
	}
	if err == nil && who.Disabled {
		err = errors.New("account disabled")
	}
	if err == nil {
		if wu, werr := a.waUser(ctx, who); werr == nil && a.cloned(ctx, req, who, wu, cred) {
			err = errCloned
		}
	}
	tries.done(err == nil)
	if err != nil {
		log.Printf("login: passkey refused from %s: %v", ip, err)
		a.audit_(ctx, who, "passkey", "login_failed", "passkey", ip)
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.store.KeyUsed(ctx, who, cred)
	a.signIn(w, req, who, in.Remember, "passkey")
}

/* ---------- one's own factors ---------- */

type keyInfo struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Passwordless bool       `json:"passwordless"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsed     *time.Time `json:"last_used,omitempty"`
}

func (a *app) security(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	f, err := a.store.Factors(ctx, p.user)
	keys, err2 := a.store.Keys(ctx, p.user)
	if err != nil || err2 != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	list := []keyInfo{}
	for _, k := range keys {
		ki := keyInfo{ID: k.ID, Name: k.Name, Passwordless: k.Passwordless, CreatedAt: k.CreatedAt.UTC()}
		if !k.LastUsed.IsZero() {
			lu := k.LastUsed.UTC()
			ki.LastUsed = &lu
		}
		list = append(list, ki)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fresh":         a.isFresh(p),
		"required":      a.needsFactor(p.user),
		"totp":          f.TOTP,
		"recovery_left": f.RecoveryLeft,
		"keys":          list,
	})
}

type passwordChange struct {
	Current string `json:"current"`
	New     string `json:"new"`
	// Also end every API token (left out: yes) and the passkeys that sign
	// in on their own (left out: no): what someone who had the account
	// may have left behind.
	RevokeTokens   *bool `json:"revoke_tokens"`
	RevokePasskeys bool  `json:"revoke_passkeys"`
}

// Why a new password is refused, for the page's own texts.
var passwordReasons = map[error]string{
	auth.ErrPasswordShort:  "too_short",
	auth.ErrPasswordLong:   "too_long",
	auth.ErrPasswordCommon: "common",
	auth.ErrPasswordIsName: "is_name",
}

// changePassword sets a new password. The current one proves who asks, so
// no recent sign-in is needed; wrong ones count like failed sign-ins.
// Every other session of the user ends; this one stays and counts as
// freshly signed in.
func (a *app) changePassword(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in passwordChange
	if !readJSON(w, req, &in) {
		return
	}
	ip := clientIP(req)
	// With a second factor, the current password isn't proof enough: a
	// stolen session and a known password would get past the factor.
	f, err := a.store.Factors(ctx, p.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if f.Any() && !a.fresh(w, p) {
		return
	}
	tries, ok := a.try(w, ip, p.user.Name)
	if !ok {
		return
	}
	ok, err = a.store.PasswordMatches(ctx, p.user, in.Current)
	tries.done(ok || err != nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if !ok {
		a.nameFailed(ctx, p.user.Name, ip)
		log.Printf("password change: wrong current password for %q from %s", p.user.Name, ip)
		a.audit_(ctx, p.user, "", "password_change_failed", "", ip)
		writeError(w, http.StatusForbidden, "wrong_password")
		return
	}
	if in.New == in.Current {
		writeError(w, http.StatusBadRequest, "same")
		return
	}
	revoke := auth.Revoke{Tokens: in.RevokeTokens == nil || *in.RevokeTokens, Passkeys: in.RevokePasskeys}
	if err := a.store.SetPassword(ctx, p.user, in.New, p.session.ID, revoke); err != nil {
		if why, ok := passwordReasons[err]; ok {
			writeError(w, http.StatusBadRequest, why)
			return
		}
		log.Printf("password change: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.byName.Reset(nameKey(p.user.Name, ip))
	var ended []string
	if revoke.Tokens {
		ended = append(ended, "tokens")
	}
	if revoke.Passkeys {
		ended = append(ended, "passkeys")
	}
	a.audit_(ctx, p.user, "", "password_changed", strings.Join(ended, ", "), ip)
	a.notify(ctx, p.user, "password_changed", strings.Join(ended, ", "), ip, p)
	w.WriteHeader(http.StatusNoContent)
}

func regKey(p *principal) string  { return sessionKey("reg", p) }
func totpKey(p *principal) string { return sessionKey("totp", p) }

func (a *app) keyBegin(w http.ResponseWriter, req *http.Request, p *principal) {
	var in credentialRequest
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	if ok, _ := a.checkEnrol(w, req.Context(), p.user, in.Enrol); !ok {
		return
	}
	rp, wu, ok := a.ceremony(w, req, p.user)
	if !ok {
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, k := range wu.keys {
		exclude = append(exclude, k.Credential.Descriptor())
	}
	// A passkey if the device can (it then signs in on its own), a plain
	// security key otherwise. Asked for a security key, nothing is stored on
	// it and a touch is enough, as most sites register keys: some ways to a
	// key (the CTAP proxy of Qubes OS, older keys) can't store passkeys, and
	// the browser then gives up without the key even lighting up.
	keyOnly := in.Kind == "key"
	reg := webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred)
	if keyOnly {
		reg = webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			AuthenticatorAttachment: protocol.CrossPlatform,
			ResidentKey:             protocol.ResidentKeyRequirementDiscouraged,
			RequireResidentKey:      protocol.ResidentKeyNotRequired(),
			UserVerification:        protocol.VerificationDiscouraged,
		})
	}
	opts, sd, err := rp.BeginRegistration(wu, webauthn.WithExclusions(exclude), reg)
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		name = "Passkey"
		if keyOnly {
			name = "Security key"
		}
	}
	if _, err := a.pending.put(regKey(p), pendingKeyAdd, "", &pending{wa: sd, name: name, enrol: in.Enrol, keyOnly: keyOnly}); err != nil {
		writeError(w, http.StatusTooManyRequests, "busy")
		return
	}
	writeJSON(w, http.StatusOK, opts.Response)
}

func (a *app) keyFinish(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	t := a.pending.take(regKey(p), pendingKeyAdd)
	if t == nil || t.wa == nil {
		writeError(w, http.StatusBadRequest, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, p.user)
	if !ok {
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = rp.CreateCredential(wu, *t.wa, parsed)
	}
	if err != nil {
		log.Printf("webauthn: registration refused: %v", err)
		writeError(w, http.StatusBadRequest, "invalid")
		return
	}
	first, ok := a.firstFactor(w, ctx, p.user, t.enrol)
	if !ok {
		return
	}
	// Signs in on its own only as a passkey that verified its user.
	if err := a.store.AddKey(ctx, p.user, t.name, cred, cred.Flags.UserVerified && !t.keyOnly); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.factorAdded(ctx, req, p, "key_added", t.name, first)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) keyDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err == nil {
		err = a.store.DeleteKey(req.Context(), p.user, id)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.audit_(req.Context(), p.user, "", "key_removed", strconv.FormatInt(id, 10), clientIP(req))
	a.notify(req.Context(), p.user, "key_removed", "", clientIP(req), p)
	w.WriteHeader(http.StatusNoContent)
}

// totpBegin makes a secret and shows it as a QR code (made here, so no
// outside service ever sees it) and as text. It counts once a code from
// the app confirms it.
func (a *app) totpBegin(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Enrol string `json:"enrol,omitempty"`
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	if ok, _ := a.checkEnrol(w, req.Context(), p.user, in.Enrol); !ok {
		return
	}
	secret, err := auth.NewTOTPSecret()
	uri := auth.TOTPURI(secret, p.user.Name)
	var code *qr.Code
	if err == nil {
		code, err = qr.Encode(uri, qr.M)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if _, err := a.pending.put(totpKey(p), pendingTOTPAdd, "", &pending{secret: secret, enrol: in.Enrol}); err != nil {
		writeError(w, http.StatusTooManyRequests, "busy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"uri":    uri,
		"qr":     "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()),
	})
}

func (a *app) totpConfirm(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in ticketRequest
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	t := a.pending.get(totpKey(p), pendingTOTPAdd)
	if t == nil || t.secret == "" {
		writeError(w, http.StatusBadRequest, "expired")
		return
	}
	step, ok := auth.CheckTOTP(t.secret, in.Code, a.store.Now(), 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid")
		return
	}
	first, ok := a.firstFactor(w, ctx, p.user, t.enrol)
	if !ok {
		return
	}
	if err := a.store.SetTOTP(ctx, p.user, t.secret, step); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.pending.take(totpKey(p), pendingTOTPAdd)
	a.factorAdded(ctx, req, p, "totp_added", "", first)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) totpDelete(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	if err := a.store.DeleteTOTP(req.Context(), p.user); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.audit_(req.Context(), p.user, "", "totp_removed", "", clientIP(req))
	a.notify(req.Context(), p.user, "totp_removed", "", clientIP(req), p)
	w.WriteHeader(http.StatusNoContent)
}

// recoveryCodes makes ten new codes, replacing the old ones, and shows
// them this once.
func (a *app) recoveryCodes(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
		return
	}
	codes, err := a.store.NewRecoveryCodes(req.Context(), p.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.audit_(req.Context(), p.user, "", "recovery_codes_made", "", clientIP(req))
	a.notify(req.Context(), p.user, "recovery_codes_made", "", clientIP(req), p)
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

var errCloned = errors.New("the key's signature counter went back: a copy of it may be in use")

// cloned reports whether a sign-in with cred looks like one with a copy of
// the key (pentest F14): its signature counter didn't go up. Keys that
// always send 0, as synced passkeys do, never trip it. The sign-in is then
// refused, the audit log and the user's notices say so, and the key keeps
// its counter, so the copy can't move it on either.
func (a *app) cloned(ctx context.Context, req *http.Request, u *auth.User, wu *waUser, cred *webauthn.Credential) bool {
	if !cred.Authenticator.CloneWarning {
		return false
	}
	name := "?"
	for _, k := range wu.keys {
		if string(k.Credential.ID) == string(cred.ID) {
			name = k.Name
		}
	}
	ip := clientIP(req)
	log.Printf("WARNING: %s's key %q sent a signature counter that didn't go up (stored %d), from %s: refused, may be cloned", u.Name, name, cred.Authenticator.SignCount, ip)
	a.audit_(ctx, u, "", "key_clone_warning", name, ip)
	a.notify(ctx, u, "key_clone_warning", name, ip, nil)
	return true
}

// secondFactorUV asks a key for a touch only, no PIN or fingerprint, where
// it is the second step after the password (or confirms a signed-in user):
// having the key is the factor, and a PIN would add nothing. That is how
// most sites ask, and it keeps the request simple for the ways to a key
// that can't do more, such as the CTAP proxy of Qubes OS. A passkey that
// signs in on its own still has to verify its user (loginPasskeyBegin).
var secondFactorUV = webauthn.WithUserVerification(protocol.VerificationDiscouraged)
