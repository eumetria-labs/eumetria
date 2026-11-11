package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/notes"
	"eumetria/internal/store"
)

// A role that may write notes, and one that may only read them.
func (e *testEnv) writer(t *testing.T) *http.Client {
	t.Helper()
	ctx := context.Background()
	scopes := []string{auth.ViewHealth, auth.ViewNotes, auth.EditNotes}
	if err := e.store.SaveRole(ctx, "Trainer", scopes); err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateUser(ctx, "trainer", coachPassword, "Trainer")
	if err != nil {
		t.Fatal(err)
	}
	// Writing needs a second factor (outside -demo): the same app as patrick's.
	if err := e.store.SetTOTP(ctx, u, e.secret, 0); err != nil {
		t.Fatal(err)
	}
	return e.signIn(t, "trainer", coachPassword, false)
}

func noteList(t *testing.T, body string) []notes.Note {
	t.Helper()
	var out []notes.Note
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("notes = %s: %v", body, err)
	}
	return out
}

func TestNotesFollowTheScopes(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)

	res, body := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"  # Warm-Up\n- 6 Sit-Ups\n"}`, sameSite)
	if res.StatusCode != 201 {
		t.Fatalf("new note = %d %s", res.StatusCode, body)
	}
	var n notes.Note
	json.Unmarshal([]byte(body), &n)
	if n.Date != "2026-10-20" || n.Markdown != "# Warm-Up\n- 6 Sit-Ups" || n.CreatedBy != "trainer" {
		t.Errorf("note = %+v", n)
	}
	if res, _ := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"changed"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("changing it = %d", res.StatusCode)
	}

	// A viewer reads it and can't change it; without view.notes it's refused.
	res, body = e.do(t, anna, "GET", "/api/notes", "")
	if list := noteList(t, body); res.StatusCode != 200 || len(list) != 1 || list[0].Markdown != "changed" || list[0].WorkoutID != "w1" {
		t.Errorf("anna's notes = %d %s", res.StatusCode, body)
	}
	for _, c := range []struct {
		who          *http.Client
		method, path string
		want         int
	}{
		{anna, "PUT", "/api/notes/w1", 403},
		{anna, "DELETE", "/api/notes/w1", 403},
		{coach, "GET", "/api/notes", 403},
		{coach, "PUT", "/api/notes/w1", 403},
		{coach, "DELETE", "/api/notes/w1", 403},
		{trainer, "GET", "/api/admin/notes", 403},
	} {
		body := ""
		if c.method == "PUT" {
			body = `{"markdown":"x"}`
		}
		if res, _ := e.do(t, c.who, c.method, c.path, body, jsonBody...); res.StatusCode != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, res.StatusCode, c.want)
		}
	}

	// A token reads with "read" and never writes.
	tok, err := e.store.CreateAPIToken(context.Background(), mustUser(t, e, "trainer"), "script", []string{"read", "sync"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do(t, e.client(), "GET", "/api/notes", "", bearer(tok)); res.StatusCode != 200 {
		t.Errorf("a token reading = %d", res.StatusCode)
	}
	if res, _ := e.do(t, e.client(), "PUT", "/api/notes/w1", `{"markdown":"x"}`, bearer(tok), header("Content-Type", "application/json")); res.StatusCode != 403 {
		t.Errorf("a token writing = %d, want 403", res.StatusCode)
	}

	// An admin without a second factor can't use the rights of the role.
	newbie := e.signIn(t, "newbie", adminPassword+"!", false)
	if res, body := e.do(t, newbie, "PUT", "/api/notes/w1", `{"markdown":"x"}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("an admin without a second factor = %d %s", res.StatusCode, body)
	}

	// Every change is in the audit log, without the text.
	log, err := e.store.AuditLog(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	saw := map[string]bool{}
	for _, entry := range log {
		saw[entry.Action] = true
		if strings.Contains(entry.Detail, "changed") || strings.Contains(entry.Detail, "Warm-Up") {
			t.Errorf("the audit log holds the note's text: %+v", entry)
		}
	}
	if !saw["note_added"] || !saw["note_changed"] {
		t.Errorf("audit log = %v", saw)
	}

	if res, _ := e.do(t, trainer, "DELETE", "/api/notes/w1", "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("deleting it = %d", res.StatusCode)
	}
	if res, _ := e.do(t, trainer, "DELETE", "/api/notes/w1", "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("deleting it again = %d", res.StatusCode)
	}
}

func mustUser(t *testing.T, e *testEnv, name string) *auth.User {
	t.Helper()
	u, err := e.store.UserByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNotesAreChecked(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	for name, c := range map[string]struct {
		key, body string
		code      int
		reason    string
	}{
		"unknown workout": {"nope", `{"markdown":"x"}`, 404, "not_found"},
		"path tricks":     {"..%2F..%2Fetc", `{"markdown":"x"}`, 404, "not_found"},
		"empty":           {"w1", `{"markdown":" \n "}`, 400, "empty"},
		"unknown field":   {"w1", `{"markdown":"x","by":"me"}`, 400, "bad_request"},
		"over the limit":  {"w1", `{"markdown":"` + strings.Repeat("a", maxNote+1) + `"}`, 413, "too_long"},
		"umlauts count":   {"w1", `{"markdown":"` + strings.Repeat("ü", maxNote/2+1) + `"}`, 413, "too_long"},
		"far over it":     {"w1", `{"markdown":"` + strings.Repeat("a", maxNoteBody) + `"}`, 413, "too_long"},
	} {
		res, body := e.do(t, trainer, "PUT", "/api/notes/"+c.key, c.body, sameSite)
		if res.StatusCode != c.code || !strings.Contains(body, c.reason) {
			t.Errorf("%s = %d %s, want %d %s", name, res.StatusCode, body, c.code, c.reason)
		}
	}

	// Exactly at the limit is fine, and so is the most it can take as JSON.
	if res, body := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"`+strings.Repeat("a", maxNote)+`"}`, sameSite); res.StatusCode != 201 {
		t.Errorf("at the limit = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"`+strings.Repeat(`A`, maxNote)+`"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("escaped = %d %.100s", res.StatusCode, body)
	}

	// Cross-site writes are refused like every other change.
	res, _ := e.do(t, trainer, "DELETE", "/api/notes/w1", "", header("Sec-Fetch-Site", "cross-site"), header("Content-Type", "application/json"))
	if res.StatusCode != 403 {
		t.Errorf("a cross-site delete = %d, want 403", res.StatusCode)
	}
	res, _ = e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"x"}`, header("Sec-Fetch-Site", "same-origin"), header("Content-Type", "text/plain"))
	if res.StatusCode != 403 {
		t.Errorf("a form post = %d, want 403", res.StatusCode)
	}
}

func TestNotesByDate(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	if res, _ := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"x"}`, sameSite); res.StatusCode != 201 {
		t.Fatal("no note")
	}
	for query, want := range map[string]int{
		"":                               1,
		"?from=2026-10-20":               1,
		"?to=2026-10-20":                 1,
		"?from=2026-10-21":               0,
		"?to=2026-10-19":                 0,
		"?from=2026-10-01&to=2026-10-31": 1,
	} {
		res, body := e.do(t, trainer, "GET", "/api/notes"+query, "")
		if res.StatusCode != 200 || len(noteList(t, body)) != want {
			t.Errorf("%q = %d %s, want %d notes", query, res.StatusCode, body, want)
		}
	}
	if res, _ := e.do(t, trainer, "GET", "/api/notes?from=tomorrow", ""); res.StatusCode != 400 {
		t.Errorf("a bad date = %d, want 400", res.StatusCode)
	}
}

// A workout that disappears from the synced data keeps its note: nobody sees
// it next to a workout any more, and admins find it in their list.
func TestNotesOfWorkoutsThatAreGone(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, _ := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"keep me"}`, sameSite); res.StatusCode != 201 {
		t.Fatal("no note")
	}
	if _, body := e.do(t, admin, "GET", "/api/admin/notes", ""); len(noteList(t, body)) != 0 {
		t.Errorf("a note with its workout is listed: %s", body)
	}

	hist := testHistory()
	hist.Health = map[string]*store.HealthDay{}
	e.data.set(hist, e.now)

	if _, body := e.do(t, trainer, "GET", "/api/notes", ""); len(noteList(t, body)) != 0 {
		t.Errorf("a note without its workout is served: %s", body)
	}
	if res, _ := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"x"}`, sameSite); res.StatusCode != 404 {
		t.Errorf("changing it = %d, want 404", res.StatusCode)
	}
	if res, _ := e.do(t, trainer, "DELETE", "/api/notes/w1", "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("deleting it without being an admin = %d, want 404", res.StatusCode)
	}
	_, body := e.do(t, admin, "GET", "/api/admin/notes", "")
	if list := noteList(t, body); len(list) != 1 || list[0].Markdown != "keep me" || list[0].Date != "" {
		t.Fatalf("admin's list = %s", body)
	}
	if res, _ := e.do(t, admin, "DELETE", "/api/notes/w1", "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("an admin deleting it = %d", res.StatusCode)
	}
	if _, body := e.do(t, admin, "GET", "/api/admin/notes", ""); len(noteList(t, body)) != 0 {
		t.Errorf("still listed: %s", body)
	}
}

// Someone an admin asked for a second factor gets no notes either, until
// they set one up.
func TestNotesWaitForTheSecondFactor(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	if err := e.store.SetRequireSecondFactor(context.Background(), mustUser(t, e, "anna"), true); err != nil {
		t.Fatal(err)
	}
	// Every route that shows data says so, not only the ones that build the
	// stats (pentest F3): lifts, benchmark workouts and pictures too.
	for _, path := range []string{"/api/notes", "/api/stats", "/api/lifts", "/api/wods", "/avatars/1", "/days/x.json", "/photos/abc.jpg"} {
		if res, body := e.do(t, anna, "GET", path, ""); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
			t.Errorf("%s without the second factor = %d %s, want 403", path, res.StatusCode, body)
		}
	}
	// What she needs to set one up still works.
	for _, path := range []string{"/api/me", "/api/security", "/api/sessions", "/api/profile"} {
		if res, body := e.do(t, anna, "GET", path, ""); res.StatusCode != 200 {
			t.Errorf("%s = %d %s, want 200", path, res.StatusCode, body)
		}
	}
	// Setting one up needs the enrolment link the admin hands over (F19).
	if res, body := e.do(t, anna, "POST", "/api/security/totp/begin", "{}", sameSite); res.StatusCode != 403 || !strings.Contains(body, "enrol_required") {
		t.Errorf("setting up an app without a link = %d %s", res.StatusCode, body)
	}
	link, _, err := e.store.NewEnrolment(context.Background(), mustUser(t, e, "anna"))
	if err != nil {
		t.Fatal(err)
	}
	if res, body := e.do(t, anna, "POST", "/api/security/totp/begin", `{"enrol":"`+link+`"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("setting up an app = %d %s", res.StatusCode, body)
	}
	// So does a token she made before: it is her, without the factor.
	token, err := e.store.CreateAPIToken(context.Background(), mustUser(t, e, "anna"), "old", []string{"read"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do(t, e.client(), "GET", "/api/lifts", "", bearer(token)); res.StatusCode != 403 {
		t.Errorf("her token = %d, want 403", res.StatusCode)
	}
}

// Writing notes and lifts needs a second factor, also for a role that is
// neither admin nor sync (pentest F22); /api/me says so.
func TestWritersNeedASecondFactor(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	if err := e.store.SaveRole(ctx, "Scribe", []string{auth.ViewHealth, auth.ViewNotes, auth.EditNotes, auth.EditLifts}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateUser(ctx, "scribe", coachPassword, "Scribe"); err != nil {
		t.Fatal(err)
	}
	scribe := e.signIn(t, "scribe", coachPassword, false)
	if _, body := e.do(t, scribe, "GET", "/api/me", ""); !strings.Contains(body, `"needs_second_factor":true`) {
		t.Errorf("me = %s", body)
	}
	for _, rq := range []struct{ method, path, body string }{
		{"PUT", "/api/notes/w1", `{"markdown":"hi"}`},
		{"POST", "/api/lifts", `{"lift":"Snatch","kg":60,"date":"2026-10-20"}`},
		{"POST", "/api/wods", `{"name":"Fran","scoring":"time"}`},
	} {
		if res, body := e.do(t, scribe, rq.method, rq.path, rq.body, sameSite); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
			t.Errorf("%s %s = %d %s", rq.method, rq.path, res.StatusCode, body)
		}
	}
	// Reading stays open.
	if res, _ := e.do(t, scribe, "GET", "/api/lifts", ""); res.StatusCode != 200 {
		t.Errorf("reading lifts = %d", res.StatusCode)
	}
}

// A small image with a comment in its file, as base64 for the API.
func uploadImage(t *testing.T, comment string) string {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// A tEXt chunk right after the header: a valid file with a payload.
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(comment)))
	chunk = append(chunk, "tEXt"+comment...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	raw = slices.Insert(slices.Clone(raw), 33, chunk...)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestNoteImages(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)
	body := `{"image":"` + uploadImage(t, "secret payload") + `"}`

	res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite)
	var got struct {
		Src string `json:"src"`
	}
	json.Unmarshal([]byte(out), &got)
	if res.StatusCode != 201 || !strings.HasPrefix(got.Src, "note-images/") {
		t.Fatalf("upload = %d %s", res.StatusCode, out)
	}

	// Whoever reads notes sees it, as a new file without what was hidden in it.
	res, img := e.do(t, anna, "GET", "/"+got.Src, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(res.Header.Get("Cache-Control"), "private") {
		t.Fatalf("image = %d %v", res.StatusCode, res.Header)
	}
	if !strings.HasPrefix(img, "\xFF\xD8") || strings.Contains(img, "secret payload") {
		t.Errorf("the image wasn't written anew: %.40q", img)
	}
	if res, _ := e.do(t, coach, "GET", "/"+got.Src, ""); res.StatusCode != 403 {
		t.Errorf("without view.notes = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, e.client(), "GET", "/"+got.Src, ""); res.StatusCode != 401 {
		t.Errorf("signed out = %d, want 401", res.StatusCode)
	}

	for path, want := range map[string]int{
		"/note-images/00000000000000000000000000000000.jpg": 404,
		"/note-images/..%2Fapp.db":                          404,
		"/note-images/x.png":                                404,
	} {
		if res, _ := e.do(t, anna, "GET", path, ""); res.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, res.StatusCode, want)
		}
	}

	big := base64.StdEncoding.EncodeToString(make([]byte, maxImage+1))
	for name, c := range map[string]struct {
		who    *http.Client
		key    string
		body   string
		code   int
		reason string
	}{
		"not a writer":    {anna, "w1", body, 403, "forbidden"},
		"unknown workout": {trainer, "nope", body, 404, "not_found"},
		"not an image":    {trainer, "w1", `{"image":"aGVsbG8gd29ybGQ="}`, 400, "bad_image"},
		"not base64":      {trainer, "w1", `{"image":"%%%"}`, 400, "bad_request"},
		"too big":         {trainer, "w1", `{"image":"` + big + `"}`, 413, "too_long"},
		"far too big":     {trainer, "w1", `{"image":"` + strings.Repeat("A", maxImageBody) + `"}`, 413, "too_long"},
	} {
		res, out := e.do(t, c.who, "POST", "/api/notes/"+c.key+"/images", c.body, sameSite)
		if res.StatusCode != c.code || !strings.Contains(out, c.reason) {
			t.Errorf("%s = %d %.100s, want %d %s", name, res.StatusCode, out, c.code, c.reason)
		}
	}

	// The cross-site check holds for uploads too.
	res, _ = e.do(t, trainer, "POST", "/api/notes/w1/images", body, header("Sec-Fetch-Site", "cross-site"))
	if res.StatusCode != 403 {
		t.Errorf("a cross-site upload = %d, want 403", res.StatusCode)
	}
}

// nginx stops bodies over 1 KB; without its exception a note of any length
// would fail in production while every test here passes.
func TestNginxLetsNotesThrough(t *testing.T) {
	files, err := filepath.Glob("../../deploy/nginx*.conf")
	if err != nil || len(files) != 2 {
		t.Fatalf("nginx configs: %v, %v", files, err)
	}
	want := fmt.Sprintf("client_max_body_size %dm;", maxImageBody>>20)
	for _, f := range files {
		conf, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for path, limit := range map[string]string{
			"location /api/notes {":            want,
			"location = /api/profile/avatar {": want,
			"location = /api/admin/settings {": "client_max_body_size 8k;",
		} {
			_, block, found := strings.Cut(string(conf), path)
			block, _, _ = strings.Cut(block, "}")
			if !found || !strings.Contains(block, limit) {
				t.Errorf("%s has no %s with %s", f, path, limit)
			}
		}
		// A prefix ending in a slash makes nginx redirect the address without
		// it: GET /api/notes, the list, never reached the app that way.
		if m := regexp.MustCompile(`location /api/[^ ]+/ \{`).FindString(string(conf)); m != "" {
			t.Errorf("%s: %s redirects the address without the slash", f, m)
		}
	}
}

// Uploads no note embeds are limited per user, and the folder in all
// (pentest F21).
func TestNoteImageQuota(t *testing.T) {
	e := newTestEnv(t, true)
	trainer := e.writer(t)
	body := `{"image":"` + uploadImage(t, "x") + `"}`
	var first string
	for i := range maxPendingImages {
		res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite)
		if res.StatusCode != 201 {
			t.Fatalf("upload %d = %d %s", i, res.StatusCode, out)
		}
		if i == 0 {
			var got struct{ Src string }
			json.Unmarshal([]byte(out), &got)
			first = got.Src
		}
	}
	if res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite); res.StatusCode != 429 || !strings.Contains(out, "too_many_pending") {
		t.Fatalf("one more = %d %s", res.StatusCode, out)
	}
	// Once a note embeds one, there is room for another.
	if res, out := e.do(t, trainer, "PUT", "/api/notes/w1", `{"markdown":"![](`+first+`)"}`, sameSite); res.StatusCode/100 != 2 {
		t.Fatalf("note = %d %s", res.StatusCode, out)
	}
	if res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite); res.StatusCode != 201 {
		t.Errorf("after a note used one = %d %s", res.StatusCode, out)
	}
	// A full folder takes nothing more.
	old := maxImageDir
	maxImageDir = 1
	t.Cleanup(func() { maxImageDir = old })
	e = newTestEnv(t, true)
	trainer = e.writer(t)
	if res, _ := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite); res.StatusCode != 201 {
		t.Fatalf("first upload = %d", res.StatusCode)
	}
	if res, out := e.do(t, trainer, "POST", "/api/notes/w1/images", body, sameSite); res.StatusCode != 507 || !strings.Contains(out, "storage_full") {
		t.Errorf("full folder = %d %s", res.StatusCode, out)
	}
}

// Backups leave out note images no note embeds.
func TestBackupsSkipUnusedNoteImages(t *testing.T) {
	skip := skipUnusedNoteImages(func(name string) bool { return name == "used.jpg" })
	for name, want := range map[string]bool{
		"data/note-images/used.jpg":   false,
		"data/note-images/orphan.jpg": true,
		"data/photos/orphan.jpg":      false,
		"data/app.db":                 false,
	} {
		if got := skip(name); got != want {
			t.Errorf("skip(%s) = %v, want %v", name, got, want)
		}
	}
	if skipUnusedNoteImages(nil) != nil {
		t.Error("without knowing the notes, nothing may be skipped")
	}
}
