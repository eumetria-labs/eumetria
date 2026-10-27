package stats

import (
	"strings"

	"eumetria/internal/store"
)

// Product is what Open Food Facts knows about a food the diary links to by
// barcode. Grades are as Open Food Facts gives them (crowd-sourced); empty
// means not rated there.
type Product struct {
	Name       string   `json:"n,omitempty"`
	Brand      string   `json:"b,omitempty"`
	NutriScore string   `json:"ns,omitempty"`
	NOVA       int      `json:"nova,omitempty"`
	Additives  []string `json:"add,omitempty"`
}

// products collects the Open Food Facts data behind the published foods.
type products struct {
	h      *store.History
	byName map[string]string // product name → barcode; "" when ambiguous
	out    map[string]Product
}

func newProducts(h *store.History) *products {
	p := &products{h: h, byName: map[string]string{}, out: map[string]Product{}}
	for key, f := range h.Foods {
		if !strings.HasPrefix(key, "product:") || f.EAN == "" {
			continue
		}
		if prev, dup := p.byName[f.Name]; dup && prev != f.EAN {
			p.byName[f.Name] = ""
		} else {
			p.byName[f.Name] = f.EAN
		}
	}
	return p
}

// link returns the barcode for a food or ingredient and adds its Open Food
// Facts data to the output, or "" when there is none. Entries stored before
// barcodes were recorded are matched by product name.
func (p *products) link(ean, name string) string {
	if ean == "" {
		ean = p.byName[name]
	}
	if ean == "" {
		return ""
	}
	if _, ok := p.out[ean]; ok {
		return ean
	}
	o := p.h.OFF[ean]
	if o == nil || o.NotFound {
		return ""
	}
	p.out[ean] = Product{Name: o.Name, Brand: o.Brand, NutriScore: o.NutriScore, NOVA: o.NOVA, Additives: o.Additives}
	return ean
}
