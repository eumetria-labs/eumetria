package off

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// fake serves the real responses saved in testdata (2026-10-20).
func fake(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "Eumetria") {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/product/4067796000306":
			data, _ := os.ReadFile("testdata/crunchy.json")
			w.Write(data)
		case "/api/v2/product/1":
			w.Write([]byte(`{"status":1,"product":{"product_name":"Water","brands":"A, B","nutriscore_grade":"not-applicable","nova_group":"1"}}`))
		case "/api/v2/product/2":
			w.Write([]byte(`{"status":1,"product":{"product_name":"New","nutriscore_grade":"unknown"}}`))
		default:
			data, _ := os.ReadFile("testdata/unknown.json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(data)
		}
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.Base = srv.URL
	return c
}

func TestProduct(t *testing.T) {
	c := fake(t)
	p, err := c.Product(context.Background(), "4067796000306")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Müsli Crunchy, Schoko" || p.Brand != "dmBio" || p.NutriScore != "c" || p.NOVA != 4 || !reflect.DeepEqual(p.Additives, []string{"E322"}) {
		t.Errorf("product = %+v", p)
	}
	// Grades arrive as strings or numbers; unrated stays empty.
	p, _ = c.Product(context.Background(), "1")
	if p.NutriScore != "not-applicable" || p.NOVA != 1 || p.Brand != "A" {
		t.Errorf("water = %+v", p)
	}
	p, _ = c.Product(context.Background(), "2")
	if p.NutriScore != "" || p.NOVA != 0 {
		t.Errorf("unrated = %+v", p)
	}
	if _, err := c.Product(context.Background(), "4000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown barcode: err = %v, want ErrNotFound", err)
	}
}
