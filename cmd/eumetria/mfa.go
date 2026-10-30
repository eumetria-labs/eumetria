package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
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
// Changing one's factors needs a sign-in within the last 10 minutes.
// Admins without a second factor can read but not sync or see the audit log.

const (
	ticketTTL = 5 * time.Minute
	freshFor  = 10 * time.Minute
)

/* ---------- what lives between two requests ---------- */

// pending is a ticket or a WebAuthn ceremony, kept in memory between the
// request that starts it and the one that finishes it. A restart forgets
// them, which only means starting over.
type pending struct {
	until    time.Time
	user     *auth.User
	remember bool
	tries    int
	wa       *webauthn.SessionData // the challenge of a WebAuthn ceremony
	secret   string                // a TOTP secret waiting for its first code
	name     string                // a key's name waiting for its registration
}

type pendingStore struct {
	mu sync.Mutex
	m  map[string]*pending
}

// put keeps p under key, or under a new random id when key is "".
func (s *pendingStore) put(key string, p *pending) string {
	if key == "" {
		b := make([]byte, 24)
		rand.Read(b)
		key = base64.RawURLEncoding.EncodeToString(b)
	}
	p.until = time.Now().Add(ticketTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*pending{}
	}
	for k, x := range s.m {
		if time.Now().After(x.until) {
			delete(s.m, k)
		}
	}
	s.m[key] = p
	return key
}

// get returns a copy of what is kept under key, or nil.
func (s *pendingStore) get(key string) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[key]
	if p == nil || time.Now().After(p.until) {
		delete(s.m, key)
		return nil
	}
	c := *p
	return &c
}

func (s *pendingStore) take(key string) *pending {
	p := s.get(key)
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	return p
}

// set replaces what is kept under key, keeping its expiry.
func (s *pendingStore) set(key string, update func(*pending)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.m[key]; p != nil {
		update(p)
	}
}

// failed counts a wrong second factor; the fifth ends the ticket.
func (s *pendingStore) failed(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.m[key]; p != nil {
		if p.tries++; p.tries >= 5 {
			delete(s.m, key)
		}
	}
}

/* ---------- WebAuthn ---------- */

// relyingParty is the WebAuthn configuration for the site as the browser
// sees it: its host is the relying party ID, its origin the only one
// accepted. Browsers allow WebAuthn only over https, or on plain
// http://localhost, so that decides the scheme; behind a proxy that ends
// TLS (nginx) the request itself is plain http and can't tell.
func (a *app) relyingParty(req *http.Request) (*webauthn.WebAuthn, error) {
	host := req.Host
	if h, _, err := net.SplitHostPort(req.Host); err == nil {
		host = h
	}
	scheme := "https"
	if req.TLS == nil && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
		scheme = "http"
	}
	origin := scheme + "://" + req.Host
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
		return
	}
	a.byName.Reset("name:" + strings.ToLower(u.Name))
	setSessionCookie(w, token, remember)
	a.audit_(ctx, u, "", "login", how, ip)
	a.writeMe(w, ctx, u)
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
	t := a.pending.get(in.Ticket)
	if t == nil || t.user == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	ip := clientIP(req)
	nameKey := "name:" + strings.ToLower(t.user.Name)
	if d := max(a.byIP.Blocked("ip:"+ip), a.byName.Blocked(nameKey)); d > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "locked", "retry_after": int(d.Seconds()) + 1})
		return
	}
	var ok bool
	var err error
	switch in.Kind {
	case "totp":
		ok, err = a.store.UseTOTP(ctx, t.user, in.Code)
	case "recovery":
		ok, err = a.store.UseRecoveryCode(ctx, t.user, in.Code)
	default:
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
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
	a.pending.take(in.Ticket)
	if in.Kind == "recovery" {
		f, _ := a.store.Factors(ctx, t.user)
		a.audit_(ctx, t.user, "", "recovery_code_used", strconv.Itoa(f.RecoveryLeft)+" left", ip)
	}
	a.signIn(w, req, t.user, t.remember, in.Kind)
}

func (a *app) secondFactorFailed(ctx context.Context, ticket string, u *auth.User, kind, ip string) {
	a.pending.failed(ticket)
	a.byIP.Fail("ip:" + ip)
	a.byName.Fail("name:" + strings.ToLower(u.Name))
	log.Printf("login: wrong %s for %q from %s", kind, u.Name, ip)
	a.audit_(ctx, u, "", "second_factor_failed", kind, ip)
}

// loginKeyBegin asks the browser for a signature from one of the user's keys.
func (a *app) loginKeyBegin(w http.ResponseWriter, req *http.Request, _ *principal) {
	var in ticketRequest
	if !readJSON(w, req, &in) {
		return
	}
	t := a.pending.get(in.Ticket)
	if t == nil || t.user == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, t.user)
	if !ok {
		return
	}
	opts, sd, err := rp.BeginLogin(wu)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_keys")
		return
	}
	a.pending.set(in.Ticket, func(p *pending) { p.wa = sd })
	writeJSON(w, http.StatusOK, opts.Response)
}

type credentialRequest struct {
	Ticket     string          `json:"ticket,omitempty"`
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
	t := a.pending.get(in.Ticket)
	if t == nil || t.user == nil || t.wa == nil {
		writeError(w, http.StatusUnauthorized, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, t.user)
	if !ok {
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = rp.ValidateLogin(wu, *t.wa, parsed)
	}
	if err != nil {
		a.secondFactorFailed(ctx, in.Ticket, t.user, "key", clientIP(req))
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.pending.take(in.Ticket)
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
	id := a.pending.put("", &pending{wa: sd})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "options": opts.Response})
}

func (a *app) loginPasskeyFinish(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) {
		return
	}
	t := a.pending.take(in.ID)
	if t == nil || t.wa == nil {
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
	if err != nil {
		ip := clientIP(req)
		a.byIP.Fail("ip:" + ip)
		log.Printf("login: passkey refused from %s: %v", ip, err)
		a.audit_(ctx, who, "passkey", "login_failed", "passkey", ip)
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.store.KeyUsed(ctx, who, cred)
	a.signIn(w, req, who, in.Remember, "passkey")
}

/* ---------- one's own factors ---------- */

// fresh reports whether the session signed in recently enough to change
// its second factors; if not, it has answered.
func (a *app) fresh(w http.ResponseWriter, p *principal) bool {
	if a.store.Now().Sub(p.session.AuthenticatedAt) <= freshFor {
		return true
	}
	writeError(w, http.StatusForbidden, "reauth")
	return false
}

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
		"fresh":         a.store.Now().Sub(p.session.AuthenticatedAt) <= freshFor,
		"required":      p.user.NeedsSecondFactor(),
		"totp":          f.TOTP,
		"recovery_left": f.RecoveryLeft,
		"keys":          list,
	})
}

type passwordChange struct {
	Current string `json:"current"`
	New     string `json:"new"`
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
	nameKey := "name:" + strings.ToLower(p.user.Name)
	if d := max(a.byIP.Blocked("ip:"+ip), a.byName.Blocked(nameKey)); d > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "locked", "retry_after": int(d.Seconds()) + 1})
		return
	}
	ok, err := a.store.PasswordMatches(ctx, p.user, in.Current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if !ok {
		a.byIP.Fail("ip:" + ip)
		a.byName.Fail(nameKey)
		log.Printf("password change: wrong current password for %q from %s", p.user.Name, ip)
		a.audit_(ctx, p.user, "", "password_change_failed", "", ip)
		writeError(w, http.StatusForbidden, "wrong_password")
		return
	}
	if in.New == in.Current {
		writeError(w, http.StatusBadRequest, "same")
		return
	}
	if err := a.store.SetPassword(ctx, p.user, in.New, p.session.ID); err != nil {
		if why, ok := passwordReasons[err]; ok {
			writeError(w, http.StatusBadRequest, why)
			return
		}
		log.Printf("password change: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.store.SetAuthenticated(ctx, p.session)
	a.byName.Reset(nameKey)
	a.audit_(ctx, p.user, "", "password_changed", "", ip)
	w.WriteHeader(http.StatusNoContent)
}

func regKey(p *principal) string  { return "reg:" + strconv.FormatInt(p.session.ID, 10) }
func totpKey(p *principal) string { return "totp:" + strconv.FormatInt(p.session.ID, 10) }

func (a *app) keyBegin(w http.ResponseWriter, req *http.Request, p *principal) {
	var in credentialRequest
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
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
	// security key otherwise.
	opts, sd, err := rp.BeginRegistration(wu, webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil {
		log.Printf("webauthn: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		name = "Passkey"
	}
	a.pending.put(regKey(p), &pending{wa: sd, name: name})
	writeJSON(w, http.StatusOK, opts.Response)
}

func (a *app) keyFinish(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	t := a.pending.take(regKey(p))
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
	if err := a.store.AddKey(ctx, p.user, t.name, cred, cred.Flags.UserVerified); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.audit_(ctx, p.user, "", "key_added", t.name, clientIP(req))
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
	w.WriteHeader(http.StatusNoContent)
}

// totpBegin makes a secret and shows it as a QR code (made here, so no
// outside service ever sees it) and as text. It counts once a code from
// the app confirms it.
func (a *app) totpBegin(w http.ResponseWriter, req *http.Request, p *principal) {
	if !a.fresh(w, p) {
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
	a.pending.put(totpKey(p), &pending{secret: secret})
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
	t := a.pending.get(totpKey(p))
	if t == nil || t.secret == "" {
		writeError(w, http.StatusBadRequest, "expired")
		return
	}
	step, ok := auth.CheckTOTP(t.secret, in.Code, a.store.Now(), 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid")
		return
	}
	if err := a.store.SetTOTP(ctx, p.user, t.secret, step); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.pending.take(totpKey(p))
	a.audit_(ctx, p.user, "", "totp_added", "", clientIP(req))
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
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// fromProxy reports whether the request came through the local reverse
// proxy, whose X-Real-IP is then believed: from this machine, or from a
// private address, which is how Docker's port forwarding delivers it
// (the bridge's gateway, e.g. 172.17.0.1). The container's port is bound
// to 127.0.0.1 on the host, so nothing else gets there.
func fromProxy(req *http.Request) bool {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}
