package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/db"
	"eumetria/internal/stats"
	"eumetria/internal/store"
	"eumetria/site"
)

const (
	adminPassword  = "three sweaty protein shakes"
	viewerPassword = "a viewer with a long password"
	coachPassword  = "the coach sees what we eat"
)

// tinyJPEG is the smallest file imgmeta takes for a JPEG.
var tinyJPEG = []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02, 0xFF, 0xD9}

// testHistory has a day with food, a photo and a workout with a route, and
// an old day that goes into a year file.
func testHistory() *store.History {
	start := time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC)
	return &store.History{
		Days: map[string]*store.Day{
			"2025-01-10": {Date: "2025-01-10", Total: store.Macros{Energy: 1500}},
			"2026-10-20": {Date: "2026-10-20", Total: store.Macros{Energy: 1800}, Steps: 9000,
				Items:  []store.Item{{Name: "Skyr", Daytime: "breakfast", Energy: 66}},
				Photos: []store.Photo{{Daytime: "lunch", File: "abc.jpg"}}},
		},
		Health: map[string]*store.HealthDay{"2026-10-20": {Date: "2026-10-20", Workouts: []store.Workout{
			{ID: "w1", Type: "RUNNING", Start: start, End: start.Add(time.Hour), Route: []store.RoutePoint{{48, 11, 0, 500, 120}}},
		}}},
	}
}

type testEnv struct {
	srv     *httptest.Server
	store   *auth.Store
	data    *published
	release chan struct{}
	calls   chan config
	now     time.Time // the server's clock; codes work once, so it moves on
	secret  string    // patrick's authenticator app
}

// newTestEnv serves a tiny site and testHistory over TLS (the session
// cookie is Secure) with an admin "patrick" (who has an authenticator app),
// an admin "newbie" (who has none yet), a viewer "anna" and "coach", whose
// role sees only nutrition and foods; syncs block until release is closed.
func newTestEnv(t *testing.T, withUsers bool) *testEnv {
	t.Helper()
	dir := t.TempDir()
	siteDir, photos := filepath.Join(dir, "site"), filepath.Join(dir, "photos")
	for path, body := range map[string]string{
		filepath.Join(siteDir, "index.html"):       "<h1>hi</h1>",
		filepath.Join(siteDir, "fonts", "f.woff2"): "font",
		filepath.Join(siteDir, "stats.json"):       `{"days":[]}`, // as -out writes it locally
		filepath.Join(photos, "abc.jpg"):           string(tinyJPEG),
		filepath.Join(photos, "secret.jpg"):        string(tinyJPEG), // on disk, but on no page
	} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &testEnv{release: make(chan struct{}), calls: make(chan config, 10), now: time.Now()}
	accounts := auth.NewStore(d)
	accounts.Now = func() time.Time { return e.now }
	e.store = accounts
	if withUsers {
		ctx := context.Background()
		u, _ := accounts.CreateUser(ctx, "patrick", adminPassword, auth.RoleAdmin)
		accounts.CreateUser(ctx, "newbie", adminPassword+"!", auth.RoleAdmin)
		accounts.CreateUser(ctx, "anna", viewerPassword, auth.RoleViewer)
		accounts.SaveRole(ctx, "Coach", []string{auth.ViewNutrition, auth.ViewFoods})
		accounts.CreateUser(ctx, "coach", coachPassword, "Coach")
		e.secret, _ = auth.NewTOTPSecret()
		accounts.SetTOTP(ctx, u, e.secret, 0)
	}
	data := newPublished(stats.Options{PhotoPath: "photos/"})
	data.set(testHistory(), time.Now())
	e.data = data
	r := newRunner(config{}, time.Hour, data)
	r.sync = func(ctx context.Context, cfg config) (*store.History, error) {
		e.calls <- cfg
		<-e.release
		return nil, nil
	}
	e.srv = httptest.NewTLSServer(newHandler(r, os.DirFS(siteDir), data, photos, accounts))
	t.Cleanup(e.srv.Close)
	t.Cleanup(func() {
		select {
		case <-e.release:
		default:
			close(e.release)
		}
	})
	return e
}

// client is a browser: it keeps cookies and sends what browsers send with
// the page's own fetch() calls.
func (e *testEnv) client() *http.Client {
	// srv.Client() is one shared client: copy it, so every browser has its
	// own cookies.
	c := *e.srv.Client()
	c.Jar, _ = cookiejar.New(nil)
	return &c
}

type reqOpt func(*http.Request)

func bearer(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// sameSite marks a request as made by the site's own page.
var sameSite = header("Sec-Fetch-Site", "same-origin")

func (e *testEnv) do(t *testing.T, c *http.Client, method, path, body string, opts ...reqOpt) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

// code is the next code of patrick's app: the clock moves on a step first,
// since each code works only once.
func (e *testEnv) code() string {
	e.now = e.now.Add(30 * time.Second)
	c, _ := auth.TOTPCode(e.secret, e.now)
	return c
}

type loginAnswer struct {
	Next    string   `json:"next"`
	Ticket  string   `json:"ticket"`
	Methods []string `json:"methods"`
}

// signIn signs in, with the app's code when the account asks for one.
func (e *testEnv) signIn(t *testing.T, name, password string, remember bool) *http.Client {
	t.Helper()
	c := e.client()
	body, _ := json.Marshal(loginRequest{Name: name, Password: password, Remember: remember})
	res, text := e.do(t, c, "POST", "/api/login", string(body), sameSite)
	if res.StatusCode != 200 {
		t.Fatalf("sign-in as %s = %d %s", name, res.StatusCode, text)
	}
	var ans loginAnswer
	json.Unmarshal([]byte(text), &ans)
	if ans.Next == "second_factor" {
		body, _ := json.Marshal(ticketRequest{Ticket: ans.Ticket, Kind: "totp", Code: e.code()})
		if res, text := e.do(t, c, "POST", "/api/login/code", string(body), sameSite); res.StatusCode != 200 {
			t.Fatalf("second factor for %s = %d %s", name, res.StatusCode, text)
		}
	}
	return c
}

func TestEveryRouteNamesItsAccess(t *testing.T) {
	e := newTestEnv(t, true)
	a := &app{}
	for _, rt := range a.routes() {
		if rt.access == 0 || rt.handle == nil {
			t.Errorf("%s %s has no access level or no handler", rt.method, rt.path)
			continue
		}
		if rt.access == open {
			continue
		}
		// Without a session every route that isn't open says so, before
		// doing anything.
		path := strings.NewReplacer("{id}", "1", "{name}", "x").Replace(rt.path)
		var body string
		if rt.method != "GET" {
			body = "{}"
		}
		if res, _ := e.do(t, e.client(), rt.method, path, body, sameSite); res.StatusCode != 401 {
			t.Errorf("%s %s without a session = %d, want 401", rt.method, path, res.StatusCode)
		}
	}
	if len(e.calls) != 0 {
		t.Error("a request without a session started a sync")
	}
}

// statsOf fetches and decodes the stats c gets.
func (e *testEnv) statsOf(t *testing.T, c *http.Client, opts ...reqOpt) stats.Output {
	t.Helper()
	res, body := e.do(t, c, "GET", "/api/stats", "", opts...)
	var out stats.Output
	if err := json.Unmarshal([]byte(body), &out); res.StatusCode != 200 || err != nil {
		t.Fatalf("stats = %d %s", res.StatusCode, body)
	}
	return out
}

func TestServeFiles(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.signIn(t, "anna", viewerPassword, true)
	anon := e.client()
	out := e.statsOf(t, c)
	if len(out.Days) != 1 || len(out.Archive) != 1 || len(out.Days[0].Photos) != 1 || out.Days[0].Workouts[0].RouteSrc == "" {
		t.Fatalf("anna's stats = %+v", out)
	}
	for _, x := range []struct {
		path     string
		anonCode int
		code     int
		cache    string
	}{
		{"/", 200, 200, "no-cache"},
		{"/fonts/f.woff2", 200, 200, "max-age=604800"},
		{"/api/stats", 401, 200, "private, no-cache"},
		{"/" + out.Days[0].Photos[0].Src, 401, 200, "private, max-age=31536000, immutable"},
		{"/" + out.Days[0].Workouts[0].RouteSrc, 401, 200, "immutable"},
		{"/" + out.Archive[0], 401, 200, "immutable"},
		{"/photos/secret.jpg", 401, 404, ""}, // only photos the stats show
		{"/days/0000000000000000.json", 401, 404, ""},
		{"/photos/", 404, 404, ""}, // no directory listing
		{"/fonts/", 404, 404, ""},  // same for the site
		{"/missing.js", 404, 404, ""},
		{"/stats.json", 404, 404, ""}, // no public files any more
		{"/photos/..%2Fapp.db", 401, 404, ""},
	} {
		if res, _ := e.do(t, anon, "GET", x.path, ""); res.StatusCode != x.anonCode {
			t.Errorf("GET %s signed out = %d, want %d", x.path, res.StatusCode, x.anonCode)
		}
		res, _ := e.do(t, c, "GET", x.path, "")
		if res.StatusCode != x.code {
			t.Errorf("GET %s = %d, want %d", x.path, res.StatusCode, x.code)
		}
		if x.cache != "" && !strings.Contains(res.Header.Get("Cache-Control"), x.cache) {
			t.Errorf("GET %s Cache-Control = %q, want %q", x.path, res.Header.Get("Cache-Control"), x.cache)
		}
		if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("GET %s has no CSP header", x.path)
		}
	}
}

func TestSignInAndOut(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.client()
	if res, body := e.do(t, c, "POST", "/api/login", `{"name":"patrick","password":"wrong password!!"}`, sameSite); res.StatusCode != 401 || !strings.Contains(body, "invalid") {
		t.Fatalf("wrong password = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, c, "POST", "/api/login", `{"name":"nobody","password":"wrong password!!"}`, sameSite); res.StatusCode != 401 || !strings.Contains(body, "invalid") {
		t.Fatalf("unknown name = %d %s, want the same answer as a wrong password", res.StatusCode, body)
	}

	res, body := e.do(t, c, "POST", "/api/login", `{"name":"Anna","password":"`+viewerPassword+`","remember":true}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"name":"anna"`) || !strings.Contains(body, `"sync":false`) {
		t.Fatalf("sign-in = %d %s", res.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int(auth.SessionMax.Seconds()) {
		t.Fatalf("session cookie %+v", cookie)
	}
	if res, body := e.do(t, c, "GET", "/api/me", ""); res.StatusCode != 200 || !strings.Contains(body, `"name":"anna"`) {
		t.Errorf("me = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, c, "GET", "/api/sessions", ""); res.StatusCode != 200 || !strings.Contains(body, `"current":true`) {
		t.Errorf("sessions = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, c, "POST", "/api/logout", "{}", sameSite); res.StatusCode != 204 {
		t.Fatalf("logout = %d", res.StatusCode)
	}
	if res, _ := e.do(t, c, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Errorf("me after logout = %d, want 401", res.StatusCode)
	}

	// Without "Remember me" the cookie lives only as long as the browser.
	res, _ = e.do(t, e.client(), "POST", "/api/login", `{"name":"anna","password":"`+viewerPassword+`"}`, sameSite)
	for _, ck := range res.Cookies() {
		if ck.Name == sessionCookie && ck.MaxAge != 0 {
			t.Errorf("cookie without Remember me has MaxAge %d", ck.MaxAge)
		}
	}

	// The audit log has the failures and the sign-ins, for admins only.
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "GET", "/api/audit", ""); res.StatusCode != 200 || !strings.Contains(body, "login_failed") || !strings.Contains(body, `"actor":"nobody"`) {
		t.Errorf("audit = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, e.signIn(t, "anna", viewerPassword, false), "GET", "/api/audit", ""); res.StatusCode != 403 {
		t.Errorf("audit as viewer = %d, want 403", res.StatusCode)
	}
}

func TestCrossSiteRequestsAreRefused(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.signIn(t, "patrick", adminPassword, false)
	login := `{"name":"patrick","password":"` + adminPassword + `"}`
	host := strings.TrimPrefix(e.srv.URL, "https://")
	for name, x := range map[string]struct {
		path, body string
		opts       []reqOpt
	}{
		"sync from another site":        {"/api/sync", "{}", []reqOpt{header("Sec-Fetch-Site", "cross-site")}},
		"sync from a sibling subdomain": {"/api/sync", "{}", []reqOpt{header("Sec-Fetch-Site", "same-site")}},
		"sync as a form post":           {"/api/sync", "", []reqOpt{sameSite, header("Content-Type", "application/x-www-form-urlencoded")}},
		"sync with another Origin":      {"/api/sync", "{}", []reqOpt{header("Origin", "https://evil.example")}},
		"sync with neither header":      {"/api/sync", "{}", nil},
		"logout from another site":      {"/api/logout", "{}", []reqOpt{header("Sec-Fetch-Site", "cross-site")}},
		"login from another site":       {"/api/login", login, []reqOpt{header("Sec-Fetch-Site", "cross-site")}},
		"login as text/plain (no CORS)": {"/api/login", login, []reqOpt{sameSite, header("Content-Type", "text/plain")}},
	} {
		if res, _ := e.do(t, c, "POST", x.path, x.body, x.opts...); res.StatusCode != 403 {
			t.Errorf("%s = %d, want 403", name, res.StatusCode)
		}
	}
	if len(e.calls) != 0 {
		t.Fatal("a cross-site request started a sync")
	}
	// The page's own request: browsers without Sec-Fetch-Site still send Origin.
	if res, _ := e.do(t, c, "POST", "/api/sync", "{}", header("Origin", "https://"+host)); res.StatusCode != 202 {
		t.Errorf("sync with the site's Origin = %d, want 202", res.StatusCode)
	}
}

func TestSignInLockout(t *testing.T) {
	e := newTestEnv(t, true)
	c := e.client()
	for range 5 {
		e.do(t, c, "POST", "/api/login", `{"name":"patrick","password":"guess guess guess"}`, sameSite)
	}
	res, body := e.do(t, c, "POST", "/api/login", `{"name":"patrick","password":"`+adminPassword+`"}`, sameSite)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" || !strings.Contains(body, "retry_after") {
		t.Errorf("after 5 wrong passwords = %d %s, want 429 even for the right one", res.StatusCode, body)
	}
	// Other accounts are not locked by guesses at this one (up to the
	// address's own limit).
	if res, _ := e.do(t, c, "POST", "/api/login", `{"name":"anna","password":"`+viewerPassword+`"}`, sameSite); res.StatusCode != 200 {
		t.Errorf("another account = %d, want 200", res.StatusCode)
	}
	// Many names from one address lock the address.
	for i := range 20 {
		e.do(t, c, "POST", "/api/login", `{"name":"user`+string(rune('a'+i))+`","password":"guess guess guess"}`, sameSite)
	}
	if res, _ := e.do(t, c, "POST", "/api/login", `{"name":"anna","password":"`+viewerPassword+`"}`, sameSite); res.StatusCode != 429 {
		t.Errorf("after 20 failures from one address = %d, want 429", res.StatusCode)
	}
}

func TestSyncNeedsAnAdminOrASyncToken(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	patrick, _ := e.store.UserByName(ctx, "patrick")
	syncTok, _ := e.store.CreateAPIToken(ctx, patrick, "cron", []string{"sync"}, time.Time{})
	readTok, _ := e.store.CreateAPIToken(ctx, patrick, "export", []string{"read"}, time.Time{})

	viewer := e.signIn(t, "anna", viewerPassword, false)
	if res, _ := e.do(t, viewer, "POST", "/api/sync", "{}", sameSite); res.StatusCode != 403 {
		t.Errorf("sync as viewer = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, viewer, "GET", "/api/stats", ""); res.StatusCode != 200 {
		t.Errorf("stats as viewer = %d", res.StatusCode)
	}
	plain := e.srv.Client()
	if res, _ := e.do(t, plain, "POST", "/api/sync", "", bearer(readTok)); res.StatusCode != 403 {
		t.Errorf("sync with a read token = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "GET", "/api/stats", "", bearer(readTok)); res.StatusCode != 200 {
		t.Errorf("stats with a read token = %d", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "GET", "/api/stats", "", bearer(syncTok)); res.StatusCode != 403 {
		t.Errorf("stats with a sync-only token = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "GET", "/api/sessions", "", bearer(readTok)); res.StatusCode != 403 {
		t.Errorf("sessions with a token = %d, want 403", res.StatusCode)
	}
	if res, _ := e.do(t, plain, "POST", "/api/sync", "", bearer("eum_wrong")); res.StatusCode != 401 {
		t.Errorf("sync with a wrong token = %d, want 401", res.StatusCode)
	}

	// A token needs no Origin or JSON: it isn't sent by browsers on their own.
	res, body := e.do(t, plain, "POST", "/api/sync?all=1", "", bearer(syncTok))
	if res.StatusCode != 202 {
		t.Fatalf("sync with a sync token = %d %s", res.StatusCode, body)
	}
	if cfg := <-e.calls; !cfg.refreshAll {
		t.Error("?all=1 did not request a full refresh")
	}
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, _ := e.do(t, admin, "POST", "/api/sync", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("second sync while running = %d, want 409", res.StatusCode)
	}
	res, body = e.do(t, admin, "GET", "/api/status", "")
	var st syncStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil || res.StatusCode != 200 || !st.Running || !st.All {
		t.Errorf("status = %d %+v %v; want a running full refresh", res.StatusCode, st, err)
	}
}

func TestSessionsBelongToTheirUser(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, true)
	patrick := e.signIn(t, "patrick", adminPassword, true)
	var list []sessionInfo
	_, body := e.do(t, anna, "GET", "/api/sessions", "")
	json.Unmarshal([]byte(body), &list)
	if len(list) != 1 {
		t.Fatalf("anna's sessions: %s", body)
	}
	if res, _ := e.do(t, patrick, "DELETE", "/api/sessions/"+itoa(list[0].ID), "", sameSite, header("Content-Type", "application/json")); res.StatusCode != 404 {
		t.Errorf("deleting another user's session = %d, want 404", res.StatusCode)
	}
	if res, _ := e.do(t, anna, "GET", "/api/me", ""); res.StatusCode != 200 {
		t.Error("anna was signed out by someone else")
	}
	other := e.signIn(t, "anna", viewerPassword, false)
	if res, _ := e.do(t, other, "POST", "/api/logout-all", "{}", sameSite); res.StatusCode != 204 {
		t.Fatalf("logout-all = %d", res.StatusCode)
	}
	if res, _ := e.do(t, anna, "GET", "/api/me", ""); res.StatusCode != 401 {
		t.Error("a session survived sign out everywhere")
	}
	if res, _ := e.do(t, patrick, "GET", "/api/me", ""); res.StatusCode != 200 {
		t.Error("sign out everywhere ended another user's session")
	}
}

func TestNoDataBeforeTheFirstAccount(t *testing.T) {
	e := newTestEnv(t, false)
	c := e.client()
	if res, _ := e.do(t, c, "GET", "/", ""); res.StatusCode != 200 {
		t.Errorf("page = %d", res.StatusCode)
	}
	for _, p := range []string{"/api/stats", "/api/me"} {
		if res, body := e.do(t, c, "GET", p, ""); res.StatusCode != 503 || !strings.Contains(body, "setup") {
			t.Errorf("GET %s = %d %s, want 503 setup", p, res.StatusCode, body)
		}
	}
	if res, _ := e.do(t, c, "POST", "/api/login", `{"name":"x","password":"y"}`, sameSite); res.StatusCode != 503 {
		t.Errorf("login = %d, want 503", res.StatusCode)
	}
}

func TestClientIPTrustsOnlyTheLocalProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "198.51.100.7")
	req.RemoteAddr = "127.0.0.1:5000"
	if ip := clientIP(req); ip != "198.51.100.7" {
		t.Errorf("behind nginx: %s", ip)
	}
	// Docker hands nginx's requests over from the bridge's gateway.
	req.RemoteAddr = "172.17.0.1:41234"
	if ip := clientIP(req); ip != "198.51.100.7" {
		t.Errorf("behind nginx in Docker: %s", ip)
	}
	req.RemoteAddr = "203.0.113.9:5000"
	if ip := clientIP(req); ip != "203.0.113.9" {
		t.Errorf("a direct client chose its own address: %s", ip)
	}
}

func TestNextAt(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	for now, want := range map[time.Time]time.Time{
		time.Date(2026, 10, 27, 1, 0, 0, 0, loc):  time.Date(2026, 10, 27, 3, 20, 0, 0, loc),
		time.Date(2026, 10, 27, 3, 20, 0, 0, loc): time.Date(2026, 10, 28, 3, 20, 0, 0, loc),
		time.Date(2026, 12, 31, 9, 0, 0, 0, loc):  time.Date(2027, 1, 1, 3, 20, 0, 0, loc),
	} {
		if got := nextAt(now, 3, 20); !got.Equal(want) {
			t.Errorf("nextAt(%v) = %v, want %v", now, got, want)
		}
	}
}

func TestHealthy(t *testing.T) {
	r := newRunner(config{}, 30*time.Minute, newPublished(stats.Options{}))
	if !r.healthy(r.started.Add(time.Hour)) {
		t.Error("unhealthy while still catching up after start")
	}
	if r.healthy(r.started.Add(2 * time.Hour)) {
		t.Error("healthy although no sync succeeded for 4 intervals")
	}
	r.lastSuccess = r.started.Add(100 * time.Minute)
	if !r.healthy(r.started.Add(2 * time.Hour)) {
		t.Error("unhealthy right after a successful sync")
	}
}

func TestServeCompiledInPages(t *testing.T) {
	dir := t.TempDir()
	d, _ := db.Open(context.Background(), filepath.Join(dir, "app.db"))
	defer d.Close()
	data := newPublished(stats.Options{})
	r := newRunner(config{}, time.Hour, data)
	srv := httptest.NewServer(newHandler(r, site.Files, data, dir, auth.NewStore(d)))
	t.Cleanup(srv.Close)
	get := func(p string, opts ...reqOpt) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		for _, o := range opts {
			o(req)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	for _, p := range []string{"/", "/js/main.js"} {
		if res := get(p); res.StatusCode != 200 {
			t.Errorf("GET %s = %d", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/embed.go", "/embed_test.go"} {
		if res := get(p); res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", p, res.StatusCode)
		}
	}
	// Compiled-in files have no modification time: an ETag lets browsers
	// revalidate them.
	tag := get("/style.css").Header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag for a compiled-in file")
	}
	if res := get("/style.css", header("If-None-Match", tag)); res.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", res.StatusCode)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Each role gets only the data its scopes allow, also from files named in
// someone else's stats, and a new role applies to the next request.
func TestDataFollowsTheRole(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	coach := e.signIn(t, "coach", coachPassword, false)
	full := e.statsOf(t, anna)
	if full.Scopes == nil || full.ReviewStatus != nil {
		t.Errorf("viewer: scopes %v, credit status %+v", full.Scopes, full.ReviewStatus)
	}

	out := e.statsOf(t, coach)
	d := out.Days[len(out.Days)-1]
	if d.Energy != 1800 || len(d.Foods) != 1 || d.Steps != 0 || d.Photos != nil || d.Workouts != nil {
		t.Errorf("coach's day = %+v", d)
	}
	if strings.Join(out.Scopes, " ") != "view.nutrition view.foods" {
		t.Errorf("coach's scopes = %v", out.Scopes)
	}
	for path, want := range map[string]int{
		"/" + full.Days[0].Photos[0].Src:        403,
		"/" + full.Days[0].Workouts[0].RouteSrc: 403,
		"/" + out.Archive[0]:                    200,
	} {
		if res, _ := e.do(t, coach, "GET", path, ""); res.StatusCode != want {
			t.Errorf("coach GET %s = %d, want %d", path, res.StatusCode, want)
		}
	}
	// anna's year file holds her view of the old days: not the coach's.
	if full.Archive[0] != out.Archive[0] {
		if res, _ := e.do(t, coach, "GET", "/"+full.Archive[0], ""); res.StatusCode != 404 {
			t.Errorf("coach got anna's year file: %d", res.StatusCode)
		}
	}
	var who me
	_, body := e.do(t, coach, "GET", "/api/me", "")
	json.Unmarshal([]byte(body), &who)
	if who.Role != "Coach" || who.Sync || strings.Join(who.Scopes, " ") != "view.nutrition view.foods" {
		t.Errorf("coach's me = %+v", who)
	}

	ctx := context.Background()
	u, _ := e.store.UserByName(ctx, "coach")
	e.store.SetRole(ctx, u, auth.RoleViewer)
	if out := e.statsOf(t, coach); out.Days[len(out.Days)-1].Workouts == nil {
		t.Error("the new role didn't apply to the next request")
	}

	// Revalidation: the same stats are not sent again.
	res, _ := e.do(t, anna, "GET", "/api/stats", "")
	if res, _ := e.do(t, anna, "GET", "/api/stats", "", header("If-None-Match", res.Header.Get("ETag"))); res.StatusCode != 304 {
		t.Errorf("revalidating the stats = %d, want 304", res.StatusCode)
	}
}

// Admins get the Anthropic credit warning; the data comes from the last
// sync and is replaced by the next.
func TestStatsAfterASync(t *testing.T) {
	e := newTestEnv(t, true)
	h := testHistory()
	h.ReviewState = &store.ReviewState{Billing: true}
	admin := e.signIn(t, "patrick", adminPassword, false)
	anna := e.signIn(t, "anna", viewerPassword, false)
	if out := e.statsOf(t, admin); out.ReviewStatus != nil {
		t.Errorf("credit status before the sync: %+v", out.ReviewStatus)
	}
	e.data.set(h, time.Now())
	if out := e.statsOf(t, admin); out.ReviewStatus == nil || !out.ReviewStatus.Billing {
		t.Errorf("admin's credit status = %+v", out.ReviewStatus)
	}
	if out := e.statsOf(t, anna); out.ReviewStatus != nil {
		t.Errorf("viewer's credit status = %+v", out.ReviewStatus)
	}
}

// A role that may sync needs a second factor for it, as admins do, but
// nothing beyond its scopes: no audit log.
func TestASyncRoleNeedsASecondFactor(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	e.store.SaveRole(ctx, "Syncer", []string{auth.ViewNutrition, auth.ScopeSync})
	u, _ := e.store.CreateUser(ctx, "syncer", coachPassword+"!", "Syncer")
	c := e.signIn(t, "syncer", coachPassword+"!", false)
	if res, body := e.do(t, c, "POST", "/api/sync", "{}", sameSite); res.StatusCode != 403 || !strings.Contains(body, "second_factor_required") {
		t.Errorf("sync without a second factor = %d %s", res.StatusCode, body)
	}
	var who me
	_, body := e.do(t, c, "GET", "/api/me", "")
	json.Unmarshal([]byte(body), &who)
	if !who.NeedsSecondFactor || who.Sync {
		t.Errorf("me = %+v", who)
	}
	secret, _ := auth.NewTOTPSecret()
	e.store.SetTOTP(ctx, u, secret, 0)
	if res, _ := e.do(t, c, "POST", "/api/sync", "{}", sameSite); res.StatusCode != 202 {
		t.Errorf("sync with a second factor = %d, want 202", res.StatusCode)
	}
	if res, _ := e.do(t, c, "GET", "/api/audit", ""); res.StatusCode != 403 {
		t.Errorf("audit log for a sync role = %d, want 403", res.StatusCode)
	}
}
