// Package yazio is a small client for the unofficial Yazio API.
//
// Endpoints come from https://github.com/funmelon64/Yazio-Exporter (MIT),
// https://github.com/juriadams/yazio (no license; only endpoint names are used)
// and https://github.com/dornsebastian/yazio-api-specification (MIT), which is
// also where the OAuth client credentials documented in the README come from.
// The API is undocumented and may change without notice. This client is
// unofficial and not affiliated with or endorsed by YAZIO.
package yazio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://yzapi.yazio.com/v22"

// ErrNotFound is returned when the API answers 404 (e.g. a deleted product).
var ErrNotFound = errors.New("yazio: not found")

type Client struct {
	BaseURL string
	HTTP    *http.Client
	// ClientID and ClientSecret identify the app to Yazio's OAuth server.
	// They are the Yazio app's own credentials, kept out of the code and
	// passed in as YAZIO_CLIENT_ID / YAZIO_CLIENT_SECRET.
	ClientID     string
	ClientSecret string
	// AllowImage reports whether a photo may be downloaded from u. The
	// default allows only HTTPS on Yazio's image CDN.
	AllowImage func(u *url.URL) bool
	token      string
}

func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Login exchanges email and password for an access token.
func (c *Client) Login(ctx context.Context, email, password string) error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("login: OAuth client ID and secret are not set")
	}
	body, _ := json.Marshal(map[string]string{
		"client_id":     c.ClientID,
		"client_secret": c.ClientSecret,
		"username":      email,
		"password":      password,
		"grant_type":    "password",
	})
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(ctx, http.MethodPost, "/oauth/token", body, &tok); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if tok.AccessToken == "" {
		return errors.New("login: response contained no access token")
	}
	c.token = tok.AccessToken
	return nil
}

// Nutrients are keyed like "energy.energy", "nutrient.protein", "nutrient.carb", "nutrient.fat".
type Nutrients map[string]float64

type MealSummary struct {
	EnergyGoal float64   `json:"energy_goal"`
	Nutrients  Nutrients `json:"nutrients"`
}

type DailySummary struct {
	ActivityEnergy float64 `json:"activity_energy"`
	// ConsumeActivityEnergy means the energy goal already includes ActivityEnergy.
	ConsumeActivityEnergy *bool                  `json:"consume_activity_energy"`
	Steps                 float64                `json:"steps"`
	WaterIntake           float64                `json:"water_intake"`
	Goals                 map[string]float64     `json:"goals"`
	Meals                 map[string]MealSummary `json:"meals"`
	Units                 map[string]string      `json:"units"`
}

func (c *Client) DailySummary(ctx context.Context, day time.Time) (*DailySummary, error) {
	var s DailySummary
	err := c.get(ctx, "/user/widgets/daily-summary", url.Values{"date": {dateStr(day)}}, &s)
	return &s, err
}

type ConsumedProduct struct {
	ID              string  `json:"id"`
	Date            string  `json:"date"`
	Daytime         string  `json:"daytime"`
	ProductID       string  `json:"product_id"`
	Amount          float64 `json:"amount"`
	Serving         string  `json:"serving"`
	ServingQuantity float64 `json:"serving_quantity"`
}

type ConsumedRecipe struct {
	ID           string  `json:"id"`
	Date         string  `json:"date"`
	Daytime      string  `json:"daytime"`
	RecipeID     string  `json:"recipe_id"`
	PortionCount float64 `json:"portion_count"`
}

type ConsumedSimple struct {
	ID          string    `json:"id"`
	Date        string    `json:"date"`
	Daytime     string    `json:"daytime"`
	Name        string    `json:"name"`
	Nutrients   Nutrients `json:"nutrients"`
	AIGenerated bool      `json:"is_ai_generated"`
}

type Consumed struct {
	Products       []ConsumedProduct `json:"products"`
	RecipePortions []ConsumedRecipe  `json:"recipe_portions"`
	SimpleProducts []ConsumedSimple  `json:"simple_products"`
}

func (c *Client) Consumed(ctx context.Context, day time.Time) (*Consumed, error) {
	var res Consumed
	err := c.get(ctx, "/user/consumed-items", url.Values{"date": {dateStr(day)}}, &res)
	return &res, err
}

type WeightEntry struct {
	ID    string   `json:"id"`
	Date  string   `json:"date"`
	Value *float64 `json:"value"`
}

// LastWeight returns the most recent weight entry on or before day (nil if none).
func (c *Client) LastWeight(ctx context.Context, day time.Time) (*WeightEntry, error) {
	var w *WeightEntry
	err := c.get(ctx, "/user/bodyvalues/weight/last", url.Values{"date": {dateStr(day)}}, &w)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return w, err
}

// Product nutrients are per base unit (g or ml).
type Product struct {
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	BaseUnit  string    `json:"base_unit"`
	Nutrients Nutrients `json:"nutrients"`
	// EANs are the product's barcodes; loose foods (an apple) have none.
	EANs []string `json:"eans"`
}

func (c *Client) Product(ctx context.Context, id string) (*Product, error) {
	var p Product
	err := c.get(ctx, "/products/"+url.PathEscape(id), nil, &p)
	return &p, err
}

// Recipe is a Yazio or user-made recipe. Servings are the ingredients of
// the whole recipe (PortionCount portions); they carry no nutrients, those
// come from the product. Image is a full-size photo on Yazio's CDN.
type Recipe struct {
	Name         string          `json:"name"`
	PortionCount float64         `json:"portion_count"`
	Nutrients    Nutrients       `json:"nutrients"`
	Image        string          `json:"image"`
	Servings     []RecipeServing `json:"servings"`
}

type RecipeServing struct {
	Name            string  `json:"name"`
	Producer        string  `json:"producer"`
	ProductID       string  `json:"product_id"`
	Amount          float64 `json:"amount"` // in BaseUnit
	BaseUnit        string  `json:"base_unit"`
	Serving         string  `json:"serving"`
	ServingQuantity float64 `json:"serving_quantity"`
}

func (c *Client) Recipe(ctx context.Context, id string) (*Recipe, error) {
	var r Recipe
	err := c.get(ctx, "/recipes/"+url.PathEscape(id), nil, &r)
	return &r, err
}

type Exercise struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Energy   float64 `json:"energy"`
	Duration float64 `json:"duration"`
	Distance float64 `json:"distance"`
	// ExternalID is the ID at the source the activity was imported from;
	// for Source "fitbit" it is the Google Health workout ID.
	ExternalID string `json:"external_id"`
	Source     string `json:"source"`
}

type ExercisesResponse struct {
	Training       []Exercise `json:"training"`
	CustomTraining []Exercise `json:"custom_training"`
}

func (c *Client) Exercises(ctx context.Context, day time.Time) (*ExercisesResponse, error) {
	var res ExercisesResponse
	err := c.get(ctx, "/user/exercises", url.Values{"date": {dateStr(day)}}, &res)
	if errors.Is(err, ErrNotFound) {
		return &ExercisesResponse{}, nil
	}
	return &res, err
}

// MealImagesRaw returns the undecoded meal photo list for a day. The endpoint
// only exists from API v22 on and its response shape is undocumented, so
// callers parse it leniently (see MealImageURLs).
func (c *Client) MealImagesRaw(ctx context.Context, day time.Time) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.doURL(ctx, http.MethodGet, c.root()+"/v22/user/meal-images/"+dateStr(day), nil, &raw)
	return raw, err
}

// MealImage is a photo found in a meal-images response. Daytime is empty when
// the response did not say which meal it belongs to.
type MealImage struct {
	Daytime string
	URL     string
}

var daytimes = map[string]bool{"breakfast": true, "lunch": true, "dinner": true, "snack": true}

// MealImageURLs walks an arbitrary JSON document and returns meal image URLs
func MealImageURLs(raw json.RawMessage) []MealImage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}

	var out []MealImage
	seen := make(map[string]bool)

	var walk func(v any, daytime string)
	walk = func(v any, daytime string) {
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				walk(e, daytime)
			}

		case string:
			if isImageURL(t) && !seen[t] {
				seen[t] = true
				out = append(out, MealImage{
					Daytime: daytime,
					URL:     t,
				})
			}

		case map[string]any:
			if dt, ok := t["daytime"].(string); ok && daytimes[dt] {
				daytime = dt
			}

			// Handle meal-type keys such as "breakfast", "lunch",
			// "dinner", and "snacks" directly.
			for k, e := range t {
				childDaytime := daytime
				if daytimes[k] {
					childDaytime = k
				}
				walk(e, childDaytime)
			}
		}
	}

	walk(v, "")
	return out
}

func isImageURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// YazioImage allows HTTPS URLs on Yazio's image CDN (images.yazio-cdn.com).
func YazioImage(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (host == "yazio-cdn.com" || strings.HasSuffix(host, ".yazio-cdn.com"))
}

// Download fetches a meal photo. Only URLs (and redirects) that AllowImage
// accepts are followed, so a tampered API response cannot make the sync fetch
// arbitrary addresses. No credentials are ever sent with it.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, string, error) {
	allow := c.AllowImage
	if allow == nil {
		allow = YazioImage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	if !allow(req.URL) {
		return nil, "", fmt.Errorf("refusing to download %s: not an HTTPS URL on Yazio's image CDN", req.URL.Redacted())
	}
	hc := *c.HTTP
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !allow(r.URL) {
			return fmt.Errorf("refusing redirect to %s", r.URL.Redacted())
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return data, resp.Header.Get("Content-Type"), err
}

// root is the scheme and host of BaseURL, for endpoints on another API version.
func (c *Client) root() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return c.BaseURL
	}
	return u.Scheme + "://" + u.Host
}

// Get fetches a date-scoped endpoint into out (used for debugging raw responses).
func (c *Client) Get(ctx context.Context, path, date string, out any) error {
	return c.get(ctx, path, url.Values{"date": {date}}, out)
}

// GetPath is a GET without query parameters, for -debug-day.
func (c *Client) GetPath(ctx context.Context, path string, out any) error {
	return c.get(ctx, path, nil, out)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	return c.doURL(ctx, method, c.BaseURL+path, body, out)
}

// doURL performs a request, retrying on 429 and 5xx with exponential backoff.
func (c *Client) doURL(ctx context.Context, method, path string, body []byte, out any) error {
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "YAZIO/26.30.1 (com.yazio.ios.YAZIO; build:2607271240; iOS 27.0.0) Ktor")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if attempt < 3 {
				if !sleep(ctx, backoff) {
					return ctx.Err()
				}
				backoff *= 2
				continue
			}
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retryable && attempt < 4 {
			wait := backoff
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				wait = time.Duration(s) * time.Second
			}
			if !sleep(ctx, wait) {
				return ctx.Err()
			}
			backoff *= 2
			continue
		}
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode == http.StatusNoContent:
			return nil
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, truncate(data, 200))
		case readErr != nil:
			return fmt.Errorf("%s %s: reading body: %w", method, path, readErr)
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decoding: %w", method, path, err)
		}
		return nil
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func dateStr(t time.Time) string { return t.Format(time.DateOnly) }

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
