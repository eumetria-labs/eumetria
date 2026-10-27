package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"eumetria/internal/seal"
	"eumetria/internal/stats"
	"eumetria/internal/store"
	"eumetria/internal/yazio"
)

func TestDaysToFetch(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	cur := func() *store.Day { return &store.Day{Version: store.DayVersion} }
	h := &store.History{Days: map[string]*store.Day{
		"2026-01-01": cur(), "2026-01-02": {}, "2026-01-04": cur(), "2026-01-05": cur(), "2026-01-06": cur(),
	}}
	got := daysToFetch(h, day("2026-01-01"), day("2026-01-06"), 2, false)
	want := []string{"2026-01-02", "2026-01-03", "2026-01-05", "2026-01-06"} // outdated + gap + last 2 days
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Format(time.DateOnly) != want[i] {
			t.Errorf("day %d: got %s, want %s", i, got[i].Format(time.DateOnly), want[i])
		}
	}

	// refreshAll ignores the history and returns the whole window.
	all := daysToFetch(h, day("2026-01-01"), day("2026-01-06"), 2, true)
	if len(all) != 6 {
		t.Errorf("refreshAll: got %d days, want 6", len(all))
	}
	if len(all) > 0 && all[0].Format(time.DateOnly) != "2026-01-01" {
		t.Errorf("refreshAll: first day = %s, want 2026-01-01", all[0].Format(time.DateOnly))
	}
}

// fakeYazio mimics the response shapes of the endpoints the syncer uses.
func fakeYazio(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "me@example.com" || body["grant_type"] != "password" || body["client_id"] != "test-client" {
			http.Error(w, "bad credentials", http.StatusBadRequest)
			return
		}
		write(w, map[string]any{"access_token": "tok", "expires_in": 3600})
	})
	mux.HandleFunc("GET /user/widgets/daily-summary", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"activity_energy": 120, "consume_activity_energy": true, "steps": 8000, "water_intake": 1500,
			"goals": map[string]float64{"energy.energy": 2120, "bodyvalue.weight": 75},
			"meals": map[string]any{
				"breakfast": map[string]any{"energy_goal": 500, "nutrients": map[string]float64{"energy.energy": 250, "nutrient.protein": 20, "nutrient.carb": 50, "nutrient.fat": 10}},
				// Curry 2 × 300 + quick add 150, as Yazio adds it up.
				"lunch": map[string]any{"energy_goal": 700, "nutrients": map[string]float64{"energy.energy": 750, "nutrient.protein": 40, "nutrient.carb": 60, "nutrient.fat": 20}},
			},
		})
	}))
	mux.HandleFunc("GET /user/bodyvalues/weight/last", auth(func(w http.ResponseWriter, r *http.Request) {
		// Only 2026-03-02 has a weigh-in; other days return that older entry.
		write(w, map[string]any{"id": "w1", "date": "2026-03-02 07:12:00", "value": 80.4})
	}))
	mux.HandleFunc("GET /user/consumed-items", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"products":        []any{map[string]any{"id": "c1", "daytime": "breakfast", "product_id": "p1", "amount": 50, "serving": "slice", "serving_quantity": 2}, map[string]any{"id": "c2", "daytime": "lunch", "product_id": "gone", "amount": 10}},
			"recipe_portions": []any{map[string]any{"id": "c3", "daytime": "lunch", "recipe_id": "r1", "portion_count": 2}},
			"simple_products": []any{map[string]any{"id": "c4", "daytime": "lunch", "name": "Quick add", "nutrients": map[string]float64{"energy.energy": 150}, "is_ai_generated": true}},
		})
	}))
	mux.HandleFunc("GET /products/p1", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"name": "Oats", "base_unit": "g", "nutrients": map[string]float64{"energy.energy": 3.7}})
	}))
	mux.HandleFunc("GET /products/gone", auth(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	var srv *httptest.Server // for the image URLs
	mux.HandleFunc("GET /recipes/r1", auth(func(w http.ResponseWriter, r *http.Request) {
		// Real shape (2026-10-20): ingredients for the whole recipe, no
		// nutrients per ingredient, and a photo on the CDN.
		write(w, map[string]any{
			"name": "Curry", "portion_count": 4, "nutrients": map[string]float64{"energy.energy": 300},
			"image": srv.URL + "/img/curry.webp",
			"servings": []any{
				map[string]any{"name": "Oats", "producer": "Mill", "product_id": "p1", "amount": 200, "base_unit": "g", "serving": "gram", "serving_quantity": 200},
				map[string]any{"name": "Old thing", "product_id": "gone", "amount": 10, "base_unit": "g"},
			},
		})
	}))
	mux.HandleFunc("GET /img/curry.webp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("webp"))
	})
	mux.HandleFunc("GET /user/exercises", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"training":        []any{map[string]any{"id": "e1", "name": "Running", "energy": 350, "duration": 1800, "distance": 5.2}},
			"custom_training": []any{},
		})
	}))
	mux.HandleFunc("GET /v22/user/meal-images/{date}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("date") != "2026-03-02" {
			write(w, []any{})
			return
		}
		// Real shape: one photo URL per meal.
		write(w, map[string]any{"lunch": srv.URL + "/img/lunch.jpg"})
	}))
	mux.HandleFunc("GET /img/lunch.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMealImageURLs(t *testing.T) {
	// The documented shape ({"dinner": url}) plus a nested variant, in case
	// Yazio ever returns several photos per meal.
	raw := []byte(`{"breakfast":"https://cdn/a.jpg","items":[{"daytime":"dinner","photo":"https://cdn/b.jpg"}],"other":"not a url"}`)
	got := map[string]string{}
	for _, img := range yazio.MealImageURLs(raw) {
		got[img.URL] = img.Daytime
	}
	want := map[string]string{"https://cdn/a.jpg": "breakfast", "https://cdn/b.jpg": "dinner"}
	if len(got) != len(want) || got["https://cdn/a.jpg"] != "breakfast" || got["https://cdn/b.jpg"] != "dinner" {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSyncAgainstFakeAPI(t *testing.T) {
	srv := fakeYazio(t)
	ctx := context.Background()
	c := yazio.New(srv.URL)
	c.ClientID, c.ClientSecret = "test-client", "test-secret"
	// The fake server stands in for Yazio's image CDN.
	c.AllowImage = func(u *url.URL) bool { return u.Host == strings.TrimPrefix(srv.URL, "http://") }
	if err := c.Login(ctx, "me@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	hist := &store.History{Days: map[string]*store.Day{}, Foods: map[string]*store.Food{}}
	s := &syncer{client: c, hist: hist, photoDir: t.TempDir()}

	start, _ := time.Parse(time.DateOnly, "2026-03-01")
	days := daysToFetch(hist, start, start.AddDate(0, 0, 2), 3, false)
	if failed := s.run(ctx, days, 2); failed != 0 {
		t.Fatalf("%d days failed", failed)
	}

	d := hist.Days["2026-03-02"]
	if d == nil {
		t.Fatal("missing day 2026-03-02")
	}
	if d.Total.Energy != 1000 || d.Total.Protein != 60 {
		t.Errorf("totals = %+v", d.Total)
	}
	if d.Weight == nil || *d.Weight != 80.4 {
		t.Errorf("weight = %v, want 80.4", d.Weight)
	}
	if hist.Days["2026-03-03"].Weight != nil {
		t.Error("weigh-in from an earlier day must not be attributed to 2026-03-03")
	}
	byName := map[string]store.Item{}
	for _, it := range d.Items {
		byName[it.Name] = it
	}
	if got := byName["Oats"].Energy; got != 185 {
		t.Errorf("Oats energy = %v, want 185", got)
	}
	if got := byName["Curry"].Energy; got != 600 {
		t.Errorf("Curry energy = %v, want 600", got)
	}
	if _, ok := byName["Unknown item"]; !ok {
		t.Error("deleted product should be kept as Unknown item")
	}
	if !hist.Foods["product:gone"].Missing {
		t.Error("deleted product should be cached as missing")
	}
	curry := hist.Foods["recipe:r1"]
	if it := byName["Curry"]; it.RecipeStale || it.EntryID != "c3" || it.RecipeVersion != curry.Hash || hist.Foods[store.RecipeKey("r1", curry.Hash)] != curry {
		t.Errorf("curry entry %+v, versions stored: %v", it, hist.Foods[store.RecipeKey("r1", curry.Hash)] != nil)
	}
	if byName["Curry"].RecipeID != "r1" || curry.Version != store.FoodVersion || curry.Portions != 4 || curry.ImageFile == "" {
		t.Errorf("recipe = %+v, item %+v", curry, byName["Curry"])
	}
	if in := curry.Ingredients; len(in) != 2 || in[0].Energy != 740 || in[0].Producer != "Mill" || in[0].Unit != "g" || in[1].Energy != 0 {
		t.Errorf("ingredients = %+v", in)
	}

	if !byName["Quick add"].AI || byName["Oats"].AI {
		t.Error("only the AI-generated simple product should be flagged")
	}
	if it := byName["Oats"]; it.Serving != "slice" || it.ServingQty != 2 || it.Amount != 50 || it.Unit != "g" {
		t.Errorf("Oats serving = %+v", it)
	}
	if len(d.Photos) != 1 || d.Photos[0].Daytime != "lunch" || d.Photos[0].File == "" {
		t.Errorf("photos = %+v", d.Photos)
	}

	out := stats.Build(hist, stats.Options{PublishFoods: false}, time.Now())
	if len(out.Days) != 3 || out.Goals.Weight != 75 {
		t.Errorf("stats: %d days, goals %+v", len(out.Days), out.Goals)
	}
	if out.Goals.Energy != 2000 || out.Days[1].EnergyBase != 2000 || out.Days[1].EnergyGoal != 2120 {
		t.Errorf("goal %v, day base %v, day goal %v; want activity energy excluded from base",
			out.Goals.Energy, out.Days[1].EnergyBase, out.Days[1].EnergyGoal)
	}
	if len(out.Days[0].Foods) != 0 || len(out.Days[1].Photos) != 0 {
		t.Error("foods and photos must be omitted when not published")
	}
	out = stats.Build(hist, stats.Options{PublishFoods: true, PublishPhotos: true, PhotoPath: "photos/"}, time.Now())
	if p := out.Days[1].Photos; len(p) != 1 || p[0].Src != "photos/"+d.Photos[0].File {
		t.Errorf("published photos = %+v", p)
	}
	var curryFood stats.Food
	for _, f := range out.Days[1].Foods {
		if f.Name == "Curry" {
			curryFood = f
		}
	}
	if rec := out.Recipes[curryFood.Recipe]; curryFood.Recipe != curry.Hash || rec.Image != "photos/"+curry.ImageFile || len(rec.Ingredients) != 2 || rec.Ingredients[0].Serving != "" {
		t.Errorf("published recipe %q = %+v", curryFood.Recipe, rec)
	}

	// Entries synced before items kept the recipe ID are matched by name.
	for i := range d.Items {
		d.Items[i].RecipeID, d.Items[i].RecipeVersion = "", ""
	}
	out = stats.Build(hist, stats.Options{PublishFoods: true}, time.Now())
	for _, f := range out.Days[1].Foods {
		if f.Name == "Curry" && (f.Recipe != "r1" || out.Recipes["r1"].Image != "") {
			t.Errorf("old entry: recipe %q, image %q (photos not published)", f.Recipe, out.Recipes["r1"].Image)
		}
	}
}

func TestPublishPhotosStripsAndDropsBadFiles(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	// A JPEG with a comment segment, and a file that is not an image.
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4)), nil)
	img := buf.Bytes()
	withComment := append([]byte{0xFF, 0xD8, 0xFF, 0xFE, 0, 8, 'h', 'o', 'm', 'e', '!', '!'}, img[2:]...)
	_ = os.WriteFile(filepath.Join(from, "good.jpg"), withComment, 0o644)
	_ = os.WriteFile(filepath.Join(from, "bad.jpg"), []byte("not an image"), 0o644)

	out := stats.Output{Days: []stats.Day{{Date: "2026-10-13", Photos: []stats.Photo{{Src: "photos/good.jpg"}, {Src: "photos/bad.jpg"}}}}}
	if err := publishPhotos(&out, from, to); err != nil {
		t.Fatal(err)
	}
	if p := out.Days[0].Photos; len(p) != 1 || p[0].Src != "photos/good.jpg" {
		t.Errorf("photos = %+v, want only the good one", p)
	}
	got, err := os.ReadFile(filepath.Join(to, "good.jpg"))
	if err != nil || bytes.Contains(got, []byte("home!!")) {
		t.Errorf("published photo still has its comment (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(to, "bad.jpg")); err == nil {
		t.Error("unstrippable file was published")
	}
}

func TestPublishRoutes(t *testing.T) {
	dir := t.TempDir()
	route := []store.RoutePoint{{52.5, 13.4, 0, 34, 120}, {52.51, 13.41, 1200, 36, 131}}
	build := func() stats.Output {
		return stats.Output{Days: []stats.Day{
			{Date: "2026-10-12", Workouts: []stats.Workout{{ID: "a", Route: route}, {ID: "b"}}},
			{Date: "2026-10-13", Workouts: []stats.Workout{{ID: "c", Route: route}}},
		}}
	}
	_ = os.WriteFile(filepath.Join(dir, "stale.json"), []byte("[]"), 0o644)

	out := build()
	if err := publishRoutes(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	a, b, c := out.Days[0].Workouts[0], out.Days[0].Workouts[1], out.Days[1].Workouts[0]
	if a.Route != nil || !strings.HasPrefix(a.RouteSrc, "routes/") || c.RouteSrc != a.RouteSrc || b.RouteSrc != "" {
		t.Fatalf("route_src a=%q b=%q c=%q, route left in stats: %v", a.RouteSrc, b.RouteSrc, c.RouteSrc, a.Route != nil)
	}
	var back []store.RoutePoint
	raw, _ := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(a.RouteSrc, "routes/")))
	if err := json.Unmarshal(raw, &back); err != nil || len(back) != 2 || back[1][2] != 1200 {
		t.Errorf("route file = %s (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.json")); err == nil {
		t.Error("unreferenced route file was kept")
	}

	// With a password the file is sealed and gets a new name, and the plain
	// one is removed.
	sealer, err := seal.New("pw")
	if err != nil {
		t.Fatal(err)
	}
	out = build()
	if err := publishRoutes(&out, dir, sealer); err != nil {
		t.Fatal(err)
	}
	sealed := out.Days[0].Workouts[0].RouteSrc
	if sealed == a.RouteSrc {
		t.Error("sealed route kept the plain file's name")
	}
	var env seal.Envelope
	raw, _ = os.ReadFile(filepath.Join(dir, strings.TrimPrefix(sealed, "routes/")))
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if plain, err := seal.Open(&env, "pw"); err != nil || !strings.Contains(string(plain), "1200") {
		t.Errorf("sealed route = %s (%v)", plain, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files in routes/, want only the sealed one", len(entries))
	}
}

func TestRecipeUpgradeAndFallback(t *testing.T) {
	srv := fakeYazio(t)
	ctx := context.Background()
	c := yazio.New(srv.URL)
	c.ClientID, c.ClientSecret = "test-client", "test-secret"
	c.AllowImage = func(u *url.URL) bool { return u.Host == strings.TrimPrefix(srv.URL, "http://") }
	if err := c.Login(ctx, "me@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	// Recipes cached by an older version have no ingredients yet.
	old := &store.Food{Name: "Curry", Unit: "portion", Nutrients: map[string]float64{"energy.energy": 300}}
	stale := &store.Food{Version: store.FoodVersion, Name: "Stew", Unit: "portion"}
	hist := &store.History{Days: map[string]*store.Day{}, Foods: map[string]*store.Food{"recipe:r1": old, "recipe:nope": stale}}
	s := &syncer{client: c, hist: hist}
	s.upgradeRecipes(ctx)
	if f := hist.Foods["recipe:r1"]; f.Version != store.FoodVersion || len(f.Ingredients) != 2 {
		t.Errorf("old recipe not upgraded: %+v", f)
	}
	// A recipe that can no longer be fetched keeps its cached copy.
	if f, err := s.recipe(ctx, "nope"); err != nil || f != stale {
		t.Errorf("recipe(nope) = %+v, %v; want the cached copy", f, err)
	}
}

func TestPickRecipeVersions(t *testing.T) {
	food := func(kcal float64) *store.Food {
		return &store.Food{Nutrients: map[string]float64{"energy.energy": kcal}}
	}
	// The recipe had 200 g noodles (v1, 500 kcal) and now has 300 g (v2).
	hist := &store.History{Foods: map[string]*store.Food{
		store.RecipeKey("r", "v1"): food(500), store.RecipeKey("r", "v2"): food(650),
		store.RecipeKey("r", "v3"): food(650), // other ingredients, same kcal
	}}
	s := &syncer{hist: hist}
	prev := &store.Day{Items: []store.Item{{EntryID: "e", RecipeID: "r", RecipeVersion: "v1", Amount: 1, Energy: 500}}}
	fresh := func() []store.Item {
		return []store.Item{
			{EntryID: "e", Daytime: "dinner", RecipeID: "r", RecipeVersion: "v2", Amount: 1, Energy: 650},
			{EntryID: "x", Daytime: "dinner", Energy: 100},
		}
	}
	for _, c := range []struct {
		name    string
		prev    *store.Day
		yazio   float64
		version string
		energy  float64
		stale   bool
	}{
		{"Yazio kept the old totals", prev, 600, "v1", 500, false},
		{"Yazio recalculated the day", prev, 750, "v2", 650, false},
		{"both fit: keep what it had", &store.Day{Items: []store.Item{{EntryID: "e", RecipeID: "r", RecipeVersion: "v3"}}}, 750, "v3", 650, false},
		{"history lost, old totals", nil, 600, "v2", 500, true}, // Yazio's 600 minus the other 100
		{"history lost, recalculated", nil, 750, "v2", 650, false},
	} {
		items := fresh()
		s.pickRecipeVersions(c.prev, items, map[string]store.Macros{"dinner": {Energy: c.yazio}})
		if it := items[0]; it.RecipeVersion != c.version || it.Energy != c.energy || it.RecipeStale != c.stale || items[1].RecipeStale {
			t.Errorf("%s: got %s %v stale=%v, want %s %v stale=%v", c.name, it.RecipeVersion, it.Energy, it.RecipeStale, c.version, c.energy, c.stale)
		}
	}
}

func TestPublishArchive(t *testing.T) {
	dir := t.TempDir()
	var days []stats.Day
	for d := time.Date(2025, 11, 20, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		days = append(days, stats.Day{Date: d.Format(time.DateOnly), Energy: 2000})
	}
	_ = os.WriteFile(filepath.Join(dir, "stale.json"), []byte("{}"), 0o644)

	out := stats.Output{Days: append([]stats.Day(nil), days...)}
	if err := publishArchive(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	// August, September and October stay; 2025 and January–July 2026 move out.
	if out.Days[0].Date != "2026-08-01" || out.Days[len(out.Days)-1].Date != "2026-10-22" {
		t.Fatalf("stats.json keeps %s to %s", out.Days[0].Date, out.Days[len(out.Days)-1].Date)
	}
	if len(out.Archive) != 2 {
		t.Fatalf("archive = %v, want one file per year", out.Archive)
	}
	var got []string
	for _, src := range out.Archive {
		var f struct{ Days []stats.Day }
		raw, _ := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(src, "days/")))
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		got = append(got, f.Days[0].Date, f.Days[len(f.Days)-1].Date)
	}
	if want := []string{"2025-11-20", "2025-12-31", "2026-01-01", "2026-07-31"}; !slices.Equal(got, want) {
		t.Errorf("archive files span %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.json")); err == nil {
		t.Error("unreferenced archive file was kept")
	}

	// The next sync with the same days writes nothing new.
	again := stats.Output{Days: append([]stats.Day(nil), days...)}
	if err := publishArchive(&again, dir, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again.Archive, out.Archive) {
		t.Errorf("unchanged days got new names: %v, then %v", out.Archive, again.Archive)
	}

	// A short history stays in stats.json, and old files are removed.
	short := stats.Output{Days: days[len(days)-10:]}
	if err := publishArchive(&short, dir, nil); err != nil {
		t.Fatal(err)
	}
	if short.Archive != nil || len(short.Days) != 10 {
		t.Errorf("short history: archive %v, %d days", short.Archive, len(short.Days))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d files left in days/", len(entries))
	}
}

func TestSetNutrients(t *testing.T) {
	// A product per gram, 150 g: fibre known, saturated fat not.
	var it store.Item
	setNutrients(&it, map[string]float64{"energy.energy": 1.2, "nutrient.protein": 0.1, "nutrient.carb": 0.2, "nutrient.fat": 0.05, "nutrient.dietaryfiber": 0.04, "nutrient.sugar": 0.1}, 150)
	if it.Energy != 180 || it.Protein != 15 || it.Carb != 30 || it.Fat != 7.5 {
		t.Fatalf("macros = %+v", it)
	}
	if it.Fiber == nil || *it.Fiber != 6 || it.Sugar == nil || *it.Sugar != 15 || it.Saturated != nil {
		t.Fatalf("fiber %v, saturated %v; want 6 and unknown", it.Fiber, it.Saturated)
	}
	// A stale recipe's share of the meal: everything scales with the energy.
	scaleNutrients(&it, 90)
	if it.Energy != 90 || it.Protein != 7.5 || *it.Fiber != 3 || *it.Sugar != 7.5 {
		t.Fatalf("scaled = %+v, fiber %v", it, *it.Fiber)
	}
}
