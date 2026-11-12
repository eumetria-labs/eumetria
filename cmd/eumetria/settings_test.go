package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"eumetria/internal/google"
	"eumetria/internal/settings"
	"eumetria/internal/store"
)

const testSettingsKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

type settingsView struct {
	Settings   map[string]settings.Item `json:"settings"`
	CanEncrypt bool                     `json:"can_encrypt"`
	Zone       struct {
		Name string `json:"name"`
		Env  string `json:"env"`
	} `json:"zone"`
	Google googleStatus `json:"google"`
	Status syncStatus   `json:"status"`
}

func (e *testEnv) settingsOf(t *testing.T, c *http.Client) (settingsView, string) {
	t.Helper()
	res, body := e.do(t, c, "GET", "/api/admin/settings", "")
	var v settingsView
	if err := json.Unmarshal([]byte(body), &v); res.StatusCode != 200 || err != nil {
		t.Fatalf("settings = %d %s", res.StatusCode, body)
	}
	return v, body
}

func TestSettingsAreForAdmins(t *testing.T) {
	e := newTestEnv(t, true)
	anna := e.signIn(t, "anna", viewerPassword, false)
	newbie := e.signIn(t, "newbie", adminPassword+"!", false)
	for _, c := range []*http.Client{anna, newbie} {
		for _, r := range [][2]string{{"GET", "/api/admin/settings"}, {"PUT", "/api/admin/settings"}, {"POST", "/api/google/connect"}, {"GET", "/api/google/callback"}, {"DELETE", "/api/google"}, {"POST", "/api/admin/reviews/rewrite"}, {"POST", "/api/admin/settings/test/yazio"}} {
			if res, _ := e.do(t, c, r[0], r[1], "{}", sameSite); res.StatusCode != 403 {
				t.Errorf("%s %s = %d, want 403", r[0], r[1], res.StatusCode)
			}
		}
	}
}

func TestChangingSettings(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)

	res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"title":"Mine","timezone":"Asia/Tokyo","language":"de","yazio_password":"hunter2 secret"}}`, sameSite)
	if res.StatusCode != 204 {
		t.Fatalf("saving = %d %s", res.StatusCode, body)
	}

	// The stats have it at once, without a sync or a restart.
	out := e.statsOf(t, admin)
	if out.Title != "Mine" || out.Timezone != "Asia/Tokyo" || out.Language != "de" {
		t.Errorf("stats = %q %q %q", out.Title, out.Timezone, out.Language)
	}

	// A secret never comes back, and neither the log nor the page has it.
	v, raw := e.settingsOf(t, admin)
	if strings.Contains(raw, "hunter2") || strings.Contains(raw, "v1:") {
		t.Errorf("the settings give the secret away: %s", raw)
	}
	if v.Settings["yazio_password"].State != "set" || !v.CanEncrypt || v.Zone.Name != "Asia/Tokyo" {
		t.Errorf("view = %+v", v)
	}
	log, _ := e.store.AuditLog(context.Background(), 20)
	var entry string
	for _, l := range log {
		if l.Action == "settings_changed" {
			entry = l.Detail
		}
	}
	if entry != "language timezone title yazio_password" {
		t.Errorf("audit log = %q", entry)
	}

	// Refused changes say which field, and nothing is saved.
	res, body = e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"title":"Other","sync_interval":"1m"}}`, sameSite)
	if res.StatusCode != 400 || !strings.Contains(body, `"key":"sync_interval"`) {
		t.Errorf("a short interval = %d %s", res.StatusCode, body)
	}
	if out := e.statsOf(t, admin); out.Title != "Mine" {
		t.Errorf("title after a refused change = %q", out.Title)
	}
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"env_imported":"0"}}`, sameSite); res.StatusCode != 400 {
		t.Errorf("an internal row = %d, want 400", res.StatusCode)
	}

	// "Use TZ again".
	t.Setenv("TZ", "Europe/Berlin")
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"reset":["timezone"]}`, sameSite); res.StatusCode != 204 {
		t.Errorf("reset = %d", res.StatusCode)
	}
	if out := e.statsOf(t, admin); out.Timezone != "Europe/Berlin" {
		t.Errorf("zone after the reset = %q", out.Timezone)
	}

	// An old sign-in has to sign in again.
	e.now = e.now.Add(time.Hour)
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"title":"Late"}}`, sameSite); res.StatusCode != 403 || !strings.Contains(body, "reauth") {
		t.Errorf("after an hour = %d %s", res.StatusCode, body)
	}
}

func TestSecretsNeedTheKey(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	var err error
	if e.runner.settings, err = settings.New(e.runner.settings.DB, ""); err != nil {
		t.Fatal(err)
	}
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"anthropic_key":"sk"}}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_key") {
		t.Errorf("without a key = %d %s", res.StatusCode, body)
	}
	if v, _ := e.settingsOf(t, admin); v.CanEncrypt {
		t.Error("the page is told it can encrypt")
	}
	// Everything else still works.
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"title":"Plain"}}`, sameSite); res.StatusCode != 204 {
		t.Errorf("a plain value = %d", res.StatusCode)
	}
}

func TestANewIntervalMovesTheSchedule(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	e.runner.cfg.interval = time.Hour
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"sync_interval":"10m"}}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("saving = %d", res.StatusCode)
	}
	if got := e.runner.period(); got != 10*time.Minute {
		t.Errorf("interval = %s", got)
	}
	select {
	case <-e.runner.retimed:
	default:
		t.Error("the scheduler wasn't told")
	}
	if out := e.statsOf(t, admin); out.SyncIntervalMinutes != 10 {
		t.Errorf("the footer's interval = %v", out.SyncIntervalMinutes)
	}
}

func TestASyncReadsTheSettings(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"timezone":"Asia/Tokyo","refresh_days":"9","yazio_password":"pw from the page"}}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("saving = %d %s", res.StatusCode, body)
	}
	e.runner.start(context.Background(), false)
	cfg := <-e.calls
	close(e.release)
	e.runner.wait()
	if cfg.refreshDays != 9 || cfg.values.YazioPassword != "pw from the page" || cfg.zone().String() != "Asia/Tokyo" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestAFlagWinsOverTheSettings(t *testing.T) {
	v := settings.Values{RefreshDays: 9, Since: "2026-01-01", Interval: time.Hour}
	cfg := config{refreshDays: 1, since: "2025-01-01", interval: time.Minute, flags: map[string]bool{"refresh-days": true, "since": true, "interval": true}}.with(v)
	if cfg.refreshDays != 1 || cfg.since != "2025-01-01" || cfg.interval != time.Minute {
		t.Errorf("config = %+v", cfg)
	}
	cfg = config{interval: time.Minute}.with(v)
	if cfg.refreshDays != 9 || cfg.since != "2026-01-01" || cfg.interval != time.Hour {
		t.Errorf("without flags = %+v", cfg)
	}
}

func TestToday(t *testing.T) {
	loc, _ := time.LoadLocation("Pacific/Kiritimati") // UTC+14: mostly another day than UTC
	cfg := config{values: settings.Values{Zone: loc}}
	if cfg.now().Location().String() != "Pacific/Kiritimati" {
		t.Errorf("now is in %s", cfg.now().Location())
	}
	if (config{}).now().Location() != time.Local {
		t.Error("without settings it isn't the local time")
	}
}

func TestTestingTheYazioLogin(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	old := testYazio
	defer func() { testYazio = old }()
	var got string
	testYazio = func(_ context.Context, v settings.Values) error {
		got = v.YazioPassword
		return errors.New("yazio: wrong password")
	}
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"yazio_password":"saved one"}}`, sameSite)
	res, body := e.do(t, admin, "POST", "/api/admin/settings/test/yazio", "{}", sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "wrong password") || got != "saved one" {
		t.Errorf("test = %d %s, password %q", res.StatusCode, body, got)
	}
	testYazio = func(context.Context, settings.Values) error { return nil }
	if _, body := e.do(t, admin, "POST", "/api/admin/settings/test/yazio", "{}", sameSite); !strings.Contains(body, `"ok":true`) {
		t.Errorf("test = %s", body)
	}
}

func fakeGoogle(t *testing.T, fail error) *[]google.Config {
	t.Helper()
	calls := &[]google.Config{}
	oldX, oldR := exchangeGoogle, revokeGoogle
	t.Cleanup(func() { exchangeGoogle, revokeGoogle = oldX, oldR })
	exchangeGoogle = func(_ context.Context, cfg google.Config, code string) (*google.Token, error) {
		*calls = append(*calls, cfg)
		if fail != nil {
			return nil, fail
		}
		return &google.Token{RefreshToken: "refresh-" + code, Scope: strings.Join(google.Scopes, " "), IssuedAt: time.Now()}, nil
	}
	revokeGoogle = func(context.Context, *google.Token) error { return errors.New("offline") }
	return calls
}

// connect starts Connect Google and returns the consent address's query.
func (e *testEnv) connect(t *testing.T, c *http.Client) url.Values {
	t.Helper()
	res, body := e.do(t, c, "POST", "/api/google/connect", "{}", sameSite)
	var ans struct {
		URL string `json:"url"`
	}
	json.Unmarshal([]byte(body), &ans)
	u, err := url.Parse(ans.URL)
	if res.StatusCode != 200 || err != nil {
		t.Fatalf("connect = %d %s", res.StatusCode, body)
	}
	return u.Query()
}

// back follows Google's redirect to the callback and returns where the page
// is sent.
func (e *testEnv) back(t *testing.T, c *http.Client, query string) string {
	t.Helper()
	noFollow := *c
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, _ := e.do(t, &noFollow, "GET", "/api/google/callback?"+query, "")
	if res.StatusCode != 303 {
		t.Fatalf("callback = %d", res.StatusCode)
	}
	return res.Header.Get("Location")
}

func TestConnectGoogle(t *testing.T) {
	e := newTestEnv(t, true)
	calls := fakeGoogle(t, nil)
	admin := e.signIn(t, "patrick", adminPassword, false)

	// It needs the client first.
	if res, body := e.do(t, admin, "POST", "/api/google/connect", "{}", sameSite); res.StatusCode != 409 || !strings.Contains(body, "no_client") {
		t.Errorf("without a client = %d %s", res.StatusCode, body)
	}
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"google_client_id":"id","google_client_secret":"client secret"}}`, sameSite)

	q := e.connect(t, admin)
	if q.Get("client_id") != "id" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Fatalf("consent = %v", q)
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/api/google/callback") || !strings.HasPrefix(q.Get("redirect_uri"), "https://") {
		t.Errorf("redirect = %s", q.Get("redirect_uri"))
	}
	if strings.Contains(q.Encode(), "client+secret") {
		t.Error("the consent address holds the client secret")
	}

	if to := e.back(t, admin, "state="+url.QueryEscape(q.Get("state"))+"&code=abc"); to != "/#settings=google&google=connected" {
		t.Errorf("back to %s", to)
	}
	if len(*calls) != 1 || (*calls)[0].Verifier == "" || (*calls)[0].RedirectURL != q.Get("redirect_uri") || (*calls)[0].ClientSecret != "client secret" {
		t.Fatalf("exchange = %+v", *calls)
	}
	tok, err := google.LoadToken(e.runner.cfg.googleToken)
	if err != nil || tok.RefreshToken != "refresh-abc" {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	if v, raw := e.settingsOf(t, admin); !v.Google.Connected || len(v.Google.Missing) != 0 || strings.Contains(raw, "refresh-abc") {
		t.Errorf("status = %s", raw)
	}

	// A state works once.
	if to := e.back(t, admin, "state="+url.QueryEscape(q.Get("state"))+"&code=again"); !strings.HasSuffix(to, "google=expired") {
		t.Errorf("the same state again: %s", to)
	}
	if len(*calls) != 1 {
		t.Error("a used state reached Google")
	}

	// Disconnecting works even when Google can't be reached.
	if res, _ := e.do(t, admin, "DELETE", "/api/google", "", jsonBody...); res.StatusCode != 204 {
		t.Errorf("disconnect = %d", res.StatusCode)
	}
	if _, err := os.Stat(e.runner.cfg.googleToken); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the token is still there: %v", err)
	}
	if res, _ := e.do(t, admin, "DELETE", "/api/google", "", jsonBody...); res.StatusCode != 404 {
		t.Errorf("disconnecting again = %d", res.StatusCode)
	}
}

func TestConnectGoogleRefuses(t *testing.T) {
	e := newTestEnv(t, true)
	calls := fakeGoogle(t, nil)
	admin := e.signIn(t, "patrick", adminPassword, false)
	e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"google_client_id":"id","google_client_secret":"s"}}`, sameSite)

	// A code with a made-up state: an attacker's own Google account.
	if to := e.back(t, admin, "state=made-up&code=evil"); !strings.HasSuffix(to, "google=expired") {
		t.Errorf("a made-up state: %s", to)
	}
	// Another admin's browser can't finish what this one started.
	ctx := context.Background()
	u := mustUser(t, e, "newbie")
	e.store.SetRole(ctx, u, "admin")
	e.store.SetTOTP(ctx, u, e.secret, 0)
	other := e.signIn(t, "newbie", adminPassword+"!", false)
	q := e.connect(t, admin)
	if to := e.back(t, other, "state="+url.QueryEscape(q.Get("state"))+"&code=x"); !strings.HasSuffix(to, "google=expired") {
		t.Errorf("another admin: %s", to)
	}
	// Saying no at Google.
	q = e.connect(t, admin)
	if to := e.back(t, admin, "state="+url.QueryEscape(q.Get("state"))+"&error=access_denied"); !strings.HasSuffix(to, "google=denied") {
		t.Errorf("denied: %s", to)
	}
	if len(*calls) != 0 {
		t.Errorf("exchanges = %d", len(*calls))
	}
	if _, err := os.Stat(e.runner.cfg.googleToken); err == nil {
		t.Error("a token was saved")
	}

	// A failed exchange says so.
	fakeGoogle(t, errors.New("google: 400"))
	q = e.connect(t, admin)
	if to := e.back(t, admin, "state="+url.QueryEscape(q.Get("state"))+"&code=x"); !strings.HasSuffix(to, "google=failed") {
		t.Errorf("failed: %s", to)
	}
}

func TestRewritingTheReviews(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	gate := make(chan struct{})
	e.runner.rewrite = func(_ context.Context, _ config, progress func(done, total int)) (*store.History, error) {
		progress(1, 3)
		<-gate
		return testHistory(), errors.New("week of 2026-10-19: credit used up")
	}
	if res, _ := e.do(t, admin, "POST", "/api/admin/reviews/rewrite", "{}", sameSite); res.StatusCode != 202 {
		t.Fatalf("rewrite = %d", res.StatusCode)
	}
	// One job at a time: nothing else writes the history meanwhile.
	if e.runner.start(context.Background(), false) {
		t.Error("a sync started during the rewrite")
	}
	if res, _ := e.do(t, admin, "POST", "/api/admin/reviews/rewrite", "{}", sameSite); res.StatusCode != 409 {
		t.Errorf("a second rewrite = %d, want 409", res.StatusCode)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if st := e.runner.status(); st.JobDone == 1 {
			break
		}
	}
	if st := e.runner.status(); st.Job != "rewrite" || st.JobDone != 1 || st.JobTotal != 3 {
		t.Errorf("status = %+v", st)
	}
	close(gate)
	e.runner.wait()
	if st := e.runner.status(); st.Running || st.Job != "" || !strings.Contains(st.JobError, "credit") {
		t.Errorf("status after = %+v", st)
	}
}

// Pentest re-test N4 and I3 through the page: a mail server needs the
// public URL, and a new one clears the password, says so, and the audit
// log has it.
func TestSettingsSaveMailServer(t *testing.T) {
	e := newTestEnv(t, true)
	admin := e.signIn(t, "patrick", adminPassword, false)
	// A mail server needs the public URL, for the links in mails (I3).
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"smtp_host":"mail.example.org","smtp_password":"relay secret"}}`, sameSite); res.StatusCode != 409 || !strings.Contains(body, "needs_public_url") {
		t.Fatalf("mail server without a public URL = %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"smtp_host":"mail.example.org","smtp_password":"relay secret","public_url":"https://eumetria.example.org"}}`, sameSite); res.StatusCode != 204 {
		t.Fatalf("set = %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, admin, "PUT", "/api/admin/settings", `{"reset":["public_url"]}`, sameSite); res.StatusCode != 409 {
		t.Errorf("removing the public URL with a mail server = %d, want 409", res.StatusCode)
	}
	res, body := e.do(t, admin, "PUT", "/api/admin/settings", `{"values":{"smtp_host":"evil.example"}}`, sameSite)
	if res.StatusCode != 200 || !strings.Contains(body, `"cleared":["smtp_password"]`) {
		t.Fatalf("new host = %d %s", res.StatusCode, body)
	}
	if v, _ := e.runner.settings.Values(context.Background()); v.SMTP.Password != "" {
		t.Error("the password stayed for the new host")
	}
	if log, _ := e.store.AuditLog(context.Background(), 20); !strings.Contains(jsonOf(t, log), "setting_secret_cleared") {
		t.Error("no setting_secret_cleared in the audit log")
	}
}
