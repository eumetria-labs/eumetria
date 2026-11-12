package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"eumetria/internal/auth"
)

// Pentest re-test N5: an app set up while the account was a viewer (with
// the password alone) doesn't carry over to admin rights. It is removed,
// the sessions end and the admin gets an enrolment link to pass on. One
// set up with a link stays.
func TestElevationDropsUnverifiedFactors(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	anna := e.signIn(t, "anna", viewerPassword, false)

	// As a viewer, anna (or whoever has her password) sets up an app.
	res, body := e.do(t, anna, "POST", "/api/security/totp/begin", "{}", sameSite)
	var setup struct{ Secret string }
	json.Unmarshal([]byte(body), &setup)
	if res.StatusCode != 200 {
		t.Fatalf("viewer totp begin = %d %s", res.StatusCode, body)
	}
	code, _ := auth.TOTPCode(setup.Secret, e.now)
	if res, body := e.do(t, anna, "POST", "/api/security/totp/confirm", `{"code":"`+code+`"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("viewer totp confirm = %d %s", res.StatusCode, body)
	}
	u, _ := e.store.UserByName(ctx, "anna")
	if _, o, ok, _ := e.store.TOTPOrigin(ctx, u); !ok || o.Via != auth.ViaPlain || o.Role != "viewer" {
		t.Errorf("origin of a viewer's app = %+v %v", o, ok)
	}

	// The admin makes her an admin: the app goes, and a link comes back.
	admin := e.signIn(t, "patrick", adminPassword, false)
	res, body = e.do(t, admin, "PATCH", "/api/admin/users/"+e.userID(t, "anna"), `{"role":"admin"}`, sameSite)
	var answer struct{ Enrol string }
	json.Unmarshal([]byte(body), &answer)
	if res.StatusCode != 200 || answer.Enrol == "" {
		t.Fatalf("promotion = %d %s, want an enrolment link", res.StatusCode, body)
	}
	if f, _ := e.store.Factors(ctx, u); f.Any() || f.RecoveryLeft != 0 {
		t.Errorf("factors after promotion = %+v, want none", f)
	}
	if res, _ := e.do(t, anna, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Errorf("anna's old session after promotion = %d, want 401", res.StatusCode)
	}
	if log, _ := e.store.AuditLog(ctx, 50); !strings.Contains(jsonOf(t, log), "factors_dropped") {
		t.Error("no factors_dropped in the audit log")
	}
	if list, _ := e.store.Notices(ctx, u, ""); len(list) == 0 || list[len(list)-1].Kind != "factors_dropped" && list[0].Kind != "factors_dropped" {
		t.Errorf("notices = %+v, want factors_dropped", list)
	}

	// Whoever knew the password signs in, but can't set up a factor
	// without the link; with it, it is stored as set up with a link…
	anna = e.signIn(t, "anna", viewerPassword, false)
	if res, body := e.do(t, anna, "POST", "/api/security/totp/begin", "{}", sameSite); res.StatusCode != 403 || !strings.Contains(body, "enrol_required") {
		t.Errorf("admin without factor, no link = %d %s", res.StatusCode, body)
	}
	_, body = e.do(t, anna, "POST", "/api/security/totp/begin", `{"enrol":"`+answer.Enrol+`"}`, sameSite)
	json.Unmarshal([]byte(body), &setup)
	code, _ = auth.TOTPCode(setup.Secret, e.now)
	if res, body := e.do(t, anna, "POST", "/api/security/totp/confirm", `{"code":"`+code+`"}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("confirm with the link = %d %s", res.StatusCode, body)
	}
	if _, o, _, _ := e.store.TOTPOrigin(ctx, u); o.Via != auth.ViaLink {
		t.Errorf("origin with a link = %+v", o)
	}

	// …and survives being made a viewer and an admin again.
	for _, role := range []string{"viewer", "admin"} {
		if res, body := e.do(t, admin, "PATCH", "/api/admin/users/"+e.userID(t, "anna"), `{"role":"`+role+`"}`, sameSite); res.StatusCode != 204 {
			t.Errorf("role %s = %d %s", role, res.StatusCode, body)
		}
	}
	if f, _ := e.store.Factors(ctx, u); !f.TOTP {
		t.Error("an app set up with a link was removed")
	}
}

// The same when a role gets rights that need a second factor: everyone
// with it loses the factors set up without a link.
func TestRoleEditDropsUnverifiedFactors(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	coach, _ := e.store.UserByName(ctx, "coach")
	secret, _ := auth.NewTOTPSecret()
	if err := e.store.SetTOTP(ctx, coach, secret, 0, auth.Origin{Via: auth.ViaPlain, Role: "Coach"}); err != nil {
		t.Fatal(err)
	}
	admin := e.signIn(t, "patrick", adminPassword, false)
	res, body := e.do(t, admin, "PUT", "/api/admin/roles/Coach", `{"scopes":["view.nutrition","view.foods","sync"]}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"coach"`) {
		t.Fatalf("role edit = %d %s, want a link for coach", res.StatusCode, body)
	}
	if f, _ := e.store.Factors(ctx, coach); f.TOTP {
		t.Error("coach kept the app set up without a link")
	}
	// The admin's own factors (unknown origin) are untouched: patrick
	// needed one all along.
	p, _ := e.store.UserByName(ctx, "patrick")
	if f, _ := e.store.Factors(ctx, p); !f.TOTP {
		t.Error("patrick lost the app")
	}
}
