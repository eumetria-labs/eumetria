package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
)

// Changing how an account signs in needs a recent proof of who one is:
// the sign-in, or confirming it again with what a stolen session cookie
// doesn't come with (pentest F18).
func TestStepUp(t *testing.T) {
	e := newTestEnv(t, true)
	patrick := e.signIn(t, "patrick", adminPassword, false)
	token := `{"name":"cron","scopes":["read"]}`
	if res, body := e.do(t, patrick, "POST", "/api/tokens", token, sameSite); res.StatusCode != 201 {
		t.Fatalf("a token right after signing in = %d %s", res.StatusCode, body)
	}
	e.now = e.now.Add(freshFor + time.Second)
	if res, body := e.do(t, patrick, "POST", "/api/tokens", token, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("a token later = %d %s", res.StatusCode, body)
	}
	// With a second factor, the password doesn't do; a recovery code
	// neither (it isn't an app's code).
	for body, want := range map[string]int{
		`{"password":"` + adminPassword + `"}`: 400,
		`{"code":"abcd-efgh-jkmn"}`:            401,
		`{"code":"000000"}`:                    401,
		`{}`:                                   400,
	} {
		if res, text := e.do(t, patrick, "POST", "/api/security/reauth", body, sameSite); res.StatusCode != want {
			t.Errorf("reauth %s = %d %s, want %d", body, res.StatusCode, text, want)
		}
	}
	if res, body := e.do(t, patrick, "POST", "/api/security/reauth", `{"code":"`+e.code()+`"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("reauth with the app = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, patrick, "POST", "/api/tokens", token, sameSite); res.StatusCode != 201 {
		t.Errorf("a token after confirming = %d %s", res.StatusCode, body)
	}
	// It lasts freshFor, then has to be confirmed again.
	e.now = e.now.Add(freshFor + time.Second)
	// The password of an account with a second factor changes only after a
	// confirmation too.
	change := `{"current":"` + adminPassword + `","new":"` + adminPassword + `2"}`
	if res, body := e.do(t, patrick, "POST", "/api/security/password", change, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("password change later = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, patrick, "POST", "/api/admin/users", `{"name":"x","password":"a long enough password","role":"viewer"}`, sameSite); res.StatusCode != 403 {
		t.Errorf("an admin change later = %d", res.StatusCode)
	}

	// Without a second factor the password confirms; changing the password
	// doesn't count as confirming (a stolen session and a known password).
	anna := e.signIn(t, "anna", viewerPassword, false)
	e.now = e.now.Add(freshFor + time.Second)
	change = `{"current":"` + viewerPassword + `","new":"` + viewerPassword + `2"}`
	if res, body := e.do(t, anna, "POST", "/api/security/password", change, sameSite); res.StatusCode != 204 {
		t.Fatalf("anna's password change = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, anna, "POST", "/api/security/recovery", "{}", sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("recovery codes after a password change = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "POST", "/api/security/reauth", `{"code":"123456"}`, sameSite); res.StatusCode != 400 {
		t.Errorf("a code without an app = %d", res.StatusCode)
	}
	if res, body := e.do(t, anna, "POST", "/api/security/reauth", `{"password":"`+viewerPassword+`2"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("reauth with the password = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "POST", "/api/security/recovery", "{}", sameSite); res.StatusCode != 200 {
		t.Errorf("recovery codes after confirming = %d", res.StatusCode)
	}

	// Wrong tries lock like signing in does.
	for range 5 {
		e.do(t, anna, "POST", "/api/security/reauth", `{"password":"wrong wrong wrong"}`, sameSite)
	}
	if res, _ := e.do(t, anna, "POST", "/api/security/reauth", `{"password":"`+viewerPassword+`2"}`, sameSite); res.StatusCode != 429 {
		t.Errorf("after five wrong tries = %d, want 429", res.StatusCode)
	}
	if log, _ := e.store.AuditLog(context.Background(), 50); !strings.Contains(jsonOf(t, log), "reauth_failed") {
		t.Error("wrong tries aren't in the audit log")
	}

	// A key confirms too: the ceremony starts with the user's own keys
	// (none here, so it says so). Go's client leaves the cookies out once
	// Host is changed (see TestPasskeyCeremoniesStart).
	cookies := func(r *http.Request) {
		for _, ck := range patrick.Jar.Cookies(r.URL) {
			r.AddCookie(ck)
		}
	}
	if res, body := e.do(t, patrick, "POST", "/api/security/reauth/key/begin", "{}", sameSite, cookies, host("localhost")); res.StatusCode != 400 || !strings.Contains(body, "no_keys") {
		t.Errorf("key begin without keys = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, patrick, "POST", "/api/security/reauth/key/finish", `{"credential":{}}`, sameSite, cookies, host("localhost")); res.StatusCode != 400 || !strings.Contains(body, "expired") {
		t.Errorf("key finish without begin = %d %s", res.StatusCode, body)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Admins make enrolment links for users without a second factor; the
// newest one counts, once.
func TestAdminMakesEnrolLinks(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	newbie := e.userID(t, "newbie")
	res, body := e.do(t, admin, "POST", "/api/admin/users/"+newbie+"/enrol-link", "{}", sameSite)
	var link struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires"`
	}
	if json.Unmarshal([]byte(body), &link); res.StatusCode != 201 || link.Token == "" || link.Expires.Sub(e.now) < 23*time.Hour {
		t.Fatalf("enrol link = %d %s", res.StatusCode, body)
	}
	res, body = e.do(t, admin, "POST", "/api/admin/users/"+newbie+"/enrol-link", "{}", sameSite)
	var second struct{ Token string }
	json.Unmarshal([]byte(body), &second)
	u, _ := e.store.UserByName(context.Background(), "newbie")
	if ok, _ := e.store.EnrolmentValid(context.Background(), u, link.Token); ok {
		t.Error("an older link still works after a new one was made")
	}
	if ok, _ := e.store.EnrolmentValid(context.Background(), u, second.Token); !ok {
		t.Error("the new link doesn't work")
	}
	// Someone with a factor needs none; others can't make them.
	if res, _ := e.do(t, admin, "POST", "/api/admin/users/"+e.userID(t, "patrick")+"/enrol-link", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("a link for an account with a factor = %d, want 409", res.StatusCode)
	}
	anna := e.signIn(t, "anna", viewerPassword, false)
	if res, _ := e.do(t, anna, "POST", "/api/admin/users/"+newbie+"/enrol-link", "{}", sameSite); res.StatusCode != 403 {
		t.Errorf("a viewer making a link = %d, want 403", res.StatusCode)
	}
	// Expired links don't work.
	e.now = e.now.Add(auth.EnrolTTL + time.Minute)
	if ok, _ := e.store.EnrolmentValid(context.Background(), u, second.Token); ok {
		t.Error("an expired link works")
	}
}

// Changes to how an account signs in show up for its user until dismissed,
// and for admins in the user list.
func TestSecurityNotices(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	anna := e.signIn(t, "anna", viewerPassword, false)
	e.do(t, anna, "POST", "/api/tokens", `{"name":"grafana","scopes":["read"]}`, sameSite)
	e.do(t, admin, "POST", "/api/admin/users/"+e.userID(t, "anna")+"/password", `{"password":"`+coachPassword+`!!"}`, sameSite)
	anna = e.signIn(t, "anna", coachPassword+"!!", false)
	var me struct{ Notices []auth.Notice }
	_, body := e.do(t, anna, "GET", "/api/me", "")
	json.Unmarshal([]byte(body), &me)
	if len(me.Notices) != 2 || me.Notices[0].Kind != "password_reset" || me.Notices[0].Detail != "patrick" || me.Notices[1].Kind != "token_created" || me.Notices[1].Detail != "grafana" {
		t.Fatalf("notices = %+v", me.Notices)
	}
	if _, body := e.do(t, admin, "GET", "/api/admin/users", ""); !strings.Contains(body, `"security_changed"`) {
		t.Errorf("users = %s", body)
	}
	if res, _ := e.do(t, anna, "POST", "/api/notices/seen", `{"up_to":`+itoa(me.Notices[0].ID)+`}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("seen = %d", res.StatusCode)
	}
	if _, body := e.do(t, anna, "GET", "/api/me", ""); !strings.Contains(body, `"notices":[]`) {
		t.Errorf("me after dismissing = %s", body)
	}
}

// -enrol-link prints a link for an account without a second factor and
// refuses one for an account that has a factor.
func TestEnrolLinkCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := config{dataDir: dir, dbPath: filepath.Join(dir, "app.db")}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s := auth.NewStore(d)
	u, _ := s.CreateUser(ctx, "admin", adminPassword, auth.RoleAdmin)
	d.Close()
	if err := runAccountCommand(ctx, cfg, accountCommand{enrolLink: "admin"}); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Open(ctx, cfg.dbPath)
	defer d.Close()
	s = auth.NewStore(d)
	var n int
	d.QueryRowContext(ctx, "SELECT count(*) FROM enrolments WHERE user_id = ? AND used_at IS NULL", u.ID).Scan(&n)
	if n != 1 {
		t.Errorf("%d enrolments, want 1", n)
	}
	secret, _ := auth.NewTOTPSecret()
	s.SetTOTP(ctx, u, secret, 0)
	if err := runAccountCommand(ctx, cfg, accountCommand{enrolLink: "admin"}); err == nil {
		t.Error("a link for an account with a factor")
	}
}

// Pentest re-test I1: a notice of a new way in takes a step-up to dismiss,
// so whoever holds a stolen session can't hide the key they added.
func TestNewFactorNoticeNeedsStepUp(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	anna := e.signIn(t, "anna", viewerPassword, false)
	u, _ := e.store.UserByName(ctx, "anna")
	e.store.AddNotice(ctx, u, "key_added", "evil key", "203.0.113.9", "")
	e.now = e.now.Add(10 * time.Minute) // the sign-in isn't recent any more
	if res, body := e.do(t, anna, "POST", "/api/notices/seen", `{"up_to":0}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Fatalf("dismiss a key_added notice with a stale session = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, anna, "POST", "/api/security/reauth", `{"password":"`+viewerPassword+`"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("reauth = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "POST", "/api/notices/seen", `{"up_to":0}`, sameSite); res.StatusCode != 204 {
		t.Errorf("dismiss after step-up = %d", res.StatusCode)
	}
	// Other notices need nothing more.
	e.store.AddNotice(ctx, u, "password_changed", "", "", "")
	e.now = e.now.Add(10 * time.Minute)
	if res, _ := e.do(t, anna, "POST", "/api/notices/seen", `{"up_to":0}`, sameSite); res.StatusCode != 204 {
		t.Errorf("dismiss password_changed = %d", res.StatusCode)
	}
}
