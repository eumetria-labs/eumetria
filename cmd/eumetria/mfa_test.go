package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
)

func (e *testEnv) password(t *testing.T, name, password string) (loginAnswer, int) {
	t.Helper()
	c := e.client()
	body, _ := json.Marshal(loginRequest{Name: name, Password: password})
	res, text := e.do(t, c, "POST", "/api/login", string(body), sameSite)
	var ans loginAnswer
	json.Unmarshal([]byte(text), &ans)
	for _, ck := range res.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Errorf("%s got a session for the password alone", name)
		}
	}
	return ans, res.StatusCode
}

func (e *testEnv) secondStep(t *testing.T, ticket, kind, code string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(ticketRequest{Ticket: ticket, Kind: kind, Code: code})
	res, text := e.do(t, e.client(), "POST", "/api/login/code", string(body), sameSite)
	return res.StatusCode, text
}

func TestThePasswordAloneIsNotEnough(t *testing.T) {
	e := newTestEnv(t, true)
	ans, code := e.password(t, "patrick", adminPassword)
	if code != 200 || ans.Next != "second_factor" || ans.Ticket == "" || strings.Join(ans.Methods, ",") != "totp" {
		t.Fatalf("password step = %d %+v", code, ans)
	}
	if res, _ := e.do(t, e.client(), "GET", "/stats.json", ""); res.StatusCode != 401 {
		t.Error("data without the second factor")
	}
	if code, _ := e.secondStep(t, ans.Ticket, "totp", "000000"); code != 401 {
		t.Errorf("wrong code = %d, want 401", code)
	}
	good := e.code()
	if code, body := e.secondStep(t, ans.Ticket, "totp", good); code != 200 || !strings.Contains(body, `"sync":true`) {
		t.Fatalf("right code = %d %s", code, body)
	}
	// The ticket is used up, and so is the code.
	if code, body := e.secondStep(t, ans.Ticket, "totp", good); code != 401 || !strings.Contains(body, "expired") {
		t.Errorf("ticket used twice = %d %s", code, body)
	}
	ans, _ = e.password(t, "patrick", adminPassword)
	if code, _ := e.secondStep(t, ans.Ticket, "totp", good); code != 401 {
		t.Errorf("a code worked twice = %d", code)
	}

	// Wrong codes count against the account like wrong passwords: after
	// five the right one comes too late.
	ans, _ = e.password(t, "patrick", adminPassword)
	for range 4 {
		e.secondStep(t, ans.Ticket, "totp", "111111")
	}
	if code, body := e.secondStep(t, ans.Ticket, "totp", e.code()); code == 200 {
		t.Errorf("after 5 wrong codes = %d %s, want a refusal", code, body)
	}
	// A wrong ticket is no ticket.
	if code, _ := e.secondStep(t, "made-up", "totp", e.code()); code != 401 {
		t.Errorf("made-up ticket = %d", code)
	}
}

func TestRecoveryCodes(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	u, _ := e.store.UserByName(ctx, "patrick")
	codes, _ := e.store.NewRecoveryCodes(ctx, u)
	ans, _ := e.password(t, "patrick", adminPassword)
	if strings.Join(ans.Methods, ",") != "totp,recovery" {
		t.Errorf("methods %v", ans.Methods)
	}
	if code, _ := e.secondStep(t, ans.Ticket, "recovery", codes[0]); code != 200 {
		t.Fatalf("recovery code = %d", code)
	}
	ans, _ = e.password(t, "patrick", adminPassword)
	if code, _ := e.secondStep(t, ans.Ticket, "recovery", codes[0]); code != 401 {
		t.Errorf("a recovery code worked twice = %d", code)
	}
	log, _ := e.store.AuditLog(ctx, 20)
	var used bool
	for _, l := range log {
		used = used || (l.Action == "recovery_code_used" && l.Detail == "9 left")
	}
	if !used {
		t.Errorf("no audit entry for the used recovery code: %+v", log)
	}
}

func TestAnAdminNeedsASecondFactor(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.signIn(t, "newbie", adminPassword+"!", false)
	if _, body := e.do(t, c, "GET", "/api/me", ""); !strings.Contains(body, `"needs_second_factor":true`) || !strings.Contains(body, `"sync":false`) {
		t.Errorf("me = %s", body)
	}
	if res, _ := e.do(t, c, "GET", "/stats.json", ""); res.StatusCode != 200 {
		t.Error("an admin without a second factor can't read")
	}
	for _, x := range [][2]string{{"POST", "/api/sync"}, {"GET", "/api/audit"}, {"GET", "/api/status"}} {
		body := ""
		if x[0] == "POST" {
			body = "{}"
		}
		if res, text := e.do(t, c, x[0], x[1], body, sameSite); res.StatusCode != 403 || !strings.Contains(text, "second_factor_required") {
			t.Errorf("%s %s = %d %s", x[0], x[1], res.StatusCode, text)
		}
	}
	if len(e.calls) != 0 {
		t.Fatal("sync started")
	}

	// Setting up the app: a secret, then a code from it confirms it.
	res, body := e.do(t, c, "POST", "/api/security/totp/begin", "{}", sameSite)
	var setup struct{ Secret, URI, QR string }
	json.Unmarshal([]byte(body), &setup)
	if res.StatusCode != 200 || setup.Secret == "" || !strings.HasPrefix(setup.QR, "data:image/png;base64,") || !strings.Contains(setup.URI, "Eumetria:newbie") {
		t.Fatalf("totp begin = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, c, "POST", "/api/security/totp/confirm", `{"code":"000000"}`, sameSite); res.StatusCode != 400 {
		t.Errorf("confirm with a wrong code = %d", res.StatusCode)
	}
	now, _ := auth.TOTPCode(setup.Secret, e.now)
	if res, body := e.do(t, c, "POST", "/api/security/totp/confirm", `{"code":"`+now+`"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("confirm = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, c, "POST", "/api/sync", "{}", sameSite); res.StatusCode != 202 {
		t.Errorf("sync with a second factor = %d, want 202", res.StatusCode)
	}
	// The code that confirmed the app can't sign in as well.
	ans, _ := e.password(t, "newbie", adminPassword+"!")
	if code, _ := e.secondStep(t, ans.Ticket, "totp", now); code != 401 {
		t.Errorf("the confirming code signed in = %d", code)
	}
}

func TestChangingFactorsNeedsARecentSignIn(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.signIn(t, "anna", viewerPassword, true)
	if res, body := e.do(t, c, "POST", "/api/security/recovery", "{}", sameSite); res.StatusCode != 200 || strings.Count(body, "-") != 20 {
		t.Fatalf("recovery codes right after sign-in = %d %s", res.StatusCode, body)
	}
	e.now = e.now.Add(11 * time.Minute)
	for _, x := range [][3]string{
		{"POST", "/api/security/recovery", "{}"},
		{"POST", "/api/security/totp/begin", "{}"},
		{"DELETE", "/api/security/totp", ""},
		{"POST", "/api/security/key/begin", `{"name":"x"}`},
		{"DELETE", "/api/security/key/1", ""},
	} {
		opts := []reqOpt{sameSite, header("Content-Type", "application/json")}
		if res, body := e.do(t, c, x[0], x[1], x[2], opts...); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
			t.Errorf("%s %s 11 minutes after sign-in = %d %s", x[0], x[1], res.StatusCode, body)
		}
	}
	// Reading is fine, and says the sign-in is no longer recent.
	if _, body := e.do(t, c, "GET", "/api/security", ""); !strings.Contains(body, `"fresh":false`) || !strings.Contains(body, `"recovery_left":10`) {
		t.Errorf("security = %s", body)
	}
	c = e.signIn(t, "anna", viewerPassword, true)
	if res, _ := e.do(t, c, "POST", "/api/security/recovery", "{}", sameSite); res.StatusCode != 200 {
		t.Errorf("after signing in again = %d", res.StatusCode)
	}
}

func TestPasskeyCeremoniesStart(t *testing.T) {
	e := newTestEnv(t, true)
	// A passkey sign-in starts without a name: the options carry a fresh
	// challenge, ask for user verification and list no credentials.
	res, body := e.do(t, e.client(), "POST", "/api/login/passkey/begin", "{}", sameSite, host("localhost"))
	var begin struct {
		ID      string `json:"id"`
		Options struct {
			Challenge        string `json:"challenge"`
			RPID             string `json:"rpId"`
			UserVerification string `json:"userVerification"`
			Allow            []any  `json:"allowCredentials"`
		} `json:"options"`
	}
	json.Unmarshal([]byte(body), &begin)
	if res.StatusCode != 200 || begin.ID == "" || len(begin.Options.Challenge) < 32 || begin.Options.UserVerification != "required" || len(begin.Options.Allow) != 0 {
		t.Fatalf("passkey begin = %d %s", res.StatusCode, body)
	}
	// Finishing with garbage is refused, and the ceremony is gone after one try.
	if res, _ := e.do(t, e.client(), "POST", "/api/login/passkey/finish", `{"id":"`+begin.ID+`","credential":{}}`, sameSite, host("localhost")); res.StatusCode != 401 {
		t.Errorf("garbage credential = %d", res.StatusCode)
	}
	if res, body := e.do(t, e.client(), "POST", "/api/login/passkey/finish", `{"id":"`+begin.ID+`","credential":{}}`, sameSite, host("localhost")); res.StatusCode != 401 || !strings.Contains(body, "expired") {
		t.Errorf("second try = %d %s", res.StatusCode, body)
	}
	// Registering a key offers a passkey and leaves out none yet.
	c := e.signIn(t, "anna", viewerPassword, false)
	// Go's client leaves the jar's cookies out once Host is changed; a
	// browser on localhost would have its cookie for localhost anyway.
	cookies := func(r *http.Request) {
		for _, ck := range c.Jar.Cookies(r.URL) {
			r.AddCookie(ck)
		}
	}
	res, body = e.do(t, c, "POST", "/api/security/key/begin", `{"name":"Phone"}`, sameSite, cookies, host("localhost"))
	if res.StatusCode != 200 || !strings.Contains(body, `"residentKey":"preferred"`) || !strings.Contains(body, `"name":"anna"`) {
		t.Errorf("key begin = %d %s", res.StatusCode, body)
	}
}

// host sets the Host the server sees (an IP address is no WebAuthn
// relying party; browsers refuse it too).
func host(h string) reqOpt { return func(r *http.Request) { r.Host = h } }

func TestATicketEndsAfterFiveWrongTries(t *testing.T) {
	var s pendingStore
	id := s.put("", &pending{user: &auth.User{Name: "x"}})
	for range 4 {
		s.failed(id)
	}
	if s.get(id) == nil {
		t.Fatal("gone after 4 tries")
	}
	s.failed(id)
	if s.get(id) != nil {
		t.Error("still there after 5 tries")
	}
	// get hands out copies: changing one doesn't change the ticket.
	id = s.put("", &pending{user: &auth.User{Name: "x"}})
	s.get(id).tries = 99
	if s.get(id).tries != 0 {
		t.Error("get returned the ticket itself")
	}
}

// The origin passkeys are checked against: https for the real site, even
// though nginx hands the request over as plain http from the Docker bridge;
// plain http only on localhost, the one place browsers allow it.
func TestRelyingPartyOrigin(t *testing.T) {
	a := &app{}
	for _, x := range []struct{ host, remote, want string }{
		{"eumetria.example.com", "172.17.0.1:5000", "https://eumetria.example.com"},
		{"eumetria.example.com", "127.0.0.1:5000", "https://eumetria.example.com"},
		{"localhost:8080", "127.0.0.1:5000", "http://localhost:8080"},
		{"app.localhost:8080", "127.0.0.1:5000", "http://app.localhost:8080"},
	} {
		req := httptest.NewRequest("POST", "/api/login/passkey/begin", nil)
		req.Host, req.RemoteAddr = x.host, x.remote
		rp, err := a.relyingParty(req)
		if err != nil {
			t.Fatalf("%s: %v", x.host, err)
		}
		if got := rp.Config.RPOrigins; len(got) != 1 || got[0] != x.want {
			t.Errorf("%s: origins %v, want %s", x.host, got, x.want)
		}
	}
}
