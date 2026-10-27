// Package off looks up products on Open Food Facts (openfoodfacts.org), the
// free, crowd-sourced food database (data under the Open Database License):
// Nutri-Score, NOVA processing group and additives by barcode.
package off

import (
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

	"eumetria/internal/store"
)

// ErrNotFound means Open Food Facts doesn't know the barcode.
var ErrNotFound = errors.New("off: product not found")

// Client reads single products. Open Food Facts asks every app to identify
// itself in the User-Agent and allows 15 product reads a minute per IP
// address; more can get the address banned.
type Client struct {
	Base      string // default https://world.openfoodfacts.org
	UserAgent string
	HTTP      *http.Client
}

func New() *Client {
	return &Client{
		Base:      "https://world.openfoodfacts.org",
		UserAgent: "Eumetria/1.0 (self-hosted health dashboard; https://github.com/eumetria-labs/eumetria)",
		HTTP:      &http.Client{Timeout: 20 * time.Second},
	}
}

type response struct {
	Status  int `json:"status"`
	Product struct {
		Name       string          `json:"product_name"`
		Brands     string          `json:"brands"`
		NutriScore string          `json:"nutriscore_grade"`
		NOVA       json.RawMessage `json:"nova_group"`
		Additives  []string        `json:"additives_tags"`
	} `json:"product"`
}

// Product returns what Open Food Facts knows about the barcode.
func (c *Client) Product(ctx context.Context, ean string) (*store.OFFProduct, error) {
	u := fmt.Sprintf("%s/api/v2/product/%s?fields=product_name,brands,nutriscore_grade,nova_group,additives_tags",
		strings.TrimRight(c.Base, "/"), url.PathEscape(ean))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	// An unknown barcode is a 404 with status 0.
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("off: %s: HTTP %d", ean, res.StatusCode)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("off: %s: %w", ean, err)
	}
	if r.Status != 1 {
		return nil, ErrNotFound
	}
	p := &store.OFFProduct{
		Name:       strings.TrimSpace(r.Product.Name),
		Brand:      firstBrand(r.Product.Brands),
		NutriScore: nutriScore(r.Product.NutriScore),
		NOVA:       nova(r.Product.NOVA),
		Additives:  additives(r.Product.Additives),
	}
	return p, nil
}

func firstBrand(s string) string {
	b, _, _ := strings.Cut(s, ",")
	return strings.TrimSpace(b)
}

// nutriScore keeps the grades a to e and "not-applicable"; "unknown" and
// anything else means the product isn't rated.
func nutriScore(s string) string {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case "a", "b", "c", "d", "e", "not-applicable":
		return s
	}
	return ""
}

// nova reads the group, which arrives as a number or a string.
func nova(raw json.RawMessage) int {
	s := strings.Trim(string(raw), `"`)
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 4 {
		return 0
	}
	return n
}

// additives turns tags like "en:e322i" into E numbers ("E322i").
func additives(tags []string) []string {
	var out []string
	for _, t := range tags {
		_, code, ok := strings.Cut(t, ":")
		if !ok || !strings.HasPrefix(code, "e") {
			continue
		}
		out = append(out, "E"+code[1:])
	}
	return out
}
