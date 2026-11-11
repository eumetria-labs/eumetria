package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/mail"
	"eumetria/internal/settings"
)

// E-mail (PLAN phase 6), optional: with a mail server in the settings,
// security alerts reach the account's owner even when someone else holds
// their session (an in-page notice can be dismissed by whoever has it), and
// enrolment and confirmation links can be sent. Mail goes only to confirmed
// addresses (auth.EmailState), in the background (mail.Outbox), at most
// mailPerUser per user and mailPerHour in all per hour. There is no
// password reset by mail: the mailbox doesn't become a way in.

const (
	mailPerUser = 10
	mailPerHour = 100
)

// mailSender is how mail goes out; tests replace it.
var mailSender = func(m settings.SMTP) mail.Sender {
	return &mail.SMTP{Host: m.Host, Port: m.Port, Implicit: m.Implicit, User: m.User, Password: m.Password, From: m.From, FromName: m.FromName}
}

// startMail sets up the outbox; with -demo nothing is ever sent.
func (a *app) startMail() {
	a.mailLimit = auth.NewLimiter(0, time.Hour, 0, 0)
	a.mailDaily = auth.NewLimiter(0, 24*time.Hour, 0, 0)
	a.outbox = &mail.Outbox{Sender: func(ctx context.Context) mail.Sender {
		if v, ok := a.mailValues(ctx); ok {
			return mailSender(v.SMTP)
		}
		return nil
	}}
	a.outbox.Start(context.Background())
}

// mailValues are the settings, and whether mail is set up.
func (a *app) mailValues(ctx context.Context) (settings.Values, bool) {
	if a.demo || a.runner == nil || a.runner.settings == nil {
		return settings.Values{}, false
	}
	v, err := a.runner.settings.Values(ctx)
	return v, err == nil && v.SMTP.Ready()
}

// mailTo queues a mail to addr for user (0: none), within the limits. It
// reports whether it was queued.
func (a *app) mailTo(ctx context.Context, user int64, addr, subject, body string) bool {
	if _, ok := a.mailValues(ctx); !ok || addr == "" {
		return false
	}
	key := "user:" + strconv.FormatInt(user, 10)
	if a.mailLimit.Failures("all") >= mailPerHour || user != 0 && a.mailLimit.Failures(key) >= mailPerUser {
		log.Printf("mail: over the limit, not sending %q to %s", subject, addr)
		return false
	}
	a.mailLimit.Fail("all")
	if user != 0 {
		a.mailLimit.Fail(key)
	}
	return a.outbox.Queue(mail.Message{To: addr, Subject: subject, Body: body})
}

// mailUser mails u at their confirmed address, if they have one.
func (a *app) mailUser(ctx context.Context, u *auth.User, subject, body string) bool {
	addr, err := a.store.VerifiedEmail(ctx, u)
	if err != nil {
		log.Printf("mail: %v", err)
	}
	return err == nil && a.mailTo(ctx, u.ID, addr, subject, body)
}

/* ---------- what the mails say ---------- */

type mailText struct{ subject, line string }

// alertTexts are the alerts, by notice kind; %s is the notice's detail.
var alertTexts = map[string]map[string]mailText{
	"en": {
		"key_added":           {"A passkey or key was added", "A passkey or key “%s” was added to your account."},
		"key_removed":         {"A passkey or key was removed", "A passkey or key was removed from your account."},
		"totp_added":          {"An authenticator app was set up", "An authenticator app was set up for your account."},
		"totp_removed":        {"The authenticator app was removed", "The authenticator app was removed from your account."},
		"recovery_codes_made": {"New recovery codes", "New recovery codes were made for your account; the old ones stopped working."},
		"token_created":       {"A new API token", "An API token “%s” was made for your account."},
		"password_changed":    {"Your password was changed", "The password of your account was changed."},
		"password_reset":      {"Your password was reset", "An admin (%s) set a new password for your account."},
		"key_clone_warning":   {"A sign-in with your key was refused", "A sign-in with your key “%s” was refused: its signature counter went back, so a copy of the key may be in use."},
		"login_pressure":      {"Many failed sign-ins", "There were many failed sign-ins for your account name lately. Nobody got in with them; they only slow sign-ins down."},
		"email_changing":      {"Your e-mail address is changing", "Your account's e-mail address is being changed to %s. Until that address is confirmed, mail keeps coming here."},
		"email_removed":       {"Your e-mail address was removed", "The e-mail address was removed from your account: no more alerts will come here."},
	},
	"de": {
		"key_added":           {"Ein Passkey oder Schlüssel wurde hinzugefügt", "Deinem Konto wurde ein Passkey oder Schlüssel „%s“ hinzugefügt."},
		"key_removed":         {"Ein Passkey oder Schlüssel wurde entfernt", "Aus deinem Konto wurde ein Passkey oder Schlüssel entfernt."},
		"totp_added":          {"Eine Authenticator-App wurde eingerichtet", "Für dein Konto wurde eine Authenticator-App eingerichtet."},
		"totp_removed":        {"Die Authenticator-App wurde entfernt", "Die Authenticator-App wurde aus deinem Konto entfernt."},
		"recovery_codes_made": {"Neue Wiederherstellungscodes", "Für dein Konto wurden neue Wiederherstellungscodes erstellt; die alten gelten nicht mehr."},
		"token_created":       {"Ein neues API-Token", "Für dein Konto wurde ein API-Token „%s“ erstellt."},
		"password_changed":    {"Dein Passwort wurde geändert", "Das Passwort deines Kontos wurde geändert."},
		"password_reset":      {"Dein Passwort wurde zurückgesetzt", "Ein Admin (%s) hat ein neues Passwort für dein Konto gesetzt."},
		"key_clone_warning":   {"Eine Anmeldung mit deinem Schlüssel wurde abgelehnt", "Eine Anmeldung mit deinem Schlüssel „%s“ wurde abgelehnt: Sein Signaturzähler ging zurück, es könnte also eine Kopie des Schlüssels im Umlauf sein."},
		"login_pressure":      {"Viele fehlgeschlagene Anmeldungen", "Zuletzt gab es viele fehlgeschlagene Anmeldungen für deinen Kontonamen. Hereingekommen ist damit niemand; sie verlangsamen nur die Anmeldung."},
		"email_changing":      {"Deine E-Mail-Adresse wird geändert", "Die E-Mail-Adresse deines Kontos wird in %s geändert. Bis diese Adresse bestätigt ist, kommen Mails weiter hierher."},
		"email_removed":       {"Deine E-Mail-Adresse wurde entfernt", "Die E-Mail-Adresse wurde aus deinem Konto entfernt: Hierher kommen keine Hinweise mehr."},
	},
}

var mailWords = map[string]map[string]string{
	"en": {
		"when":     "When: %s, from %s.",
		"notyou":   "If this wasn't you: sign in, remove what you don't know under Security and change your password, or ask your admin.",
		"security": "Security settings: %s",
		"sign":     "– %s",
	},
	"de": {
		"when":     "Wann: %s, von %s.",
		"notyou":   "Warst du das nicht? Melde dich an, entferne unter Sicherheit, was du nicht kennst, und ändere dein Passwort, oder frag deinen Admin.",
		"security": "Sicherheitseinstellungen: %s",
		"sign":     "– %s",
	},
}

func mailLang(v settings.Values) string {
	if v.Language == "de" {
		return "de"
	}
	return "en"
}

// alert mails u about a change to how their account signs in, if there is
// a text for kind and they have a confirmed address.
func (a *app) alert(ctx context.Context, u *auth.User, kind, detail, ip string) {
	a.alertTo(ctx, u, "", kind, detail, ip)
}

// alertTo is alert, to addr instead of u's confirmed address when set.
func (a *app) alertTo(ctx context.Context, u *auth.User, addr, kind, detail, ip string) {
	v, ok := a.mailValues(ctx)
	if !ok {
		return
	}
	lang := mailLang(v)
	t, ok := alertTexts[lang][kind]
	if !ok {
		return
	}
	line := t.line
	if strings.Contains(line, "%s") {
		line = fmt.Sprintf(line, detail)
	}
	body := a.mailBody(v, lang, line, ip, true)
	subject := v.Title + ": " + t.subject
	if addr != "" {
		a.mailTo(ctx, u.ID, addr, subject, body)
		return
	}
	a.mailUser(ctx, u, subject, body)
}

// mailBody is line, when it happened and from where, what to do if it
// wasn't them, and a way to the security settings when the public URL is
// known. No link signs anyone in.
func (a *app) mailBody(v settings.Values, lang, line, ip string, notYou bool) string {
	w := mailWords[lang]
	var b strings.Builder
	b.WriteString(line + "\n\n")
	if ip != "" {
		b.WriteString(fmt.Sprintf(w["when"], a.store.Now().In(v.Zone).Format("2006-01-02 15:04 MST"), ip) + "\n\n")
	}
	if notYou {
		b.WriteString(w["notyou"] + "\n\n")
		if v.PublicURL != "" {
			b.WriteString(fmt.Sprintf(w["security"], v.PublicURL+"/#security") + "\n\n")
		}
	}
	b.WriteString(fmt.Sprintf(w["sign"], v.Title) + "\n")
	return b.String()
}

// alertAdmins mails every admin with a confirmed address about something
// that concerns admin or sync rights.
func (a *app) alertAdmins(ctx context.Context, en, de string) {
	v, ok := a.mailValues(ctx)
	if !ok {
		return
	}
	users, err := a.store.Users(ctx)
	if err != nil {
		log.Printf("mail: %v", err)
		return
	}
	lang := mailLang(v)
	line := en
	if lang == "de" {
		line = de
	}
	for _, u := range users {
		if u.Can(auth.ScopeAdmin) && !u.Disabled {
			a.mailUser(ctx, u, v.Title+": "+line, a.mailBody(v, lang, line+".", "", false))
		}
	}
}

/* ---------- the address ---------- */

// sendConfirmation lets email wait for its confirmation and mails the link
// there. It reports whether mail is set up (and the link went out).
func (a *app) sendConfirmation(ctx context.Context, req *http.Request, u *auth.User, email string) (bool, error) {
	v, ok := a.mailValues(ctx)
	if !ok {
		return false, nil
	}
	token, err := a.store.RequestEmail(ctx, u, email)
	if err != nil {
		return false, err
	}
	link := a.siteURL(req) + "/#email=" + token
	subject, line := "Confirm your e-mail address", "To get your account's security alerts at this address, open this link (once, within a day) while signed in as %s:"
	if mailLang(v) == "de" {
		subject, line = "Bestätige deine E-Mail-Adresse", "Damit die Sicherheitshinweise deines Kontos an diese Adresse gehen, öffne diesen Link (einmal, innerhalb eines Tages), angemeldet als %s:"
	}
	body := fmt.Sprintf(line, u.Name) + "\n\n" + link + "\n\n" + fmt.Sprintf(mailWords[mailLang(v)]["sign"], v.Title) + "\n"
	if !a.mailTo(ctx, u.ID, email, v.Title+": "+subject, body) {
		return true, errors.New("not sent: over the limit or the queue is full")
	}
	return true, nil
}

// changeEmail makes email u's address: confirmed by a link sent there when
// mail is set up, otherwise right away but not confirmed. The old confirmed
// address is told (pentest F18: someone with the session would want the
// alerts elsewhere). It needs a recent proof of who one is; if not, it has
// answered.
func (a *app) changeEmail(w http.ResponseWriter, req *http.Request, p *principal, email string) bool {
	ctx := req.Context()
	if !a.fresh(w, p) {
		return false
	}
	old, err := a.store.VerifiedEmail(ctx, p.user)
	if err != nil {
		a.failed(w, "email", err)
		return false
	}
	ip := clientIP(req)
	switch {
	case email == "":
		err = a.store.SetEmail(ctx, p.user, "")
		if err == nil && old != "" {
			a.alertTo(ctx, p.user, old, "email_removed", "", ip)
		}
		a.audit_(ctx, p.user, "", "email_removed", "", ip)
	default:
		var mailed bool
		mailed, err = a.sendConfirmation(ctx, req, p.user, email)
		if err == nil && !mailed {
			err = a.store.SetEmail(ctx, p.user, email)
		}
		if err == nil && mailed && old != "" {
			a.alertTo(ctx, p.user, old, "email_changing", email, ip)
		}
		a.audit_(ctx, p.user, "", "email_change_requested", email, ip)
	}
	if err != nil {
		a.failed(w, "email", err)
		return false
	}
	return true
}

// emailConfirm takes the token of a confirmation link.
func (a *app) emailConfirm(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, req, &in) {
		return
	}
	email, err := a.store.ConfirmEmail(req.Context(), p.user, in.Token)
	if err != nil {
		a.failed(w, "email", err)
		return
	}
	if email == "" {
		writeError(w, http.StatusBadRequest, "expired")
		return
	}
	a.audit_(req.Context(), p.user, "", "email_verified", email, clientIP(req))
	a.profile(w, req, p)
}

// emailResend sends the confirmation link again: for the address waiting,
// or the unconfirmed one in the profile.
func (a *app) emailResend(w http.ResponseWriter, req *http.Request, p *principal) {
	var in struct{}
	if !readJSON(w, req, &in) || !a.fresh(w, p) {
		return
	}
	st, err := a.store.Email(req.Context(), p.user)
	if err != nil {
		a.failed(w, "email", err)
		return
	}
	addr := st.Pending
	if addr == "" && !st.Verified {
		addr = st.Email
	}
	if addr == "" {
		writeError(w, http.StatusConflict, "nothing_to_confirm")
		return
	}
	mailed, err := a.sendConfirmation(req.Context(), req, p.user, addr)
	switch {
	case err != nil:
		a.failed(w, "email", err)
	case !mailed:
		writeError(w, http.StatusConflict, "mail_off")
	default:
		a.profile(w, req, p)
	}
}

/* ---------- admin ---------- */

// smtpTest sends a test mail to the admin's own confirmed address and waits
// for the answer, so the settings page can say what went wrong.
func (a *app) smtpTest(w http.ResponseWriter, req *http.Request, p *principal) {
	ctx := req.Context()
	if !a.fresh(w, p) {
		return
	}
	if a.demo {
		writeError(w, http.StatusConflict, "demo")
		return
	}
	v, ok := a.mailValues(ctx)
	if !ok {
		writeError(w, http.StatusConflict, "mail_off")
		return
	}
	addr, err := a.store.VerifiedEmail(ctx, p.user)
	if err != nil {
		a.failed(w, "email", err)
		return
	}
	if addr == "" {
		writeError(w, http.StatusConflict, "no_address")
		return
	}
	key := "user:" + strconv.FormatInt(p.user.ID, 10)
	if a.mailLimit.Failures(key) >= mailPerUser {
		writeError(w, http.StatusTooManyRequests, "too_many")
		return
	}
	a.mailLimit.Fail(key)
	a.mailLimit.Fail("all")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = a.outbox.Send(ctx, mail.Message{To: addr, Subject: v.Title + ": test mail", Body: a.mailBody(v, mailLang(v), "This is a test mail from the settings. E-mail works.", "", false)})
	answer := map[string]any{"ok": err == nil, "to": addr}
	if err != nil {
		answer["error"] = err.Error()
	}
	a.audit_(ctx, p.user, "", "mail_test", addr, clientIP(req))
	writeJSON(w, http.StatusOK, answer)
}

// mailEnrolLink mails an enrolment link to u's confirmed address.
func (a *app) mailEnrolLink(ctx context.Context, req *http.Request, u *auth.User, token string, expires time.Time) (string, bool) {
	v, ok := a.mailValues(ctx)
	if !ok {
		return "mail_off", false
	}
	addr, err := a.store.VerifiedEmail(ctx, u)
	if err != nil || addr == "" {
		return "no_address", false
	}
	link := a.siteURL(req) + "/#enrol=" + token
	when := expires.In(v.Zone).Format("2006-01-02 15:04")
	subject, line := "Set up your second factor", "Your account %s needs a second factor. Set up the first one with this link: open it, sign in and add a passkey or an authenticator app. It works once, until %s. If you didn't expect this mail, ignore it and tell your admin."
	if mailLang(v) == "de" {
		subject, line = "Richte deinen zweiten Faktor ein", "Dein Konto %s braucht einen zweiten Faktor. Richte den ersten mit diesem Link ein: öffnen, anmelden und einen Passkey oder eine Authenticator-App hinzufügen. Er funktioniert einmal, bis %s. Hast du diese Mail nicht erwartet, ignoriere sie und sag deinem Admin Bescheid."
	}
	body := fmt.Sprintf(line, u.Name, when) + "\n\n" + link + "\n\n" + fmt.Sprintf(mailWords[mailLang(v)]["sign"], v.Title) + "\n"
	if !a.mailTo(ctx, u.ID, addr, v.Title+": "+subject, body) {
		return "not_sent", false
	}
	return "", true
}
