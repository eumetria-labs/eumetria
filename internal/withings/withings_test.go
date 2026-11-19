package withings

// The responses here are written from the documented shapes (PLAN.md,
// phase 10), not recorded: the live test replaces them.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

var cfg = Config{ClientID: "cid", ClientSecret: "secret", RedirectURL: "https://eumetria.example/api/withings/callback"}

func hm(s string) string {
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil))
}

// fake is a Withings that checks the signatures and counts the calls.
type fake struct {
	t        *testing.T
	refresh  string // the refresh token it accepts
	issued   int
	measures int
	revoked  bool
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	v := r.PostForm.Get
	switch r.URL.Path {
	case "/v2/signature":
		if v("action") != "getnonce" || v("signature") != hm("getnonce,cid,"+v("timestamp")) {
			f.t.Errorf("getnonce = %v", r.PostForm)
		}
		fmt.Fprint(w, `{"status":0,"body":{"nonce":"n1"}}`)
	case "/v2/oauth2":
		if v("signature") != hm("requesttoken,cid,n1") || v("nonce") != "n1" || v("client_secret") != "" {
			f.t.Errorf("requesttoken = %v", r.PostForm)
		}
		switch v("grant_type") {
		case "authorization_code":
			if v("code") != "c0de" || v("redirect_uri") != cfg.RedirectURL {
				f.t.Errorf("exchange = %v", r.PostForm)
			}
		case "refresh_token":
			if f.revoked || v("refresh_token") != f.refresh {
				fmt.Fprint(w, `{"status":401,"error":"invalid refresh token"}`)
				return
			}
		}
		f.issued++
		f.refresh = fmt.Sprintf("r%d", f.issued)
		fmt.Fprintf(w, `{"status":0,"body":{"userid":12345,"access_token":"a%d","refresh_token":"%s","expires_in":10800,"scope":"user.metrics","token_type":"Bearer"}}`, f.issued, f.refresh)
	case "/measure":
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer a%d", f.issued) || v("action") != "getmeas" {
			fmt.Fprint(w, `{"status":401,"error":"invalid token"}`)
			return
		}
		f.measures++
		page := "page1"
		if v("offset") == "2" {
			page = "page2"
		}
		b, _ := os.ReadFile("testdata/getmeas-" + page + ".json")
		w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

func serve(t *testing.T, f *fake) {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = old })
}

func TestAuthURL(t *testing.T) {
	u := cfg.AuthURL("st")
	for _, want := range []string{"https://account.withings.com/oauth2_user/authorize2?", "scope=user.metrics", "state=st", "response_type=code", "client_id=cid", "redirect_uri=https%3A%2F%2Feumetria.example"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s lacks %s", u, want)
		}
	}
}

func TestExchangeRefreshAndMeasurements(t *testing.T) {
	f := &fake{t: t}
	serve(t, f)
	ctx := context.Background()
	now := time.Unix(1763535600, 0)
	tok, err := cfg.Exchange(ctx, http.DefaultClient, "c0de", now)
	if err != nil || tok.UserID != "12345" || tok.RefreshToken != "r1" || !tok.Expiry.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("exchange = %+v, %v", tok, err)
	}
	// An expired access token is refreshed, and the new token saved before use.
	tok.Expiry = now.Add(-time.Minute)
	var saved []Token
	c := New(cfg, tok, func(t Token) error { saved = append(saved, t); return nil })
	c.now = func() time.Time { return now }
	groups, next, err := c.Measurements(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].RefreshToken != "r2" || saved[0].UserID != "12345" {
		t.Errorf("saved = %+v", saved)
	}
	if len(groups) != 3 || f.measures != 2 || next != 1763535600 {
		t.Errorf("groups = %d over %d pages, next %d", len(groups), f.measures, next)
	}
	g := groups[0]
	if g.Timezone != "Europe/Berlin" || g.Measures[0].Real() != 78.42 || KeyOf(g.Measures[1].Type) != "fat_ratio" || g.Measures[1].Real() != 18.2 {
		t.Errorf("first group = %+v", g)
	}
	if KeyOf(999) != "type_999" {
		t.Error("an unknown type")
	}
	// The old refresh token is gone: a revoked grant asks to connect again.
	f.revoked = true
	c.tok.Expiry = now.Add(-time.Minute)
	if _, _, err := c.Measurements(ctx, next); !errors.Is(err, ErrAuth) {
		t.Errorf("revoked = %v", err)
	}
}
