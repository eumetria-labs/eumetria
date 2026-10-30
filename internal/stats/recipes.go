package stats

import (
	"strings"

	"eumetria/internal/store"
)

// Recipe is a recipe eaten in the published range. Ingredients are for the
// whole recipe (Portions portions); the page scales them to what was eaten.
type Recipe struct {
	Name        string       `json:"name"`
	Portions    float64      `json:"portions"`
	Image       string       `json:"img,omitempty"`
	Ingredients []Ingredient `json:"items,omitempty"`
}

type Ingredient struct {
	Name       string  `json:"n"`
	Producer   string  `json:"p,omitempty"`
	Amount     float64 `json:"a"`
	Unit       string  `json:"u,omitempty"`
	Serving    string  `json:"s,omitempty"`
	ServingQty float64 `json:"sq,omitempty"`
	Energy     float64 `json:"e"`
	Protein    float64 `json:"pr"`
	Carb       float64 `json:"c"`
	Fat        float64 `json:"f"`
	Barcode    string  `json:"x,omitempty"`
}

// recipes collects the recipes behind the published food entries.
type recipes struct {
	h      *store.History
	opt    Options
	prods  *products
	byName map[string]string // name → recipe ID; "" when two recipes share it
	out    map[string]Recipe
}

func newRecipes(h *store.History, opt Options, prods *products) *recipes {
	r := &recipes{h: h, opt: opt, prods: prods, byName: map[string]string{}, out: map[string]Recipe{}}
	for key, f := range h.Foods {
		id, ok := strings.CutPrefix(key, "recipe:")
		if !ok || f.Missing || strings.Contains(id, "@") { // only current versions
			continue
		}
		if _, dup := r.byName[f.Name]; dup {
			r.byName[f.Name] = ""
		} else {
			r.byName[f.Name] = id
		}
	}
	return r
}

// link returns the key of the recipe version behind it and adds that
// version to the output, or "" when it is no recipe or one without details.
// The key is the version's hash, or the recipe ID for entries synced before
// versions were kept (those show the current version; entries synced before
// items kept their recipe ID are matched by name).
func (r *recipes) link(it store.Item) string {
	id, key, fkey := it.RecipeID, it.RecipeVersion, ""
	switch {
	case id != "" && key != "":
		fkey = store.RecipeKey(id, key)
	case id == "" && it.Unit == "portion":
		id = r.byName[it.Name]
		fallthrough
	default:
		key, fkey = id, "recipe:"+id
	}
	if id == "" {
		return ""
	}
	if _, ok := r.out[key]; ok {
		return key
	}
	f := r.h.Foods[fkey]
	if f == nil || f.Missing {
		return ""
	}
	rec := Recipe{Name: f.Name, Portions: f.Portions}
	if rec.Portions <= 0 {
		rec.Portions = 1
	}
	if r.opt.Photos {
		switch {
		case strings.HasPrefix(f.Image, "data:"): // demo data
			rec.Image = f.Image
		case f.ImageFile != "":
			rec.Image = r.opt.PhotoPath + f.ImageFile
		}
	}
	for _, in := range f.Ingredients {
		rec.Ingredients = append(rec.Ingredients, Ingredient{
			Name: in.Name, Producer: in.Producer, Amount: round(in.Amount, 1), Unit: in.Unit,
			Serving: servingLabel(in.Serving), ServingQty: round(in.ServingQty, 2),
			Energy: round(in.Energy, 1), Protein: round(in.Protein, 1), Carb: round(in.Carb, 1), Fat: round(in.Fat, 1),
			Barcode: r.prods.link(in.EAN, in.Name),
		})
	}
	if rec.Image == "" && len(rec.Ingredients) == 0 {
		return ""
	}
	r.out[key] = rec
	return key
}
