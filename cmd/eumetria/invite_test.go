package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/settings"
)

// inviteToken is the token in the latest invitation mail to addr.
func inviteToken(t *testing.T, f *fakeMail, addr string) string {
	t.Helper()
	got := f.to(addr)
	if len(got) == 0 {
		t.Fatalf("no mail to %s", addr)
	}
	m := linkInvite.FindStringSubmatch(got[len(got)-1].Body)
	if m == nil {
		t.Fatalf("no invitation link in %q", got[len(got)-1].Body)
	}
	return m[1]
}

func TestInvitation(t *testing.T) {
	e := newTestEnv(t, true)
	f := withMail(t, e)
	admin := e.signIn(t, "patrick", adminPassword, false)

	// An admin, invited: no password from the admin.
	res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"carla","role":"admin","email":"carla@example.org"}`, sameSite)
	if res.StatusCode != 201 || !strings.Contains(body, `"invited":true`) || !strings.Contains(body, `"mailed":true`) {
		t.Fatalf("invite = %d %s", res.StatusCode, body)
	}
	got := f.to("carla@example.org")
	if len(got) != 1 || !strings.Contains(got[0].Body, "carla") || !strings.Contains(got[0].Body, "patrick") || !strings.Contains(got[0].Subject, "Welcome") {
		t.Fatalf("mail = %+v", got)
	}
	token := inviteToken(t, f, "carla@example.org")
	if _, body := e.do(t, admin, "GET", "/api/admin/users", ""); !strings.Contains(body, `"name":"carla"`) || !strings.Contains(body, `"invited":true`) {
		t.Errorf("users = %s", body)
	}
	// Nobody signs in as her before she chose a password.
	if res, _ := e.do(t, e.client(), "POST", "/api/login", `{"name":"carla","password":"`+adminPassword+`"}`, sameSite); res.StatusCode != 401 {
		t.Errorf("sign-in before accepting = %d", res.StatusCode)
	}

	// The welcome page asks whose it is, then takes the password.
	c := e.client()
	if res, body := e.do(t, c, "POST", "/api/invite", `{"token":"`+token+`"}`, sameSite); res.StatusCode != 200 || !strings.Contains(body, `"name":"carla"`) || !strings.Contains(body, `"needs_second_factor":true`) {
		t.Errorf("check = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, c, "POST", "/api/invite/accept", `{"token":"`+token+`","password":"short"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "too_short") {
		t.Errorf("a short password = %d %s", res.StatusCode, body)
	}
	res, body = e.do(t, c, "POST", "/api/invite/accept", `{"token":"`+token+`","password":"carla chooses a long one"}`, sameSite)
	var ans struct{ Name, Enrol string }
	if json.Unmarshal([]byte(body), &ans); res.StatusCode != 200 || ans.Name != "carla" || ans.Enrol == "" {
		t.Fatalf("accept = %d %s", res.StatusCode, body)
	}
	// Signed in, the address confirmed, and her first factor set up with
	// the enrolment token that came with it.
	if _, body := e.do(t, c, "GET", "/api/me", ""); !strings.Contains(body, `"name":"carla"`) {
		t.Errorf("me = %s", body)
	}
	if v, _ := e.store.VerifiedEmail(context.Background(), mustUser(t, e, "carla")); v != "carla@example.org" {
		t.Errorf("address = %q", v)
	}
	if res, body := e.do(t, c, "POST", "/api/security/totp/begin", `{"enrol":"`+ans.Enrol+`"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("totp with the enrolment token = %d %s", res.StatusCode, body)
	}
	// Once only.
	if res, _ := e.do(t, e.client(), "POST", "/api/invite/accept", `{"token":"`+token+`","password":"another long password"}`, sameSite); res.StatusCode != 404 {
		t.Errorf("a second accept = %d", res.StatusCode)
	}

	// A viewer: no enrolment token; sent again, the old link stops working.
	e.do(t, admin, "POST", "/api/admin/users", `{"name":"dora","role":"viewer","email":"dora@example.org"}`, sameSite)
	old := inviteToken(t, f, "dora@example.org")
	if res, _ := e.do(t, admin, "POST", "/api/admin/users/"+e.userID(t, "dora")+"/invite", "{}", sameSite); res.StatusCode != 204 {
		t.Fatalf("send again = %d", res.StatusCode)
	}
	var fresh string
	for range 50 {
		if fresh = inviteToken(t, f, "dora@example.org"); fresh != old {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if res, _ := e.do(t, e.client(), "POST", "/api/invite", `{"token":"`+old+`"}`, sameSite); res.StatusCode != 404 {
		t.Error("the old invitation still works")
	}
	res, body = e.do(t, e.client(), "POST", "/api/invite/accept", `{"token":"`+fresh+`","password":"dora chooses a long one"}`, sameSite)
	if res.StatusCode != 200 || strings.Contains(body, "enrol") {
		t.Errorf("viewer accept = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, admin, "POST", "/api/admin/users/"+e.userID(t, "dora")+"/invite", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("sending an accepted invitation again = %d, want 409", res.StatusCode)
	}

	// Refused: a bad address; anything but a token.
	if res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"eve","role":"viewer","email":"not an address"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "bad_email") {
		t.Errorf("bad address = %d %s", res.StatusCode, body)
	}
	for range 20 {
		e.do(t, e.client(), "POST", "/api/invite", `{"token":"guess"}`, sameSite)
	}
	if res, _ := e.do(t, e.client(), "POST", "/api/invite", `{"token":"guess"}`, sameSite); res.StatusCode != 429 {
		t.Errorf("guessing tokens = %d, want 429 after 20", res.StatusCode)
	}
}

// Without a mail server nobody can be invited, and no account is made.
func TestInvitationNeedsMail(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"carla","role":"viewer","email":"carla@example.org"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "mail_off") {
		t.Errorf("invite without mail = %d %s", res.StatusCode, body)
	}
	if _, err := e.store.UserByName(context.Background(), "carla"); err == nil {
		t.Error("the account was made anyway")
	}
}

func TestInviteCommand(t *testing.T) {
	f := withMail(t, nil)
	dir := t.TempDir()
	cfg := config{dataDir: dir, dbPath: dir + "/app.db"}
	ctx := context.Background()
	d, _ := db.Open(ctx, cfg.dbPath)
	conf, _ := settings.New(d, "")
	conf.Save(ctx, map[string]string{"smtp_host": "mail.example.org", "smtp_from": "eumetria@example.org"}, nil)
	d.Close()
	// Without a public URL the link would be incomplete: refused.
	if err := runAccountCommand(ctx, cfg, accountCommand{createUser: "carla", role: "admin", email: "carla@example.org"}); err == nil || !strings.Contains(err.Error(), "public URL") {
		t.Errorf("without a public URL: %v", err)
	}
	d, _ = db.Open(ctx, cfg.dbPath)
	conf, _ = settings.New(d, "")
	conf.Save(ctx, map[string]string{"public_url": "https://eumetria.example.org"}, nil)
	d.Close()
	if err := runAccountCommand(ctx, cfg, accountCommand{createUser: "carla", role: "admin", email: "carla@example.org"}); err != nil {
		t.Fatal(err)
	}
	if got := f.to("carla@example.org"); len(got) != 1 || !strings.Contains(got[0].Body, "https://eumetria.example.org/#invite=") {
		t.Errorf("mail = %+v", got)
	}
	d, _ = db.Open(ctx, cfg.dbPath)
	defer d.Close()
	if u, err := auth.NewStore(d).UserByName(ctx, "carla"); err != nil || u.Role != auth.RoleAdmin {
		t.Errorf("account: %+v %v", u, err)
	}
}
