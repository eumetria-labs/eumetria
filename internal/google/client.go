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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
var tokenURL = "https://oauth2.googleapis.com/token"

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

// ErrNotAuthorized means there is no usable refresh token: run -google-auth.
var ErrNotAuthorized = errors.New("google: not authorized, run eumetria -google-auth")

// Config identifies the OAuth client registered in the Google Cloud console.
// RedirectURL must be one of the client's authorized redirect URIs.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// AuthURL is the consent page. access_type=offline with prompt=consent makes
// Google issue a refresh token every time, also when consent was given before.
func (c Config) AuthURL(state string) string {
	q := url.Values{
		"client_id":              {c.ClientID},
		"redirect_uri":           {c.RedirectURL},
		"response_type":          {"code"},
		"scope":                  {strings.Join(Scopes, " ")},
		"access_type":            {"offline"},
		"prompt":                 {"consent"},
		"include_granted_scopes": {"true"},
		"state":                  {state},
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

// Exchange trades the code from the consent redirect for tokens.
func (c Config) Exchange(ctx context.Context, hc *http.Client, code string) (*Token, error) {
	tr, err := c.token(ctx, hc, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.RedirectURL},
	})
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

// accessToken returns a valid access token, refreshing it when it is about to expire.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.AccessToken != "" && time.Until(c.tok.Expiry) > time.Minute {
		return c.tok.AccessToken, nil
	}
	tr, err := c.Config.token(ctx, c.HTTP, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.tok.RefreshToken},
	})
	if err != nil {
		return "", err
	}
	c.tok.AccessToken = tr.AccessToken
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
