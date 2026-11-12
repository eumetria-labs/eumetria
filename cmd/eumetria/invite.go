package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/mail"
	"eumetria/internal/settings"
)

// Invitations: an admin adds someone with their e-mail address instead of a
// password. They get a welcome mail with a link (auth.InviteTTL, one use),
// choose their password there and are signed in; the address then counts
// as confirmed. If their role needs a second factor, the answer carries an
// enrolment token, so they set it up right away without a second link.

// inviteMail sends u's invitation to addr; it reports why not, or "".
func (a *app) inviteMail(ctx context.Context, req *http.Request, u *auth.User, addr, token string, expires time.Time, by string) string {
	v, ok := a.mailValues(ctx)
	if !ok {
		return "mail_off"
	}
	if v.PublicURL == "" {
		return "no_public_url" // links only to the public URL (pentest re-test I3)
	}
	m := inviteMessage(v, u, addr, v.PublicURL+"/#invite="+token, expires, by)
	if !a.mailTo(ctx, bulkMail, u.ID, addr, m.Subject, m.Body) {
		return "not_sent"
	}
	return ""
}

// inviteMessage is the welcome mail with its link; by is who made the account.
func inviteMessage(v settings.Values, u *auth.User, addr, link string, expires time.Time, by string) mail.Message {
	when := expires.In(v.Zone).Format("2006-01-02 15:04")
	subject, text := "Welcome", "%s made you an account on %s: your user name is %s.\n\nOpen this link to choose your password (once, until %s):\n\n%s\n\n%s"
	factor := "If your account needs a second factor, the page sets it up with you next: a passkey or an authenticator app."
	if mailLang(v) == "de" {
		subject, text = "Willkommen", "%s hat dir ein Konto auf %s angelegt: Dein Benutzername ist %s.\n\nÖffne diesen Link, um dein Passwort zu wählen (einmal, bis %s):\n\n%s\n\n%s"
		factor = "Braucht dein Konto einen zweiten Faktor, richtet die Seite ihn danach mit dir ein: einen Passkey oder eine Authenticator-App."
	}
	body := fmt.Sprintf(text, by, v.Title, u.Name, when, link, factor) + "\n\n" + fmt.Sprintf(mailWords[mailLang(v)]["sign"], v.Title) + "\n"
	return mail.Message{To: addr, Subject: v.Title + ": " + subject, Body: body}
}

// adminInvite sends an invited user's invitation again, as a new link.
func (a *app) adminInvite(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct{}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	u, ok := a.targetUser(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	addr, err := a.store.InviteEmail(ctx, u)
	if err != nil {
		a.failed(w, "invite", err)
		return
	}
	if addr == "" {
		writeError(w, http.StatusConflict, "not_invited")
		return
	}
	if _, ok := a.mailValues(ctx); !ok {
		writeError(w, http.StatusConflict, "mail_off")
		return
	}
	token, expires, err := a.store.NewInvite(ctx, u, addr)
	if err != nil {
		a.failed(w, "invite", err)
		return
	}
	if why := a.inviteMail(ctx, req, u, addr, token, expires, p.user.Name); why != "" {
		writeError(w, http.StatusServiceUnavailable, why)
		return
	}
	a.audit_(ctx, p.user, "", "user_invited", u.Name+" ("+addr+")", clientIP(req))
	w.WriteHeader(http.StatusNoContent)
}

type inviteRequest struct {
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
	Remember bool   `json:"remember,omitempty"`
}

// inviteCheck says whose invitation a token is, for the welcome form. Wrong
// tokens count like failed sign-ins.
func (a *app) inviteCheck(w http.ResponseWriter, req *http.Request, _ *principal) {
	var in inviteRequest
	if !readJSON(w, req, &in) {
		return
	}
	tries, ok := a.try(w, clientIP(req), "")
	if !ok {
		return
	}
	u, err := a.store.Invite(req.Context(), in.Token)
	tries.done(err == nil)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "expired")
		return
	}
	if err != nil {
		a.failed(w, "invite", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": u.Name, "needs_second_factor": a.needsFactor(u)})
}

// inviteAccept sets the chosen password and signs in; with a role that
// needs a second factor, it answers with an enrolment token for it.
func (a *app) inviteAccept(w http.ResponseWriter, req *http.Request, _ *principal) {
	ctx := req.Context()
	var in inviteRequest
	if !readJSON(w, req, &in) {
		return
	}
	ip := clientIP(req)
	tries, ok := a.try(w, ip, "")
	if !ok {
		return
	}
	u, err := a.store.AcceptInvite(ctx, in.Token, in.Password)
	tries.done(err == nil || !errors.Is(err, auth.ErrNotFound))
	if why, bad := passwordReasons[err]; bad {
		writeError(w, http.StatusBadRequest, why)
		return
	}
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "expired")
		return
	}
	if err != nil {
		a.failed(w, "invite", err)
		return
	}
	a.audit_(ctx, u, "", "invite_accepted", "", ip)
	if !a.startSession(w, req, u, in.Remember, "invite") {
		return
	}
	answer := map[string]any{"name": u.Name}
	if need, err := a.needsEnrolLink(ctx, u); err == nil && need {
		if token, _, err := a.store.NewEnrolment(ctx, u); err == nil {
			answer["enrol"] = token
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// invite makes an account for name with role and sends its invitation to
// addr. It reports the new user, or the error reason to answer with.
func (a *app) invite(ctx context.Context, req *http.Request, p *principal, name string, role auth.Role, addr string) (*auth.User, string, error) {
	addr = strings.TrimSpace(addr)
	if mail.Address(addr) != nil {
		return nil, "bad_email", nil
	}
	if _, ok := a.mailValues(ctx); !ok {
		return nil, "mail_off", nil
	}
	u, err := a.store.CreateInvited(ctx, name, role)
	if err != nil {
		return nil, "", err
	}
	token, expires, err := a.store.NewInvite(ctx, u, addr)
	if err == nil {
		err = a.store.SetEmail(ctx, u, addr)
	}
	if err != nil {
		return nil, "", err
	}
	if why := a.inviteMail(ctx, req, u, addr, token, expires, p.user.Name); why != "" {
		return u, why, nil
	}
	return u, "", nil
}
