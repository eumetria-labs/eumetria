package main

import (
	"context"
	"encoding/json"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
)

func TestProfile(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)

	res, body := e.do(t, anna, "GET", "/api/profile", "")
	var got profileInfo
	json.Unmarshal([]byte(body), &got)
	if res.StatusCode != 200 || got.Name != "anna" || got.FirstName != "" || got.Avatar != "" {
		t.Fatalf("empty profile = %d %s", res.StatusCode, body)
	}

	res, body = e.do(t, anna, "PUT", "/api/profile", `{"first_name":" Anna ","surname":"Müller","email":"anna@example.org","instagram":"@anna.m_1"}`, sameSite)
	json.Unmarshal([]byte(body), &got)
	if res.StatusCode != 200 || got.FirstName != "Anna" || got.Surname != "Müller" || got.Instagram != "anna.m_1" {
		t.Fatalf("saved = %d %s", res.StatusCode, body)
	}
	// Each user has their own.
	if _, body := e.do(t, coach, "GET", "/api/profile", ""); strings.Contains(body, "Anna") {
		t.Errorf("the coach sees anna's profile: %s", body)
	}
	for field, c := range map[string]string{
		`{"email":"nope"}`:                                 "bad_email",
		`{"email":"Anna <anna@example.org>"}`:              "bad_email",
		`{"instagram":"has space"}`:                        "bad_instagram",
		`{"instagram":"https://instagram.com/x"}`:          "bad_instagram",
		`{"instagram":"` + strings.Repeat("a", 31) + `"}`:  "bad_instagram",
		`{"first_name":"` + strings.Repeat("a", 65) + `"}`: "bad_person_name",
		`{"surname":"a\u0000b"}`:                           "bad_person_name",
		`{"role":"admin"}`:                                 "bad_request",
	} {
		if res, body := e.do(t, anna, "PUT", "/api/profile", field, sameSite); res.StatusCode != 400 || !strings.Contains(body, c) {
			t.Errorf("%s = %d %s, want %s", field, res.StatusCode, body, c)
		}
	}
	// Everything may be cleared again.
	if res, body := e.do(t, anna, "PUT", "/api/profile", `{}`, sameSite); res.StatusCode != 200 || strings.Contains(body, "Anna") {
		t.Errorf("cleared = %d %s", res.StatusCode, body)
	}

	// An API token has no profile.
	tok, _ := e.store.CreateAPIToken(context.Background(), mustUser(t, e, "anna"), "x", []string{"read"}, time.Time{})
	if res, _ := e.do(t, e.client(), "GET", "/api/profile", "", bearer(tok)); res.StatusCode != 403 {
		t.Errorf("a token reading the profile = %d, want 403", res.StatusCode)
	}
}

func TestAvatar(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)
	body := `{"image":"` + uploadImage(t, "secret payload") + `"}`

	res, out := e.do(t, anna, "POST", "/api/profile/avatar", body, sameSite)
	var got profileInfo
	json.Unmarshal([]byte(out), &got)
	if res.StatusCode != 200 || !strings.HasPrefix(got.Avatar, "avatars/") {
		t.Fatalf("upload = %d %s", res.StatusCode, out)
	}

	// Anyone signed in sees it: it signs notes. Nobody else does.
	res, img := e.do(t, coach, "GET", "/"+got.Avatar, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(res.Header.Get("Cache-Control"), "private") {
		t.Fatalf("picture = %d %v", res.StatusCode, res.Header)
	}
	if strings.Contains(img, "secret payload") {
		t.Error("the picture wasn't written anew")
	}
	if pic, err := jpeg.Decode(strings.NewReader(img)); err != nil || pic.Bounds().Dx() != pic.Bounds().Dy() {
		t.Errorf("picture: %v, %v", err, pic)
	}
	if res, _ := e.do(t, e.client(), "GET", "/"+got.Avatar, ""); res.StatusCode != 401 {
		t.Errorf("signed out = %d, want 401", res.StatusCode)
	}
	for _, path := range []string{"/avatars/999", "/avatars/..%2Fapp.db", "/avatars/0", "/avatars/-1"} {
		if res, _ := e.do(t, anna, "GET", path, ""); res.StatusCode != 404 {
			t.Errorf("%s = %d, want 404", path, res.StatusCode)
		}
	}

	// The page learns about it, and so do notes.
	_, who := e.do(t, anna, "GET", "/api/me", "")
	if !strings.Contains(who, got.Avatar) {
		t.Errorf("me = %s", who)
	}

	for name, c := range map[string]struct {
		body string
		code int
	}{
		"not an image": {`{"image":"aGVsbG8gd29ybGQ="}`, 400},
		"too big":      {`{"image":"` + strings.Repeat("A", maxImageBody) + `"}`, 413},
	} {
		if res, _ := e.do(t, anna, "POST", "/api/profile/avatar", c.body, sameSite); res.StatusCode != c.code {
			t.Errorf("%s = %d, want %d", name, res.StatusCode, c.code)
		}
	}
	if res, _ := e.do(t, anna, "POST", "/api/profile/avatar", body, header("Sec-Fetch-Site", "cross-site")); res.StatusCode != 403 {
		t.Errorf("a cross-site upload = %d, want 403", res.StatusCode)
	}

	res, out = e.do(t, anna, "DELETE", "/api/profile/avatar", "", jsonBody...)
	json.Unmarshal([]byte(out), &got)
	if res.StatusCode != 200 || got.Avatar != "" {
		t.Errorf("removed = %d %s", res.StatusCode, out)
	}
	if res, _ := e.do(t, coach, "GET", "/avatars/"+itoa(mustUser(t, e, "anna").ID), ""); res.StatusCode != 404 {
		t.Errorf("after removing = %d, want 404", res.StatusCode)
	}
}

func TestNoteShowsItsWritersPicture(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	if res, _ := e.do(t, trainer, "POST", "/api/profile/avatar", `{"image":"`+uploadImage(t, "x")+`"}`, sameSite); res.StatusCode != 200 {
		t.Fatal("no picture")
	}
	_, body := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"x"}`, sameSite)
	if !strings.Contains(body, `"updated_by_avatar":"avatars/`) {
		t.Errorf("note = %s", body)
	}
}

func TestTokensInTheProfile(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)

	res, body := e.do(t, anna, "POST", "/api/tokens", `{"name":"grafana","scopes":["read"],"days":30}`, sameSite)
	var made struct {
		Token string `json:"token"`
	}
	json.Unmarshal([]byte(body), &made)
	if res.StatusCode != 201 || !strings.HasPrefix(made.Token, auth.APITokenPrefix) {
		t.Fatalf("new token = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, e.client(), "GET", "/api/stats", "", bearer(made.Token)); res.StatusCode != 200 {
		t.Errorf("using it = %d", res.StatusCode)
	}

	// Listed without the token, and only for its owner.
	res, body = e.do(t, anna, "GET", "/api/tokens", "")
	var list []auth.TokenInfo
	json.Unmarshal([]byte(body), &list)
	if res.StatusCode != 200 || len(list) != 1 || list[0].Name != "grafana" || list[0].Expires == nil || list[0].LastUsed == nil {
		t.Fatalf("tokens = %d %s", res.StatusCode, body)
	}
	if strings.Contains(body, made.Token) || strings.Contains(body, "hash") {
		t.Errorf("the list gives the token away: %s", body)
	}
	if _, body := e.do(t, coach, "GET", "/api/tokens", ""); body != "[]\n" {
		t.Errorf("the coach's tokens = %s", body)
	}
	id := itoa(list[0].ID)
	if res, _ := e.do(t, coach, "DELETE", "/api/tokens/"+id, "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("revoking someone else's = %d, want 404", res.StatusCode)
	}

	for name, c := range map[string]struct {
		body string
		code int
		why  string
	}{
		"no name":      {`{"name":" ","scopes":["read"]}`, 400, "bad_name"},
		"no scopes":    {`{"name":"x","scopes":[]}`, 400, "bad_scopes"},
		"admin scope":  {`{"name":"x","scopes":["admin"]}`, 400, "bad_scopes"},
		"bad expiry":   {`{"name":"x","scopes":["read"],"days":-1}`, 400, "bad_request"},
		"may not sync": {`{"name":"x","scopes":["sync"]}`, 403, "forbidden"},
	} {
		if res, body := e.do(t, anna, "POST", "/api/tokens", c.body, sameSite); res.StatusCode != c.code || !strings.Contains(body, c.why) {
			t.Errorf("%s = %d %s, want %d %s", name, res.StatusCode, body, c.code, c.why)
		}
	}

	// An admin without a second factor can't get around it with a token.
	newbie := e.signIn(t, "newbie", adminPassword+"!", false)
	if res, body := e.do(t, newbie, "POST", "/api/tokens", `{"name":"x","scopes":["sync"]}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("sync token without a second factor = %d %s", res.StatusCode, body)
	}
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "POST", "/api/tokens", `{"name":"cron","scopes":["sync"]}`, sameSite); res.StatusCode != 201 {
		t.Errorf("a sync token = %d %s", res.StatusCode, body)
	}

	// A token can't make tokens or see them.
	if res, _ := e.do(t, e.client(), "POST", "/api/tokens", `{"name":"x","scopes":["read"]}`, bearer(made.Token), header("Content-Type", "application/json")); res.StatusCode != 403 {
		t.Errorf("a token making a token = %d, want 403", res.StatusCode)
	}

	// An old sign-in has to sign in again, but can always revoke.
	e.now = e.now.Add(time.Hour)
	if res, body := e.do(t, anna, "POST", "/api/tokens", `{"name":"late","scopes":["read"]}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("after an hour = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, anna, "DELETE", "/api/tokens/"+id, "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("revoking = %d", res.StatusCode)
	}
	if res, _ := e.do(t, e.client(), "GET", "/api/stats", "", bearer(made.Token)); res.StatusCode != 401 {
		t.Errorf("a revoked token = %d, want 401", res.StatusCode)
	}

	log, _ := e.store.AuditLog(context.Background(), 100)
	saw := map[string]bool{}
	for _, entry := range log {
		saw[entry.Action] = true
		if strings.Contains(entry.Detail, auth.APITokenPrefix) {
			t.Errorf("the audit log holds a token: %+v", entry)
		}
	}
	if !saw["token_created"] || !saw["token_deleted"] {
		t.Errorf("audit log = %v", saw)
	}
}
