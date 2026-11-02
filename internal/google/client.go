// Package google is a small client for the Google Health API (v4), the
// successor of the Fitbit Web API: workouts, heart rate and sleep recorded by
// a Fitbit or Pixel Watch.
//
// Reference: https://developers.google.com/health/reference/rest
//
// Authorization is a one-off OAuth consent in the browser (AuthURL, then
// Exchange). The refresh token is kept in a private token file and each run
// trades it for a fresh access token.
package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"eumetria/internal/store"
)

const (
	DefaultBaseURL = "https://health.googleapis.com/v4"
	authURL        = "https://accounts.google.com/o/oauth2/v2/auth"
	scopePrefix    = "https://www.googleapis.com/auth/googlehealth."
)

// tokenURL is a variable so tests can point it at a fake server.
var (
	tokenURL  = "https://oauth2.googleapis.com/token"
	revokeURL = "https://oauth2.googleapis.com/revoke"
)

// Scopes are read-only: workouts and heart rate zones (activity_and_fitness),
// resting heart rate and HRV (health_metrics_and_measurements), sleep, and
// the GPS track of a workout (location, needed for the TCX export).
var Scopes = []string{
	scopePrefix + "activity_and_fitness.readonly",
	scopePrefix + "health_metrics_and_measurements.readonly",
	scopePrefix + "sleep.readonly",
	scopePrefix + "location.readonly",
	scopePrefix + "settings.readonly",
}

// ErrNotAuthorized means there is no usable refresh token: connect Google in
// the settings.
var ErrNotAuthorized = errors.New("google: not connected, use Connect Google in the settings")

// Config identifies the OAuth client registered in the Google Cloud console.
// RedirectURL must be one of the client's authorized redirect URIs.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Verifier turns on PKCE: the consent carries its hash, the exchange the
	// verifier itself.
	Verifier string
	// Extra are scopes beyond the Health ones, e.g. DriveScope.
	Extra []string
}

// AuthURL is the consent page. access_type=offline with prompt=consent makes
// Google issue a refresh token every time, also when consent was given before.
func (c Config) AuthURL(state string) string {
	q := url.Values{
		"client_id":              {c.ClientID},
		"redirect_uri":           {c.RedirectURL},
		"response_type":          {"code"},
		"scope":                  {strings.Join(append(slices.Clone(Scopes), c.Extra...), " ")},
		"access_type":            {"offline"},
		"prompt":                 {"consent"},
		"include_granted_scopes": {"true"},
		"state":                  {state},
	}
	if c.Verifier != "" {
		sum := sha256.Sum256([]byte(c.Verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	return authURL + "?" + q.Encode()
}

// Token is what the token file holds. Only RefreshToken has to survive;
// the access token is cached to save a round trip within its hour.
type Token struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry,omitzero"`
	Scope        string    `json:"scope,omitempty"`
	// AccessScope is what AccessToken was narrowed to, see narrowed.
	AccessScope string `json:"access_scope,omitempty"`
	// IssuedAt is when the refresh token was granted. Refresh tokens of an
	// OAuth app in "Testing" status expire after 7 days.
	IssuedAt time.Time `json:"issued_at"`
}

func LoadToken(path string) (*Token, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotAuthorized
	}
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t.RefreshToken == "" {
		return nil, ErrNotAuthorized
	}
	return &t, nil
}

// SaveToken writes the token file readable by its owner only.
func SaveToken(path string, t *Token) error {
	if err := store.WriteJSON(path, t, true); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// Revoke ends the grant at Google, so the app loses access there too.
func Revoke(ctx context.Context, hc *http.Client, t *Token) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(url.Values{"token": {t.RefreshToken}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("google: revoking: %s", res.Status)
	}
	return nil
}

// Exchange trades the code from the consent redirect for tokens.
func (c Config) Exchange(ctx context.Context, hc *http.Client, code string) (*Token, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.RedirectURL},
	}
	if c.Verifier != "" {
		form.Set("code_verifier", c.Verifier)
	}
	tr, err := c.token(ctx, hc, form)
	if err != nil {
		return nil, err
	}
	if tr.RefreshToken == "" {
		return nil, errors.New("google: no refresh token in the response; revoke the app's access at myaccount.google.com/permissions and authorize again")
	}
	now := time.Now()
	return &Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       now.Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scope:        tr.Scope,
		IssuedAt:     now,
	}, nil
}

func (c Config) token(ctx context.Context, hc *http.Client, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google token: %w", err)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return nil, fmt.Errorf("google token: HTTP %d: %w", resp.StatusCode, err)
	}
	if tr.Error == "invalid_grant" {
		// Revoked, expired (Testing apps: 7 days) or unused for six months.
		return nil, fmt.Errorf("%w (refresh token rejected: %s)", ErrNotAuthorized, tr.Description)
	}
	if resp.StatusCode != http.StatusOK || tr.Error != "" {
		return nil, fmt.Errorf("google token: HTTP %d: %s %s", resp.StatusCode, tr.Error, tr.Description)
	}
	return &tr, nil
}

// Client calls the Health API on behalf of the user whose token it holds.
// Refreshed access tokens are written back to TokenPath.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	Config    Config
	TokenPath string

	drive bool // access tokens for Drive, not the Health API

	mu  sync.Mutex
	tok *Token
}

// New returns a client for the refresh token in tokenPath (written by
// Exchange). Refreshed access tokens are written back to it.
func New(cfg Config, tokenPath string) (*Client, error) {
	tok, err := LoadToken(tokenPath)
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL:   DefaultBaseURL,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		Config:    cfg,
		TokenPath: tokenPath,
		tok:       tok,
	}, nil
}

// narrowed is the scope to ask for when refreshing a grant of these scopes, or
// "" for all of them. The Health API refuses any access token that also carries
// Drive (DISALLOWED_OAUTH_SCOPES), so a grant with Drive gets one access token
// per API.
func narrowed(granted string, drive bool) string {
	scopes := strings.Fields(granted)
	if !slices.Contains(scopes, DriveScope) {
		return ""
	}
	if drive {
		return DriveScope
	}
	return strings.Join(slices.DeleteFunc(scopes, func(s string) bool { return s == DriveScope }), " ")
}

// accessToken returns a valid access token, refreshing it when it is about to
// expire or was made for the other API.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	scope := narrowed(c.tok.Scope, c.drive)
	if c.tok.AccessToken != "" && c.tok.AccessScope == scope && time.Until(c.tok.Expiry) > time.Minute {
		return c.tok.AccessToken, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.tok.RefreshToken},
	}
	if scope != "" {
		form.Set("scope", scope)
	}
	tr, err := c.Config.token(ctx, c.HTTP, form)
	if err != nil {
		return "", err
	}
	c.tok.AccessToken, c.tok.AccessScope = tr.AccessToken, scope
	c.tok.Expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.RefreshToken != "" {
		// Google rarely rotates refresh tokens; the file keeps up.
		c.tok.RefreshToken = tr.RefreshToken
	}
	if err := SaveToken(c.TokenPath, c.tok); err != nil {
		return "", fmt.Errorf("saving google token: %w", err)
	}
	return c.tok.AccessToken, nil
}

// APIError is a non-2xx answer of the Health API.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("google health: HTTP %d: %s", e.Status, e.Body)
}

// Raw sends a request to path (relative to BaseURL, e.g.
// "/users/me/identity") and returns the response body.
func (c *Client) Raw(ctx context.Context, method, path string, q url.Values, body any) ([]byte, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

// List returns every data point of dataType (kebab-case, e.g. "exercise")
// matching filter, following nextPageToken. method is "" for the plain list
// or ":reconcile", which Google recommends for sleep.
func (c *Client) List(ctx context.Context, dataType, method, filter string, pageSize int) ([]json.RawMessage, error) {
	var all []json.RawMessage
	q := url.Values{}
	if filter != "" {
		q.Set("filter", filter)
	}
	if pageSize > 0 {
		q.Set("pageSize", fmt.Sprint(pageSize))
	}
	for {
		data, err := c.Raw(ctx, http.MethodGet, "/users/me/dataTypes/"+dataType+"/dataPoints"+method, q, nil)
		if err != nil {
			return all, err
		}
		var page struct {
			DataPoints    []json.RawMessage `json:"dataPoints"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return all, fmt.Errorf("%s: %w", dataType, err)
		}
		all = append(all, page.DataPoints...)
		if page.NextPageToken == "" {
			return all, nil
		}
		q.Set("pageToken", page.NextPageToken)
	}
}

// ExerciseTCX downloads the TCX export of a workout (GPS track, heart rate
// per track point). name is the data point's resource name
// ("users/…/dataTypes/exercise/dataPoints/…").
func (c *Client) ExerciseTCX(ctx context.Context, name string) ([]byte, error) {
	if !strings.HasPrefix(name, "users/") {
		return nil, fmt.Errorf("google: unexpected data point name %q", name)
	}
	return c.Raw(ctx, http.MethodGet, "/"+name+":exportExerciseTcx", url.Values{"alt": {"media"}}, nil)
}
