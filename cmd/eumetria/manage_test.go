package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
)

var jsonBody = []reqOpt{sameSite, header("Content-Type", "application/json")}

// userID is the id of the account name, as the admin API takes it.
func (e *testEnv) userID(t *testing.T, name string) string {
	t.Helper()
	u, err := e.store.UserByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return itoa(u.ID)
}

func TestAdminManagesUsers(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)

	// Only admins, with their second factor.
	for _, c := range []struct {
		who  string
		pw   string
		want int
	}{{"anna", viewerPassword, 403}, {"newbie", adminPassword + "!", 403}} {
		if res, _ := e.do(t, e.signIn(t, c.who, c.pw, false), "GET", "/api/admin/users", ""); res.StatusCode != c.want {
			t.Errorf("%s lists users = %d, want %d", c.who, res.StatusCode, c.want)
		}
	}

	res, body := e.do(t, admin, "GET", "/api/admin/users", "")
	var list []auth.UserInfo
	if err := json.Unmarshal([]byte(body), &list); err != nil || res.StatusCode != 200 || len(list) != 4 {
		t.Fatalf("users = %d %s", res.StatusCode, body)
	}
	for _, u := range list {
		if u.Name == "patrick" && (!u.SecondFactor || u.Sessions != 1 || u.LastSeen == nil || u.Role != auth.RoleAdmin) {
			t.Errorf("patrick = %+v", u)
		}
	}

	// A new user signs in with the password the admin gave.
	if res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"bob","password":"short","role":"viewer"}`, sameSite); res.StatusCode != 400 || !strings.Contains(body, "too_short") {
		t.Errorf("weak password = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"anna","password":"`+coachPassword+`","role":"viewer"}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "name_taken") {
		t.Errorf("name taken = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, admin, "POST", "/api/admin/users", `{"name":"bob","password":"`+coachPassword+`","role":"Coach"}`, sameSite); res.StatusCode != 201 {
		t.Fatalf("new user = %d %s", res.StatusCode, body)
	}
	bob := e.signIn(t, "bob", coachPassword, false)
	if out := e.statsOf(t, bob); strings.Join(out.Scopes, " ") != "view.nutrition view.foods" {
		t.Errorf("bob's scopes = %v", out.Scopes)
	}

	// Another role, then disabled: the session ends at once.
	bobID := e.userID(t, "bob")
	if res, _ := e.do(t, admin, "PATCH", "/api/admin/users/"+bobID, `{"role":"viewer"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("role change = %d", res.StatusCode)
	}
	if out := e.statsOf(t, bob); len(out.Scopes) != len(auth.ViewScopes) {
		t.Errorf("bob's scopes after the change = %v", out.Scopes)
	}
	if res, _ := e.do(t, admin, "PATCH", "/api/admin/users/"+bobID, `{"disabled":true}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("disable = %d", res.StatusCode)
	}
	if res, _ := e.do(t, bob, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Errorf("a disabled user's session = %d, want 401", res.StatusCode)
	}
	if res, _ := e.do(t, e.client(), "POST", "/api/login", `{"name":"bob","password":"`+coachPassword+`"}`, sameSite); res.StatusCode != 401 {
		t.Errorf("a disabled user signs in: %d", res.StatusCode)
	}
	e.do(t, admin, "PATCH", "/api/admin/users/"+bobID, `{"disabled":false}`, sameSite)

	// A new password ends the sessions and replaces the old one.
	bob = e.signIn(t, "bob", coachPassword, false)
	if res, _ := e.do(t, admin, "POST", "/api/admin/users/"+bobID+"/password", `{"password":"`+coachPassword+`!!"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("password reset = %d", res.StatusCode)
	}
	if res, _ := e.do(t, bob, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Error("the session survived the password reset")
	}
	e.signIn(t, "bob", coachPassword+"!!", false)

	// The admin can't lock themselves out.
	me := e.userID(t, "patrick")
	for _, x := range []struct{ path, body, why string }{
		{"/api/admin/users/" + me, `{"disabled":true}`, "self"},
		{"/api/admin/users/" + me, `{"role":"viewer"}`, "self"},
		{"/api/admin/users/" + me + "/password", `{"password":"` + coachPassword + `"}`, "self"},
	} {
		method := "PATCH"
		if strings.HasSuffix(x.path, "/password") {
			method = "POST"
		}
		if res, body := e.do(t, admin, method, x.path, x.body, sameSite); res.StatusCode != 409 || !strings.Contains(body, x.why) {
			t.Errorf("%s %s = %d %s", x.path, x.body, res.StatusCode, body)
		}
	}
	if res, _ := e.do(t, admin, "PATCH", "/api/admin/users/999", `{"disabled":true}`, sameSite); res.StatusCode != 404 {
		t.Errorf("unknown user = %d", res.StatusCode)
	}

	// Changes need a recent sign-in; reading doesn't.
	e.now = e.now.Add(11 * time.Minute)
	if res, body := e.do(t, admin, "PATCH", "/api/admin/users/"+bobID, `{"disabled":true}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("change 11 minutes after sign-in = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, admin, "GET", "/api/admin/users", ""); res.StatusCode != 200 {
		t.Errorf("reading 11 minutes after sign-in = %d", res.StatusCode)
	}
}

// The last admin can't go, not even through another admin.
func TestTheLastAdminStays(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	newbie, _ := e.store.UserByName(ctx, "newbie")
	e.store.SetRole(ctx, newbie, auth.RoleViewer)
	admin := e.signIn(t, "patrick", adminPassword, false)
	// A second admin through a role of their own, then the first one goes.
	e.store.SaveRole(ctx, "Boss", []string{auth.ScopeAdmin})
	e.store.SetRole(ctx, newbie, "Boss")
	if res, _ := e.do(t, admin, "PATCH", "/api/admin/users/"+itoa(newbie.ID), `{"role":"viewer"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("demoting the other admin = %d", res.StatusCode)
	}
	patrick, _ := e.store.UserByName(ctx, "patrick")
	if left, _ := e.store.AdminsLeft(ctx, patrick, auth.RoleViewer, false); left != 0 {
		t.Errorf("admins left without patrick = %d", left)
	}
	// patrick's role may not lose the admin scope while he holds it.
	if res, body := e.do(t, admin, "PUT", "/api/admin/roles/admin", `{"scopes":["view.nutrition"]}`, sameSite); res.StatusCode != 409 {
		t.Errorf("changing the built-in admin role = %d %s", res.StatusCode, body)
	}
	e.store.SetRole(ctx, patrick, "Boss")
	if res, body := e.do(t, admin, "PUT", "/api/admin/roles/Boss", `{"scopes":["view.nutrition"]}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "self") {
		t.Errorf("taking admin from one's own role = %d %s", res.StatusCode, body)
	}
}

func TestAdminManagesRoles(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	var roles struct {
		Roles  []auth.RoleInfo `json:"roles"`
		Scopes []string        `json:"scopes"`
	}
	_, body := e.do(t, admin, "GET", "/api/admin/roles", "")
	if json.Unmarshal([]byte(body), &roles); len(roles.Roles) != 3 || len(roles.Scopes) != len(auth.AllScopes) {
		t.Fatalf("roles = %s", body)
	}
	for _, x := range []struct {
		method, path, body string
		code               int
		why                string
	}{
		{"PUT", "/api/admin/roles/Family", `{"scopes":["view.nutrition","view.health"]}`, 204, ""},
		{"PUT", "/api/admin/roles/Family", `{"scopes":["view.routes"]}`, 400, "bad_scopes"},
		{"PUT", "/api/admin/roles/viewer", `{"scopes":[]}`, 409, "builtin"},
		{"PUT", "/api/admin/roles/%20bad", `{"scopes":[]}`, 400, "bad_role_name"},
		{"DELETE", "/api/admin/roles/Coach", "", 409, "in_use"},
		{"DELETE", "/api/admin/roles/Family", "", 204, ""},
		{"DELETE", "/api/admin/roles/Family", "", 404, "not_found"},
	} {
		res, body := e.do(t, admin, x.method, x.path, x.body, jsonBody...)
		if res.StatusCode != x.code || !strings.Contains(body, x.why) {
			t.Errorf("%s %s %s = %d %s, want %d %s", x.method, x.path, x.body, res.StatusCode, body, x.code, x.why)
		}
	}
}

// A user an admin asked to set up a second factor gets no data until then,
// but can still reach their security settings.
func TestARequiredSecondFactorHoldsTheData(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	anna := e.signIn(t, "anna", viewerPassword, false)
	id := e.userID(t, "anna")
	if res, _ := e.do(t, admin, "PATCH", "/api/admin/users/"+id, `{"require_second_factor":true}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("require = %d", res.StatusCode)
	}
	if res, body := e.do(t, anna, "GET", "/api/stats", ""); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("stats = %d %s", res.StatusCode, body)
	}
	var who me
	_, body := e.do(t, anna, "GET", "/api/me", "")
	if json.Unmarshal([]byte(body), &who); !who.NeedsSecondFactor {
		t.Errorf("me = %s", body)
	}
	if res, _ := e.do(t, anna, "GET", "/api/security", ""); res.StatusCode != 200 {
		t.Errorf("security = %d", res.StatusCode)
	}
	u, _ := e.store.UserByName(context.Background(), "anna")
	secret, _ := auth.NewTOTPSecret()
	e.store.SetTOTP(context.Background(), u, secret, 0)
	if res, _ := e.do(t, anna, "GET", "/api/stats", ""); res.StatusCode != 200 {
		t.Errorf("stats with a second factor = %d", res.StatusCode)
	}
}

func TestAdminEndsSessions(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	anna := e.signIn(t, "anna", viewerPassword, false)
	var list []struct {
		ID      int64  `json:"id"`
		User    string `json:"user"`
		Current bool   `json:"current"`
	}
	_, body := e.do(t, admin, "GET", "/api/admin/sessions", "")
	json.Unmarshal([]byte(body), &list)
	if len(list) != 2 {
		t.Fatalf("sessions = %s", body)
	}
	for _, s := range list {
		if (s.User == "patrick") != s.Current {
			t.Errorf("session %+v", s)
		}
		if s.User == "anna" {
			if res, _ := e.do(t, admin, "DELETE", "/api/admin/sessions/"+itoa(s.ID), "", jsonBody...); res.StatusCode != 204 {
				t.Errorf("ending anna's session = %d", res.StatusCode)
			}
		}
	}
	if res, _ := e.do(t, anna, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Error("anna's session survived")
	}
	_, body = e.do(t, admin, "GET", "/api/audit", "")
	if !strings.Contains(body, `"session_ended"`) {
		t.Errorf("audit log = %s", body)
	}
}
