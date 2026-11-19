package main

import (
	"context"
	"strings"
	"testing"

	"eumetria/internal/auth"
)

func TestCheckinsFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	if err := e.store.SaveRole(ctx, "Self", []string{auth.ViewHealth, auth.EditCheckin}); err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateUser(ctx, "self", coachPassword, "Self")
	if err != nil {
		t.Fatal(err)
	}
	self := e.signIn(t, "self", coachPassword, false)
	// Writing needs a second factor outside -demo.
	if res, body := e.do(t, self, "PUT", "/api/checkins/2026-11-18", `{"energy":4}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("without a second factor = %d %s", res.StatusCode, body)
	}
	if err := e.store.SetTOTP(ctx, u, e.secret, 0); err != nil {
		t.Fatal(err)
	}
	res, body := e.do(t, self, "PUT", "/api/checkins/2026-11-18", `{"energy":2,"soreness":4,"tags":["sick"],"note":"cold"}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"tags":["sick"]`) || !strings.Contains(body, `"updated_by":"self"`) {
		t.Fatalf("check-in = %d %s", res.StatusCode, body)
	}
	// The sick day leaves the scores' baselines.
	if !e.data.skip["2026-11-18"] {
		t.Error("the stats don't know the sick day")
	}
	for in, want := range map[string]int{`{"energy":7}`: 400, `{"tags":["hangover"]}`: 400} {
		if res, _ := e.do(t, self, "PUT", "/api/checkins/2026-11-18", in, sameSite); res.StatusCode != want {
			t.Errorf("%s = %d", in, res.StatusCode)
		}
	}
	if res, _ := e.do(t, self, "PUT", "/api/checkins/18.11.2026", `{}`, sameSite); res.StatusCode != 400 {
		t.Errorf("bad date = %d", res.StatusCode)
	}

	// A viewer reads them and can't write; without view.health it's refused.
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)
	if res, body := e.do(t, anna, "GET", "/api/checkins?from=2026-11-01&to=2026-11-30", ""); res.StatusCode != 200 || !strings.Contains(body, `"energy":2`) || !strings.Contains(body, `"late_meal"`) {
		t.Errorf("anna's = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "PUT", "/api/checkins/2026-11-18", `{"energy":5}`, sameSite); res.StatusCode != 403 {
		t.Errorf("anna writes = %d", res.StatusCode)
	}
	if res, _ := e.do(t, coach, "GET", "/api/checkins", ""); res.StatusCode != 403 {
		t.Errorf("coach reads = %d", res.StatusCode)
	}

	if res, body := e.do(t, self, "DELETE", "/api/checkins/2026-11-18", "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("delete = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, self, "DELETE", "/api/checkins/2026-11-18", "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("delete again = %d %s", res.StatusCode, body)
	}
	if e.data.skip["2026-11-18"] {
		t.Errorf("the sick day stays after the delete: %v", e.data.skip)
	}
}
