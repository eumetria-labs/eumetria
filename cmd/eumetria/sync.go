package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"eumetria/internal/store"
	"eumetria/internal/yazio"
)

type syncer struct {
	client   *yazio.Client
	hist     *store.History
	mu       sync.Mutex // guards hist.Foods
	photoDir string     // where meal photos are downloaded; "" skips photos

	photoErrs atomic.Int32 // meal-image failures, logged once per run

	// Recipes fetched in this run. A recipe can be edited in the app, so
	// each one used is fetched again once per run instead of cached for good.
	freshRecipes map[string]bool
	// prev are the days as stored before this run, to see which recipe
	// version a day's entries had.
	prev map[string]*store.Day
}

// daysToFetch returns every day from since to today that is missing from the
// history or was stored by an older version, plus the most recent refreshDays
// days (which may still change). refreshAll ignores the history and returns the
// whole window, for when a day was edited in the app long after the fact.
func daysToFetch(h *store.History, since, today time.Time, refreshDays int, refreshAll bool) []time.Time {
	var days []time.Time
	refreshFrom := today.AddDate(0, 0, -(refreshDays - 1))
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		old, have := h.Days[d.Format(time.DateOnly)]
		if refreshAll || !have || old.Version < store.DayVersion || !d.Before(refreshFrom) {
			days = append(days, d)
		}
	}
	return days
}

// run fetches days with a bounded worker pool and returns how many failed.
func (s *syncer) run(ctx context.Context, days []time.Time, workers int) (failed int) {
	jobs := make(chan time.Time)
	var wg sync.WaitGroup
	var resMu sync.Mutex
	done := 0
	// Workers read the old days while finished ones are stored.
	s.prev = maps.Clone(s.hist.Days)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for day := range jobs {
				d, err := s.fetchDay(ctx, day)
				resMu.Lock()
				done++
				if err != nil {
					failed++
					log.Printf("day %s: %v (keeping previous data)", day.Format(time.DateOnly), err)
				} else {
					s.hist.Days[d.Date] = d
				}
				if done%50 == 0 {
					log.Printf("progress: %d/%d days", done, len(days))
				}
				resMu.Unlock()
			}
		}()
	}
	for _, d := range days {
		jobs <- d
	}
	close(jobs)
	wg.Wait()
	return failed
}

func (s *syncer) fetchDay(ctx context.Context, day time.Time) (*store.Day, error) {
	date := day.Format(time.DateOnly)

	sum, err := s.client.DailySummary(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("daily summary: %w", err)
	}
	d := &store.Day{
		Version:               store.DayVersion,
		Date:                  date,
		FetchedAt:             time.Now().UTC(),
		Meals:                 map[string]store.Macros{},
		Goals:                 sum.Goals,
		Steps:                 sum.Steps,
		ActivityEnergy:        sum.ActivityEnergy,
		ConsumeActivityEnergy: sum.ConsumeActivityEnergy,
		Water:                 sum.WaterIntake,
	}
	for name, m := range sum.Meals {
		mm := macros(m.Nutrients)
		d.Meals[name] = mm
		d.Total.Add(mm)
	}

	w, err := s.client.LastWeight(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("weight: %w", err)
	}
	// "last" returns the latest entry on or before the date; keep it only if it
	// was actually recorded that day.
	if w != nil && w.Value != nil && strings.HasPrefix(w.Date, date) {
		d.Weight = w.Value
	}

	if d.Total.Energy > 0 {
		items, err := s.items(ctx, day)
		if err != nil {
			return nil, fmt.Errorf("consumed items: %w", err)
		}
		s.pickRecipeVersions(s.prev[date], items, d.Meals)
		d.Items = items
		d.Photos = s.photos(ctx, day)
	}

	ex, err := s.client.Exercises(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("exercises: %w", err)
	}
	for _, e := range append(ex.Training, ex.CustomTraining...) {
		a := store.Activity{Name: e.Name, Energy: e.Energy, Duration: e.Duration, Distance: e.Distance}
		if e.Source == "fitbit" {
			a.ExternalID = e.ExternalID
		}
		d.Activities = append(d.Activities, a)
	}

	return d, nil
}

func (s *syncer) items(ctx context.Context, day time.Time) ([]store.Item, error) {
	c, err := s.client.Consumed(ctx, day)
	if err != nil {
		return nil, err
	}
	var items []store.Item
	for _, p := range c.Products {
		f, err := s.product(ctx, p.ProductID)
		if err != nil {
			return nil, err
		}
		it := store.Item{
			Name: f.Name, Daytime: p.Daytime, Amount: p.Amount, Unit: f.Unit,
			Serving: p.Serving, ServingQty: p.ServingQuantity, EntryID: p.ID, EAN: f.EAN,
		}
		setNutrients(&it, f.Nutrients, p.Amount)
		items = append(items, it)
	}
	for _, r := range c.RecipePortions {
		f, err := s.recipe(ctx, r.RecipeID)
		if err != nil {
			return nil, err
		}
		// Recipe nutrients are assumed to be per portion.
		it := store.Item{
			Name: f.Name, Daytime: r.Daytime, Amount: r.PortionCount, Unit: "portion",
			EntryID: r.ID, RecipeID: r.RecipeID, RecipeVersion: f.Hash,
		}
		setNutrients(&it, f.Nutrients, r.PortionCount)
		items = append(items, it)
	}
	for _, sp := range c.SimpleProducts {
		it := store.Item{Name: sp.Name, Daytime: sp.Daytime, AI: sp.AIGenerated, EntryID: sp.ID}
		setNutrients(&it, sp.Nutrients, 1)
		items = append(items, it)
	}
	return items, nil
}

// photos lists the day's meal photos and downloads any not yet on disk. Meal
// photos are a newer, undocumented endpoint, so failures never fail the day.
func (s *syncer) photos(ctx context.Context, day time.Time) []store.Photo {
	if s.photoDir == "" {
		return nil
	}
	raw, err := s.client.MealImagesRaw(ctx, day)
	if err != nil {
		if !errors.Is(err, yazio.ErrNotFound) && s.photoErrs.Add(1) == 1 {
			log.Printf("meal photos unavailable (%v); further errors are not logged", err)
		}
		return nil
	}
	var out []store.Photo
	for _, img := range yazio.MealImageURLs(raw) {
		p := store.Photo{Daytime: img.Daytime, URL: img.URL}
		file, err := s.download(ctx, img.URL)
		if err != nil {
			log.Printf("day %s: photo: %v", day.Format(time.DateOnly), err)
		}
		p.File = file
		out = append(out, p)
	}
	return out
}

// download stores a photo under a name derived from its URL and returns that
// name. Already-downloaded photos are not fetched again.
func (s *syncer) download(ctx context.Context, url string) (string, error) {
	sum := sha1.Sum([]byte(url))
	base := hex.EncodeToString(sum[:10])
	if m, _ := filepath.Glob(filepath.Join(s.photoDir, base+".*")); len(m) > 0 {
		return filepath.Base(m[0]), nil
	}
	data, ctype, err := s.client.Download(ctx, url)
	if err != nil {
		return "", err
	}
	ext := photoExt(url, ctype)
	if ext == "" {
		return "", fmt.Errorf("%s is not an image (%s)", url, ctype)
	}
	if err := os.MkdirAll(s.photoDir, 0o755); err != nil {
		return "", err
	}
	name := base + ext
	// Write to a temp file first so an interrupted sync never leaves a
	// truncated photo behind. The leading dot keeps it out of the glob above.
	tmp := filepath.Join(s.photoDir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, filepath.Join(s.photoDir, name))
}

func photoExt(url, ctype string) string {
	if mt, _, err := mime.ParseMediaType(ctype); err == nil {
		switch mt {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/webp":
			return ".webp"
		}
	}
	switch ext := strings.ToLower(path.Ext(strings.SplitN(url, "?", 2)[0])); ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return ext
	}
	return ""
}

func (s *syncer) product(ctx context.Context, id string) (*store.Food, error) {
	return s.food("product:"+id, func() (*store.Food, error) {
		return s.fetchProduct(ctx, id)
	})
}

func (s *syncer) fetchProduct(ctx context.Context, id string) (*store.Food, error) {
	prod, err := s.client.Product(ctx, id)
	if err != nil {
		return nil, err
	}
	f := &store.Food{Version: store.FoodVersion, Name: prod.Name, Unit: prod.BaseUnit, Nutrients: prod.Nutrients}
	if len(prod.EANs) > 0 {
		f.EAN = prod.EANs[0]
	}
	return f, nil
}

// upgradeProducts fetches the cached products stored by an older version
// (without their barcode) once more.
func (s *syncer) upgradeProducts(ctx context.Context) {
	var ids []string
	for key, f := range s.hist.Foods {
		if id, ok := strings.CutPrefix(key, "product:"); ok && !f.Missing && f.Version < store.FoodVersion {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		log.Printf("yazio: fetching %d products again for their barcodes", len(ids))
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		f, err := s.fetchProduct(ctx, id)
		if err != nil {
			log.Printf("product %s: %v", id, err)
			continue
		}
		s.mu.Lock()
		s.hist.Foods["product:"+id] = f
		s.mu.Unlock()
	}
}

// recipe returns a recipe with its ingredients and photo. It is fetched on
// first use in a run, also when cached, so edits in the app show up; if
// that fails, the cached copy is used.
func (s *syncer) recipe(ctx context.Context, id string) (*store.Food, error) {
	key := "recipe:" + id
	s.mu.Lock()
	cached, fresh := s.hist.Foods[key], s.freshRecipes[id]
	s.mu.Unlock()
	if cached != nil && (fresh || cached.Missing) {
		return cached, nil
	}
	f, err := s.fetchRecipe(ctx, id)
	switch {
	case err != nil && cached != nil:
		log.Printf("recipe %s: %v; keeping the cached copy", id, err)
		f = cached
	case errors.Is(err, yazio.ErrNotFound):
		f = &store.Food{Name: "Unknown item", Missing: true}
	case err != nil:
		return nil, err
	}
	s.mu.Lock()
	s.hist.Foods[key] = f
	if f.Hash != "" {
		s.hist.Foods[store.RecipeKey(id, f.Hash)] = f
	}
	if s.freshRecipes == nil {
		s.freshRecipes = map[string]bool{}
	}
	s.freshRecipes[id] = true
	s.mu.Unlock()
	return f, nil
}

func (s *syncer) fetchRecipe(ctx context.Context, id string) (*store.Food, error) {
	rec, err := s.client.Recipe(ctx, id)
	if err != nil {
		return nil, err
	}
	f := &store.Food{
		Version: store.FoodVersion, Name: rec.Name, Unit: "portion", Nutrients: rec.Nutrients,
		Portions: rec.PortionCount, Image: rec.Image,
	}
	for _, sv := range rec.Servings {
		ing := store.Ingredient{
			Name: sv.Name, Producer: sv.Producer, Amount: sv.Amount, Unit: sv.BaseUnit,
			Serving: sv.Serving, ServingQty: sv.ServingQuantity,
		}
		if sv.ProductID != "" {
			p, err := s.product(ctx, sv.ProductID)
			if err != nil {
				return nil, fmt.Errorf("ingredient %s: %w", sv.ProductID, err)
			}
			ing.EAN = p.EAN
			if !p.Missing {
				m := macros(p.Nutrients)
				ing.Macros = store.Macros{Energy: m.Energy * sv.Amount, Protein: m.Protein * sv.Amount, Carb: m.Carb * sv.Amount, Fat: m.Fat * sv.Amount}
			}
		}
		f.Ingredients = append(f.Ingredients, ing)
	}
	// The photo is a nice-to-have: a failed download never fails the sync.
	if rec.Image != "" && s.photoDir != "" {
		if f.ImageFile, err = s.download(ctx, rec.Image); err != nil {
			log.Printf("recipe %s: photo: %v", id, err)
		}
	}
	f.Hash = recipeHash(f)
	return f, nil
}

// recipeHash identifies what a recipe version shows: a new hash, and so a
// new stored version, only when the recipe actually changed.
func recipeHash(f *store.Food) string {
	// Barcodes are lookup keys, not content: leaving them out keeps the
	// versions stored before they were recorded.
	ings := make([]store.Ingredient, len(f.Ingredients))
	for i, in := range f.Ingredients {
		in.EAN = ""
		ings[i] = in
	}
	b, _ := json.Marshal(struct {
		Name        string
		Portions    float64
		Macros      store.Macros
		Ingredients []store.Ingredient
		Image       string
	}{f.Name, f.Portions, macros(f.Nutrients), ings, f.Image})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// pickRecipeVersions decides which version of a recipe each entry was eaten
// in. Yazio edits recipes in place, so the API only returns the current
// version; whether Yazio's day totals follow an edit is up to Yazio. Per
// meal, the version an entry had when the day was stored before (prev) and
// the current one compete: whichever adds up to Yazio's own meal total wins,
// and on a tie the entry keeps what it had. When no stored version adds up,
// the entries are marked stale (e.g. after the history was lost).
func (s *syncer) pickRecipeVersions(prev *store.Day, items []store.Item, meals map[string]store.Macros) {
	had := map[string]string{} // entry ID → version
	if prev != nil {
		for _, it := range prev.Items {
			if it.EntryID != "" && it.RecipeVersion != "" {
				had[it.EntryID] = it.RecipeVersion
			}
		}
	}
	version := func(it store.Item, v string) *store.Food {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.hist.Foods[store.RecipeKey(it.RecipeID, v)]
	}
	byMeal := map[string][]int{}
	for i, it := range items {
		byMeal[it.Daytime] = append(byMeal[it.Daytime], i)
	}
	for meal, idx := range byMeal {
		want, ok := meals[meal]
		if !ok {
			continue
		}
		// The meal's kcal with the current versions and with the earlier ones.
		var cur, old float64
		var switched []int
		for _, i := range idx {
			it := items[i]
			cur += it.Energy
			if v := had[it.EntryID]; it.RecipeID != "" && v != "" && v != it.RecipeVersion {
				if f := version(it, v); f != nil {
					old += f.Nutrients["energy.energy"] * it.Amount
					switched = append(switched, i)
					continue
				}
			}
			old += it.Energy
		}
		chosen := cur
		if len(switched) > 0 && math.Abs(old-want.Energy) <= math.Abs(cur-want.Energy) {
			chosen = old
			for _, i := range switched {
				v := had[items[i].EntryID]
				items[i].RecipeVersion = v
				setNutrients(&items[i], version(items[i], v).Nutrients, items[i].Amount)
			}
		}
		if math.Abs(chosen-want.Energy) <= math.Max(1, want.Energy*0.01) {
			continue
		}
		// No stored version fits: the recipes take whatever Yazio counted
		// for the meal beyond the other entries, split by their current kcal.
		var rest, recipes float64
		var stale []int
		for _, i := range idx {
			if items[i].RecipeID != "" {
				recipes += items[i].Energy
				stale = append(stale, i)
			} else {
				rest += items[i].Energy
			}
		}
		left := math.Max(0, want.Energy-rest)
		for _, i := range stale {
			items[i].RecipeStale = true
			if recipes > 0 {
				scaleNutrients(&items[i], left*items[i].Energy/recipes)
			} else {
				items[i].Energy = left / float64(len(stale))
			}
		}
	}
}

// upgradeRecipes fetches the cached recipes stored by an older version (no
// ingredients or photo yet), also those only eaten on days that are not
// fetched again.
func (s *syncer) upgradeRecipes(ctx context.Context) {
	var ids []string
	for key, f := range s.hist.Foods {
		if id, ok := strings.CutPrefix(key, "recipe:"); ok && !f.Missing && f.Version < store.FoodVersion {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if _, err := s.recipe(ctx, id); err != nil {
			log.Printf("recipe %s: %v", id, err)
		}
	}
}

// food returns a cached product/recipe, fetching it on first use. Deleted
// products are cached as missing so they are not re-requested every run.
func (s *syncer) food(key string, fetch func() (*store.Food, error)) (*store.Food, error) {
	s.mu.Lock()
	f, ok := s.hist.Foods[key]
	s.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := fetch()
	if errors.Is(err, yazio.ErrNotFound) {
		f, err = &store.Food{Name: "Unknown item", Missing: true}, nil
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.hist.Foods[key] = f
	s.mu.Unlock()
	return f, nil
}

// setNutrients sets the entry's energy and nutrients from n times factor:
// the amount for products (nutrients per g or ml), the portions for recipes
// (per portion) and 1 for simple and AI entries (their own total).
func setNutrients(it *store.Item, n map[string]float64, factor float64) {
	it.Energy = n["energy.energy"] * factor
	it.Protein = n["nutrient.protein"] * factor
	it.Carb = n["nutrient.carb"] * factor
	it.Fat = n["nutrient.fat"] * factor
	it.Fiber = known(n, "nutrient.dietaryfiber", factor)
	it.Sugar = known(n, "nutrient.sugar", factor)
	it.Saturated = known(n, "nutrient.saturated", factor)
}

// known is n[key] times factor, or nil when n doesn't have the key.
func known(n map[string]float64, key string, factor float64) *float64 {
	v, ok := n[key]
	if !ok {
		return nil
	}
	v *= factor
	return &v
}

// scaleNutrients sets the entry's energy to e and scales its nutrients
// along, as when a stale recipe takes a share of the meal's kcal.
func scaleNutrients(it *store.Item, e float64) {
	k := 0.0
	if it.Energy > 0 {
		k = e / it.Energy
	}
	it.Energy = e
	it.Protein *= k
	it.Carb *= k
	it.Fat *= k
	for _, p := range []*float64{it.Fiber, it.Sugar, it.Saturated} {
		if p != nil {
			*p *= k
		}
	}
}

func macros(n yazio.Nutrients) store.Macros {
	return store.Macros{
		Energy:  n["energy.energy"],
		Protein: n["nutrient.protein"],
		Carb:    n["nutrient.carb"],
		Fat:     n["nutrient.fat"],
	}
}
