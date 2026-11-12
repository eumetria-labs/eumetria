package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"eumetria/internal/auth"
)

// Step-up, enrolment links and security notices (pentest F18, F19).
//
// Step-up: changing how an account signs in (keys, the app, recovery
// codes, tokens, the password of an account with a second factor) and the
// admin tools need the user to have proved who they are within the last
// freshFor. Signing in counts; later, the user confirms it again with what
// a thief of the session cookie doesn't have: a code from the app, one of
// their keys, or, for an account without a second factor, the password.
// Recovery codes don't count: they are for signing in when all else is
// lost. Changing the password doesn't count either, so a stolen session and
// a known password don't get past the second factor.
//
// Enrolment links: an account that must have a second factor and has none
// sets up its first one only with a link an admin or the command line made
// (auth.NewEnrolment).
//
// Notices: every such change is shown to the account's user until they
// dismiss it, and to admins in the user list.

// isFresh reports whether p's user proved who they are recently enough.
func (a *app) isFresh(p *principal) bool {
	return p.session != nil && a.store.Now().Sub(p.session.AuthenticatedAt) <= freshFor
}

// fresh is isFresh, answering 403 "reauth" when not: the page then asks the
// user to confirm it's them (POST /api/security/reauth) and tries again.
func (a *app) fresh(w http.ResponseWriter, p *principal) bool {
	if a.isFresh(p) {
		return true
	}
	writeError(w, http.StatusForbidden, "reauth")
	return false
}

func reauthKey(u *auth.User) string { return "reauth:" + strconv.FormatInt(u.ID, 10) }

type reauthRequest struct {
	Code     string `json:"code,omitempty"`     // from the authenticator app
	Password string `json:"password,omitempty"` // only without a second factor
}

// reauth confirms who the session's user is with a code from their app or,
// when they have no second factor, their password.
func (a *app) reauth(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in reauthRequest
	if !readJSON(w, req, &in) {
		return
	}
	f, err := a.store.Factors(ctx, p.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	var how string
	switch {
	case in.Code != "" && f.TOTP:
		how = "totp"
	case in.Password != "" && !f.Any():
		how = "password"
	default:
		// A password where a second factor exists, or a code without an app.
		writeError(w, http.StatusBadRequest, "bad_method")
		return
	}
	tries, ok := a.try(w, clientIP(req), "", reauthKey(p.user))
	if !ok {
		return
	}
	if how == "totp" {
		ok, err = a.store.UseTOTP(ctx, p.user, strings.TrimSpace(in.Code))
	} else {
		ok, err = a.store.PasswordMatches(ctx, p.user, in.Password)
	}
	tries.done(ok || err != nil)
	if errors.Is(err, auth.ErrBusy) {
		busy(w)
		return
	}
	if err != nil {
		log.Printf("reauth: %v", err)
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	if !ok {
		a.reauthFailed(ctx, req, p, how)
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.reauthDone(w, req, p, how)
}

// reauthKeyBegin asks the browser for a signature from one of the user's
// own keys.
func (a *app) reauthKeyBegin(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct{}
	if !readJSON(w, req, &in) || a.reauthBlocked(w, req, p) {
		return
	}
	rp, wu, ok := a.ceremony(w, req, p.user)
	if !ok {
		return
	}
	opts, sd, err := rp.BeginLogin(wu, secondFactorUV)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_keys")
		return
	}
	if _, err := a.pending.put(sessionKey("reauth", p), pendingReauth, "", &pending{wa: sd}); err != nil {
		writeError(w, http.StatusTooManyRequests, "busy")
		return
	}
	writeJSON(w, http.StatusOK, opts.Response)
}

func (a *app) reauthKeyFinish(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	var in credentialRequest
	if !readJSON(w, req, &in) {
		return
	}
	tries, ok := a.try(w, clientIP(req), "", reauthKey(p.user))
	if !ok {
		return
	}
	t := a.pending.take(sessionKey("reauth", p), pendingReauth)
	if t == nil || t.wa == nil {
		tries.done(true)
		writeError(w, http.StatusBadRequest, "expired")
		return
	}
	rp, wu, ok := a.ceremony(w, req, p.user)
	if !ok {
		tries.done(true)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	var cred *webauthn.Credential
	if err == nil {
		cred, err = rp.ValidateLogin(wu, *t.wa, parsed)
	}
	if err == nil && a.cloned(ctx, req, p.user, wu, cred) {
		err = errCloned
	}
	tries.done(err == nil)
	if err != nil {
		a.reauthFailed(ctx, req, p, "key")
		writeError(w, http.StatusUnauthorized, "invalid")
		return
	}
	a.store.KeyUsed(ctx, p.user, cred)
	a.reauthDone(w, req, p, "key")
}

// reauthBlocked answers 429 while the user or the address made too many
// wrong tries (counted in try): the same limits as signing in.
func (a *app) reauthBlocked(w http.ResponseWriter, req *http.Request, p *principal) bool {
	if d := max(a.byIP.Blocked("ip:"+clientIP(req)), a.byName.Blocked(reauthKey(p.user))); d > 0 {
		locked(w, d)
		return true
	}
	return false
}

func (a *app) reauthFailed(ctx context.Context, req *http.Request, p *principal, how string) {
	ip := clientIP(req)
	log.Printf("reauth: wrong %s for %q from %s", how, p.user.Name, ip)
	a.audit_(ctx, p.user, "", "reauth_failed", how, ip)
}

func (a *app) reauthDone(w http.ResponseWriter, req *http.Request, p *principal, how string) {
	ctx := req.Context()
	if err := a.store.SetAuthenticated(ctx, p.session); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	a.byName.Reset(reauthKey(p.user))
	a.audit_(ctx, p.user, "", "reauth", how, clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- enrolment links ---------- */

// needsEnrolLink reports whether u may set up a second factor only with an
// enrolment link: they must have one (needsFactor) and have none yet. With
// -demo nobody does, so the demo account can try everything.
func (a *app) needsEnrolLink(ctx context.Context, u *auth.User) (bool, error) {
	if a.demo || !a.needsFactor(u) {
		return false, nil
	}
	f, err := a.store.Factors(ctx, u)
	return !f.Any(), err
}

// checkEnrol answers 403 "enrol_required" when u needs an enrolment link
// and token isn't a valid one. It reports whether to go on, and whether
// the link has to be used up once the factor is saved.
func (a *app) checkEnrol(w http.ResponseWriter, ctx context.Context, u *auth.User, token string) (ok, needed bool) {
	needed, err := a.needsEnrolLink(ctx, u)
	if err == nil && needed {
		ok, err = a.store.EnrolmentValid(ctx, u, token)
		if err == nil && !ok {
			writeError(w, http.StatusForbidden, "enrol_required")
			return false, true
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return false, needed
	}
	return true, needed
}

// useEnrol uses up the link a first factor was set up with. It reports
// whether that worked; if not, it has answered.
func (a *app) useEnrol(w http.ResponseWriter, ctx context.Context, u *auth.User, token string) bool {
	ok, err := a.store.UseEnrolment(ctx, u, token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "enrol_required")
	}
	return ok
}

// adminEnrolLink makes a new enrolment link for a user without a second
// factor; the page turns the token into a link to copy.
func (a *app) adminEnrolLink(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Mail bool `json:"mail"` // also send it to the user's confirmed address
	}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	u, ok := a.targetUser(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	f, err := a.store.Factors(ctx, u)
	if err != nil {
		a.failed(w, "enrol link", err)
		return
	}
	if f.Any() {
		writeError(w, http.StatusConflict, "has_factor")
		return
	}
	token, expires, err := a.store.NewEnrolment(ctx, u)
	if err != nil {
		a.failed(w, "enrol link", err)
		return
	}
	a.audit_(ctx, p.user, "", "enrol_link_made", u.Name, clientIP(req))
	answer := map[string]any{"token": token, "expires": expires.UTC()}
	if in.Mail {
		why, ok := a.mailEnrolLink(ctx, req, u, token, expires)
		answer["mailed"] = ok
		if !ok {
			answer["mail_error"] = why
		}
	}
	writeJSON(w, http.StatusCreated, answer)
}

// enrolLink is the link for token, for the command line: with a public URL
// (General settings, or PUBLIC_URL) the whole link, otherwise what to put
// after the site's address.
func enrolLink(base, token string) string {
	if base != "" {
		return base + "/#enrol=" + token
	}
	return "<your site's address>/#enrol=" + token
}

/* ---------- elevation ---------- */

// elevated looks at an account that may have just started to need a
// second factor (its role, its role's scopes or require_second_factor
// changed); before is the account as it was (pentest re-test N5). Whoever
// knew the password of an account that needed no factor could set one up
// without a link, and it would then count for the new rights. So the
// factors not set up with a link or by an account that already needed one
// are removed (auth.DropUnverifiedFactors), and when none is left, a new
// enrolment link is made and mailed to the confirmed address. It returns
// that link's token ("" when none was made) and whether it was mailed.
func (a *app) elevated(ctx context.Context, req *http.Request, before *auth.User, by *principal) (token string, mailed bool, err error) {
	if a.demo || a.needsFactor(before) {
		return "", false, nil
	}
	u, err := a.store.UserByID(ctx, before.ID)
	if err != nil || !a.needsFactor(u) {
		return "", false, err
	}
	n, err := a.store.DropUnverifiedFactors(ctx, u)
	if err != nil {
		return "", false, err
	}
	ip := clientIP(req)
	byName := ""
	if by != nil {
		byName = by.user.Name
	}
	if n > 0 {
		a.audit_(ctx, nil, u.Name, "factors_dropped", strconv.Itoa(n)+" set up without a link, role "+string(u.Role), ip)
		a.notify(ctx, u, "factors_dropped", byName, ip, nil)
	}
	f, err := a.store.Factors(ctx, u)
	if err != nil || f.Any() {
		return "", false, err
	}
	token, expires, err := a.store.NewEnrolment(ctx, u)
	if err != nil {
		return "", false, err
	}
	a.audit_(ctx, nil, u.Name, "enrol_link_made", u.Name, ip)
	_, mailed = a.mailEnrolLink(ctx, req, u, token, expires)
	return token, mailed, nil
}

/* ---------- notices ---------- */

// notify records a security notice for u about a change by (and logs a
// failure: the change it is about has happened already). A change u made
// in their own session isn't shown in that session.
func (a *app) notify(ctx context.Context, u *auth.User, kind, detail, ip string, by *principal) {
	var session string
	if by != nil && by.session != nil && by.user.ID == u.ID {
		session = by.session.Key
	}
	if err := a.store.AddNotice(ctx, u, kind, detail, ip, session); err != nil {
		log.Printf("notice: %v", err)
	}
	// And by mail, which whoever has the session can't dismiss (phase 6).
	a.alert(ctx, u, kind, detail, ip)
}

// stepUpNotices are the notices that take a step-up to dismiss: a new way
// into the account.
var stepUpNotices = map[string]bool{"key_added": true, "totp_added": true, "key_clone_warning": true, "token_created": true}

// noticesSeen dismisses the user's notices, up to the newest one they saw.
func (a *app) noticesSeen(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		UpTo int64 `json:"up_to"`
	}
	if !readJSON(w, req, &in) {
		return
	}
	// A notice of a new way in can't be waved away by whoever holds a
	// stolen session: that needs the user to confirm it's them (pentest
	// re-test I1).
	list, err := a.store.Notices(req.Context(), p.user, p.session.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	for _, n := range list {
		if (in.UpTo == 0 || n.ID <= in.UpTo) && stepUpNotices[n.Kind] && !a.fresh(w, p) {
			return
		}
	}
	if err := a.store.SeeNotices(req.Context(), p.user, in.UpTo); err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// expiresText is how the command line says when a link stops working.
func expiresText(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

// firstFactor checks, before a new factor is saved, whether it is the
// user's first and, if they needed an enrolment link for it, uses the link
// up. It also says how the factor is set up, to store with it (pentest
// re-test N5). It reports whether to go on; if not, it has answered.
func (a *app) firstFactor(w http.ResponseWriter, req *http.Request, u *auth.User, enrol string) (first bool, o auth.Origin, ok bool) {
	ctx := req.Context()
	f, err := a.store.Factors(ctx, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return false, o, false
	}
	needed, err := a.needsEnrolLink(ctx, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server")
		return false, o, false
	}
	if needed && !a.useEnrol(w, ctx, u, enrol) {
		return false, o, false
	}
	o = auth.Origin{Via: auth.ViaPlain, IP: clientIP(req), Role: string(u.Role)}
	switch {
	case needed:
		o.Via = auth.ViaLink
	case a.needsFactor(u):
		o.Via = auth.ViaElevated
	}
	return !f.Any(), o, true
}

// factorAdded records a new factor: in the audit log, and as a notice. The
// first one stands out in both, since it is the one that makes the
// account's elevated rights work.
func (a *app) factorAdded(ctx context.Context, req *http.Request, p *principal, action, detail string, first bool) {
	ip := clientIP(req)
	a.audit_(ctx, p.user, "", action, detail, ip)
	a.notify(ctx, p.user, action, detail, ip, p)
	if first {
		a.audit_(ctx, p.user, "", "first_factor_enrolled", strings.TrimSuffix(action, "_added"), ip)
		if p.user.Elevated() {
			a.alertAdmins(ctx, p.user.Name+" set up their first second factor", p.user.Name+" hat den ersten zweiten Faktor eingerichtet")
		}
	}
}
