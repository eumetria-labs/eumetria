// Package withings reads body measurements (weight, body composition) from
// a Withings scale through Withings' public Health Data API.
//
// Pre-staged: written from the public documentation and tested with
// fixtures in that documented shape, before a scale was there to try it.
// Points the readable docs left open are marked "(verify)" and are checked
// at the live test (PLAN.md, phase 10), which also replaces the fixtures
// with recorded responses.
//
// Confirmed from developer.withings.com (read 2026-11-19):
//   - Authorize at account.withings.com/oauth2_user/authorize2 with scope
//     user.metrics; the code is valid for 30 seconds.
//   - Requests to /v2/oauth2 are signed: a nonce from /v2/signature
//     (action=getnonce, signed over action,client_id,timestamp), then
//     HMAC-SHA256 hex with the client secret over action,client_id,nonce.
//     The client secret itself is never sent.
//   - Access tokens last 3 hours; refresh tokens a year, and rotate: each
//     refresh returns a new one that must replace the old.
//   - Data: POST /measure, action=getmeas, Authorization: Bearer; with
//     lastupdate for the changes since the last fetch.
package withings

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The endpoints; tests point them elsewhere.
var (
	AuthBase = "https://account.withings.com"
	APIBase  = "https://wbsapi.withings.net"
)

// Config is the app registered at developer.withings.com.
type Config struct {
	ClientID, ClientSecret, RedirectURL string
}

// AuthURL is where the browser goes to grant access.
func (c Config) AuthURL(state string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {c.ClientID}, "scope": {"user.metrics"}, "redirect_uri": {c.RedirectURL}, "state": {state}}
	return AuthBase + "/oauth2_user/authorize2?" + q.Encode()
}

// sign is the hex HMAC-SHA256 of the values joined by commas, keyed with
// the client secret.
func (c Config) sign(values ...string) string {
	m := hmac.New(sha256.New, []byte(c.ClientSecret))
	m.Write([]byte(strings.Join(values, ",")))
	return hex.EncodeToString(m.Sum(nil))
}

// Token is what the code exchange and each refresh give.
type Token struct {
	UserID       string    `json:"userid"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Scope        string    `json:"scope,omitempty"`
}

// ErrAuth means the grant is gone (revoked, expired, a refresh token
// replaced): only connecting again helps.
var ErrAuth = errors.New("withings: access was revoked or expired; connect Withings again")

// APIError is a response whose JSON status isn't 0. Withings answers HTTP
// 200 with the error in the body.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("withings: status %d: %s", e.Status, e.Message) }

// authStatus are the statuses that mean the token is no good. 401 is
// documented; the others are from the error list (verify).
func authStatus(s int) bool { return s == 401 || s == 100 || s == 101 || s == 102 }

type envelope struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	Error  string          `json:"error"`
}

// post sends a form and returns the body of a status-0 answer.
func post(ctx context.Context, hc *http.Client, path string, form url.Values, bearer string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuth
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("withings: %s %s", path, res.Status)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("withings: %s: %w", path, err)
	}
	if env.Status != 0 {
		if authStatus(env.Status) {
			return nil, fmt.Errorf("%w (status %d)", ErrAuth, env.Status)
		}
		return nil, &APIError{env.Status, env.Error}
	}
	return env.Body, nil
}

// nonce asks for a one-time value to sign a token request with.
func (c Config) nonce(ctx context.Context, hc *http.Client, now time.Time) (string, error) {
	ts := strconv.FormatInt(now.Unix(), 10)
	body, err := post(ctx, hc, "/v2/signature", url.Values{
		"action": {"getnonce"}, "client_id": {c.ClientID}, "timestamp": {ts}, "signature": {c.sign("getnonce", c.ClientID, ts)},
	}, "")
	if err != nil {
		return "", err
	}
	var out struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Nonce == "" {
		return "", fmt.Errorf("withings: no nonce in %s", body)
	}
	return out.Nonce, nil
}

// requestToken signs and sends a requesttoken call with the grant's fields.
func (c Config) requestToken(ctx context.Context, hc *http.Client, now time.Time, grant url.Values) (Token, error) {
	n, err := c.nonce(ctx, hc, now)
	if err != nil {
		return Token{}, err
	}
	grant.Set("action", "requesttoken")
	grant.Set("client_id", c.ClientID)
	grant.Set("nonce", n)
	grant.Set("signature", c.sign("requesttoken", c.ClientID, n))
	body, err := post(ctx, hc, "/v2/oauth2", grant, "")
	if err != nil {
		return Token{}, err
	}
	var out struct {
		UserID       json.Number `json:"userid"`
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		ExpiresIn    int64       `json:"expires_in"`
		Scope        string      `json:"scope"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return Token{}, fmt.Errorf("withings: token: %w", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		return Token{}, errors.New("withings: the token answer has no tokens")
	}
	return Token{UserID: out.UserID.String(), AccessToken: out.AccessToken, RefreshToken: out.RefreshToken,
		Expiry: now.Add(time.Duration(out.ExpiresIn) * time.Second), Scope: out.Scope}, nil
}

// Exchange trades the code from the callback for a token, at once: the
// code is valid for 30 seconds.
func (c Config) Exchange(ctx context.Context, hc *http.Client, code string, now time.Time) (Token, error) {
	return c.requestToken(ctx, hc, now, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.RedirectURL}})
}

// Refresh gets a new access token, and a new refresh token that replaces
// the old one (parameters (verify)).
func (c Config) Refresh(ctx context.Context, hc *http.Client, refresh string, now time.Time) (Token, error) {
	return c.requestToken(ctx, hc, now, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

// Client reads measurements with a token it refreshes as needed. save is
// called with every new token before it is used, so a rotated refresh
// token is never lost.
type Client struct {
	cfg  Config
	hc   *http.Client
	tok  Token
	save func(Token) error
	now  func() time.Time
}

func New(cfg Config, tok Token, save func(Token) error) *Client {
	return &Client{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}, tok: tok, save: save, now: time.Now}
}

func (c *Client) access(ctx context.Context) (string, error) {
	if c.tok.AccessToken != "" && c.now().Before(c.tok.Expiry.Add(-5*time.Minute)) {
		return c.tok.AccessToken, nil
	}
	tok, err := c.cfg.Refresh(ctx, c.hc, c.tok.RefreshToken, c.now())
	if err != nil {
		return "", err
	}
	if tok.UserID == "" {
		tok.UserID = c.tok.UserID
	}
	if err := c.save(tok); err != nil {
		return "", fmt.Errorf("withings: saving the new token: %w", err)
	}
	c.tok = tok
	return tok.AccessToken, nil
}

// Measure is one value of a group: Value × 10^Unit in the type's unit.
type Measure struct {
	Value int64 `json:"value"`
	Type  int   `json:"type"`
	Unit  int   `json:"unit"`
}

// Real is the measure's value (verify: value × 10^unit).
func (m Measure) Real() float64 {
	return float64(m.Value) * math.Pow10(m.Unit)
}

// Group is one weigh-in: the measures taken together (verify the fields).
type Group struct {
	GrpID    int64     `json:"grpid"`
	Attrib   int       `json:"attrib"`
	Date     int64     `json:"date"`
	Created  int64     `json:"created"`
	Modified int64     `json:"modified"`
	Category int       `json:"category"` // 1 a real measurement, 2 a goal (verify)
	DeviceID string    `json:"deviceid"`
	Timezone string    `json:"timezone"`
	Measures []Measure `json:"measures"`
}

// Measurements returns the groups created or changed since lastupdate (a
// Unix time; 0 for everything), following the pages, and the time to pass
// next: the latest change seen, never earlier than lastupdate.
func (c *Client) Measurements(ctx context.Context, lastupdate int64) ([]Group, int64, error) {
	var out []Group
	next := lastupdate
	offset := 0
	for page := 0; page < 200; page++ {
		tok, err := c.access(ctx)
		if err != nil {
			return out, lastupdate, err
		}
		form := url.Values{"action": {"getmeas"}, "category": {"1"}, "lastupdate": {strconv.FormatInt(lastupdate, 10)}}
		if offset > 0 {
			form.Set("offset", strconv.Itoa(offset))
		}
		body, err := post(ctx, c.hc, "/measure", form, tok)
		if err != nil {
			return out, lastupdate, err
		}
		var res struct {
			UpdateTime int64   `json:"updatetime"`
			Timezone   string  `json:"timezone"`
			Groups     []Group `json:"measuregrps"`
			More       int     `json:"more"`
			Offset     int     `json:"offset"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return out, lastupdate, fmt.Errorf("withings: getmeas: %w", err)
		}
		for _, g := range res.Groups {
			if g.Timezone == "" {
				g.Timezone = res.Timezone
			}
			out = append(out, g)
			next = max(next, g.Modified)
		}
		next = max(next, res.UpdateTime)
		if res.More == 0 || res.Offset <= offset {
			return out, next, nil
		}
		offset = res.Offset
	}
	return out, next, errors.New("withings: getmeas keeps paging")
}

// Type is a measurement type: its key in the history and unit. Codes
// besides 1, 5, 6, 8, 76, 77, 88 and 91 are (verify); a code not listed
// is kept as "type_<code>", never dropped.
type Type struct {
	Key, Unit string
}

var Types = map[int]Type{
	1:   {"weight", "kg"},
	5:   {"fat_free_mass", "kg"},
	6:   {"fat_ratio", "%"},
	8:   {"fat_mass", "kg"},
	11:  {"heart_rate", "bpm"}, // standing heart rate (verify)
	76:  {"muscle_mass", "kg"},
	77:  {"hydration", "kg"},
	88:  {"bone_mass", "kg"},
	91:  {"pulse_wave_velocity", "m/s"},
	155: {"vascular_age", "years"},     // (verify)
	168: {"extracellular_water", "kg"}, // (verify)
	169: {"intracellular_water", "kg"}, // (verify)
	170: {"visceral_fat", ""},          // an index (verify)
	226: {"basal_metabolic_rate", "kcal"},
}

// KeyOf is the history's name for a type code.
func KeyOf(code int) string {
	if t, ok := Types[code]; ok {
		return t.Key
	}
	return "type_" + strconv.Itoa(code)
}
