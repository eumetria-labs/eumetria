package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/mail"
	"eumetria/internal/settings"
	"eumetria/site"
)

// fakeMail records what would have been sent.
type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

// to waits a moment for the outbox, then returns what went to addr.
func (f *fakeMail) to(addr string) []mail.Message {
	var out []mail.Message
	for range 50 {
		f.mu.Lock()
		out = out[:0]
		for _, m := range f.sent {
			if m.To == addr {
				out = append(out, m)
			}
		}
		f.mu.Unlock()
		if len(out) > 0 {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (f *fakeMail) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// withMail sets up a mail server in e's settings and records the mail.
func withMail(t *testing.T, e *testEnv) *fakeMail {
	t.Helper()
	f := &fakeMail{}
	old := mailSender
	mailSender = func(settings.SMTP) mail.Sender { return f }
	t.Cleanup(func() { mailSender = old })
	if e != nil {
		if err := e.runner.settings.Save(context.Background(), map[string]string{
			"smtp_host": "mail.example.org", "smtp_from": "eumetria@example.org", "public_url": "https://eumetria.example.org",
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

var (
	linkToken  = regexp.MustCompile(`#(email|enrol)=([A-Za-z0-9_-]+)`)
	linkInvite = regexp.MustCompile(`#invite=([A-Za-z0-9_-]+)`)
)

func TestEmailIsConfirmedFirst(t *testing.T) {
	e := newTestEnv(t, true)
	f := withMail(t, e)
	anna := e.signIn(t, "anna", viewerPassword, false)

	// A new address gets a link; the profile shows it waiting.
	res, body := e.do(t, anna, "PUT", "/api/profile", `{"first_name":"Anna","email":"anna@example.org"}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"email_pending":"anna@example.org"`) || !strings.Contains(body, `"email_verified":false`) {
		t.Fatalf("save = %d %s", res.StatusCode, body)
	}
	got := f.to("anna@example.org")
	if len(got) != 1 || !strings.Contains(got[0].Body, "https://eumetria.example.org/#email=") || strings.Contains(got[0].Body, "\r") {
		t.Fatalf("confirmation mail = %+v", got)
	}
	token := linkToken.FindStringSubmatch(got[0].Body)[2]
	// Nothing goes there before it is confirmed.
	e.do(t, anna, "POST", "/api/tokens", `{"name":"early","scopes":["read"]}`, sameSite)
	time.Sleep(50 * time.Millisecond)
	if n := len(f.to("anna@example.org")); n != 1 {
		t.Errorf("%d mails before confirming, want 1", n)
	}
	if res, body := e.do(t, anna, "POST", "/api/profile/email/confirm", `{"token":"`+token+`"}`, sameSite); res.StatusCode != 200 || !strings.Contains(body, `"email_verified":true`) {
		t.Fatalf("confirm = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "POST", "/api/profile/email/confirm", `{"token":"`+token+`"}`, sameSite); res.StatusCode != 400 {
		t.Error("the link worked twice")
	}

	// Now the alerts come: a token made, with when and from where.
	e.do(t, anna, "POST", "/api/tokens", `{"name":"grafana","scopes":["read"]}`, sameSite)
	var alert *mail.Message
	for range 50 {
		for _, m := range f.to("anna@example.org") {
			if strings.Contains(m.Subject, "API token") {
				alert = &m
			}
		}
		if alert != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if alert == nil || !strings.Contains(alert.Body, "“grafana”") || !strings.Contains(alert.Body, "127.0.0.1") || !strings.Contains(alert.Body, "https://eumetria.example.org/#security") {
		t.Fatalf("alert = %+v", alert)
	}

	// Changing the address tells the old one; until confirmed it stays.
	e.now = e.now.Add(time.Minute)
	if res, body := e.do(t, anna, "PUT", "/api/profile", `{"first_name":"Anna","email":"thief@example.org"}`, sameSite); res.StatusCode != 200 {
		t.Fatalf("change = %d %s", res.StatusCode, body)
	}
	var told bool
	for range 50 {
		for _, m := range f.to("anna@example.org") {
			told = told || strings.Contains(m.Body, "thief@example.org")
		}
		if told {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !told {
		t.Error("the old address wasn't told about the change")
	}
	if v, _ := e.store.VerifiedEmail(context.Background(), mustUser(t, e, "anna")); v != "anna@example.org" {
		t.Errorf("verified address = %q", v)
	}
	// And it needs a recent proof of who one is.
	e.now = e.now.Add(freshFor + time.Minute)
	if res, body := e.do(t, anna, "PUT", "/api/profile", `{"first_name":"Anna","email":"other@example.org"}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("change later = %d %s", res.StatusCode, body)
	}
	// Other fields don't.
	if res, _ := e.do(t, anna, "PUT", "/api/profile", `{"first_name":"Annie","email":"anna@example.org"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("a name change later = %d", res.StatusCode)
	}
}

// Without a mail server an address is kept as given, not confirmed, and
// nothing is sent; -demo never sends.
func TestNoMailWithoutAServer(t *testing.T) {
	f := withMail(t, nil) // the sender, but no server in the settings
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	if res, body := e.do(t, anna, "PUT", "/api/profile", `{"email":"anna@example.org"}`, sameSite); res.StatusCode != 200 || !strings.Contains(body, `"email":"anna@example.org"`) || !strings.Contains(body, `"mail":false`) {
		t.Errorf("save = %d %s", res.StatusCode, body)
	}
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Error("mail without a server")
	}
	if res, _ := e.do(t, anna, "POST", "/api/profile/email/send", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("resend without a server = %d", res.StatusCode)
	}

	d := newTestEnv(t, true)
	d.runner.settings.Save(context.Background(), map[string]string{"smtp_host": "mail.example.org", "smtp_from": "eumetria@example.org"}, nil)
	d.srv.Close()
	d.runner.cfg.demo = true
	d.srv = httptest.NewTLSServer(newHandler(d.runner, site.Files, d.data, t.TempDir(), t.TempDir(), t.TempDir(), d.store))
	t.Cleanup(d.srv.Close)
	c := d.signIn(t, "anna", viewerPassword, false)
	d.do(t, c, "PUT", "/api/profile", `{"email":"anna@example.org"}`, sameSite)
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Error("-demo sent mail")
	}
}

// verified gives name a confirmed address directly.
func (e *testEnv) verified(t *testing.T, name, addr string) *auth.User {
	t.Helper()
	ctx := context.Background()
	u := mustUser(t, e, name)
	token, err := e.store.RequestEmail(ctx, u, addr)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.store.ConfirmEmail(ctx, u, token); err != nil || got != addr {
		t.Fatalf("confirm: %q %v", got, err)
	}
	return u
}

func TestAdminMail(t *testing.T) {
	e := newTestEnv(t, true)
	f := withMail(t, e)
	admin := e.signIn(t, "patrick", adminPassword, false)

	// The test mail needs the admin's own confirmed address.
	if res, body := e.do(t, admin, "POST", "/api/admin/settings/test/smtp", "{}", sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_address") {
		t.Errorf("test without an address = %d %s", res.StatusCode, body)
	}
	e.verified(t, "patrick", "patrick@example.org")
	res, body := e.do(t, admin, "POST", "/api/admin/settings/test/smtp", "{}", sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"ok":true`) || len(f.to("patrick@example.org")) != 1 {
		t.Fatalf("test mail = %d %s", res.StatusCode, body)
	}
	if _, body := e.do(t, admin, "GET", "/api/admin/settings", ""); !strings.Contains(body, `"last_sent"`) {
		t.Errorf("settings show no mail status: %.300s", body)
	}

	// An enrolment link by mail, to the confirmed address only.
	newbie := e.userID(t, "newbie")
	res, body = e.do(t, admin, "POST", "/api/admin/users/"+newbie+"/enrol-link", `{"mail":true}`, sameSite)
	if res.StatusCode != 201 || !strings.Contains(body, `"mail_error":"no_address"`) {
		t.Errorf("enrol link without an address = %d %s", res.StatusCode, body)
	}
	e.verified(t, "newbie", "newbie@example.org")
	res, body = e.do(t, admin, "POST", "/api/admin/users/"+newbie+"/enrol-link", `{"mail":true}`, sameSite)
	var ans struct {
		Token  string `json:"token"`
		Mailed bool   `json:"mailed"`
	}
	json.Unmarshal([]byte(body), &ans)
	got := f.to("newbie@example.org")
	if res.StatusCode != 201 || !ans.Mailed || len(got) != 1 || !strings.Contains(got[0].Body, "#enrol="+ans.Token) {
		t.Fatalf("enrol link by mail = %s, %+v", body, got)
	}
	if _, body := e.do(t, admin, "GET", "/api/admin/users", ""); !strings.Contains(body, `"email_verified":true`) {
		t.Errorf("users = %s", body)
	}

	// Admin rights for someone: every admin hears of it.
	before := len(f.to("patrick@example.org"))
	e.do(t, admin, "PATCH", "/api/admin/users/"+e.userID(t, "anna"), `{"role":"admin"}`, sameSite)
	var heard bool
	for range 50 {
		for _, m := range f.to("patrick@example.org")[before:] {
			heard = heard || strings.Contains(m.Subject, "anna")
		}
		if heard {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !heard {
		t.Error("admins weren't told about new admin rights")
	}
}

// One user gets at most mailPerUser mails an hour.
func TestMailLimit(t *testing.T) {
	e := newTestEnv(t, true)
	f := withMail(t, e)
	u := e.verified(t, "anna", "anna@example.org")
	ctx := context.Background()
	a := &app{store: e.store, runner: e.runner}
	a.startMail()
	for range mailPerUser + 5 {
		a.mailUser(ctx, u, "x", "y")
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.to("anna@example.org")); n != mailPerUser {
		t.Errorf("%d mails, want %d", n, mailPerUser)
	}
}

// -enrol-link -mail sends the link to the confirmed address, or says why not.
func TestEnrolLinkCommandMails(t *testing.T) {
	f := withMail(t, nil)
	dir := t.TempDir()
	cfg := config{dataDir: dir, dbPath: dir + "/app.db"}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s := auth.NewStore(d)
	u, _ := s.CreateUser(ctx, "admin", adminPassword, auth.RoleAdmin)
	conf, _ := settings.New(d, "")
	conf.Save(ctx, map[string]string{"smtp_host": "mail.example.org", "smtp_from": "eumetria@example.org", "public_url": "https://eumetria.example.org"}, nil)
	d.Close()
	if err := runAccountCommand(ctx, cfg, accountCommand{enrolLink: "admin", mail: true}); err == nil || !strings.Contains(err.Error(), "no confirmed") {
		t.Errorf("without an address: %v", err)
	}
	d, _ = db.Open(ctx, cfg.dbPath)
	s = auth.NewStore(d)
	token, _ := s.RequestEmail(ctx, u, "admin@example.org")
	s.ConfirmEmail(ctx, u, token)
	d.Close()
	if err := runAccountCommand(ctx, cfg, accountCommand{enrolLink: "admin", mail: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.to("admin@example.org"); len(got) != 1 || !strings.Contains(got[0].Body, "https://eumetria.example.org/#enrol=") {
		t.Errorf("mail = %+v", got)
	}
}

// Pentest re-test I2: confirmations to any address someone types use their
// own budget, so they can't hold up a security alert.
func TestBulkMailLeavesRoomForAlerts(t *testing.T) {
	e := newTestEnv(t, true)
	withMail(t, e)
	a := &app{runner: e.runner, store: e.store}
	a.startMail()
	ctx := context.Background()
	sent := 0
	for i := range 3 * bulkPerHour {
		if a.mailTo(ctx, bulkMail, int64(100+i/bulkPerUser), "someone@example.org", "confirm", "x") {
			sent++
		}
	}
	if sent != bulkPerHour {
		t.Errorf("%d bulk mails went out, want %d", sent, bulkPerHour)
	}
	if a.mailTo(ctx, bulkMail, 1, "x@example.org", "x", "x") {
		t.Error("a bulk mail over the hour's budget went out")
	}
	if !a.mailTo(ctx, alertMail, 1, "anna@example.org", "alert", "x") {
		t.Error("an alert was held up by bulk mail")
	}
	if _, own, _, perUser := bulkMail.budget(7); own == "user:7" || perUser != bulkPerUser {
		t.Errorf("bulk budget shares the alerts' key: %s", own)
	}
}
