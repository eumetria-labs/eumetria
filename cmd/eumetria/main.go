// Command eumetria pulls your diary from Yazio (and Fitbit data from Google
// Health), keeps a private history file up to date and writes the stats.json
// the page reads. With -serve it serves the page itself and syncs on a
// schedule (the Docker container); without it, it syncs once, which is handy
// for local development together with the -debug-* commands.
//
// Configuration comes from flags or environment variables:
//
//	DATA_DIR                     the data folder (-data, default "data"; /data in the
//	                             container): history, photos, tokens, app.db, public/
//	YAZIO_EMAIL, YAZIO_PASSWORD  account credentials (required unless -demo)
//	YAZIO_CLIENT_ID,             OAuth client of the Yazio app (required unless
//	YAZIO_CLIENT_SECRET          -demo; current values are in the README)
//	YAZIO_SINCE                  first day to backfill (YYYY-MM-DD, default: 365 days ago)
//	YAZIO_REFRESH_DAYS           always re-fetch this many most recent days (default 3)
//	YAZIO_REFRESH_ALL            "true" re-fetches every day in the window and
//	                             ignores the history; use it once after editing
//	                             an old diary entry (a past weigh-in, say)
//	YAZIO_API_BASE               API base URL (default https://yzapi.yazio.com/v22)
//	STATS_TITLE                  page title written into stats.json
//	PUBLISH_FOODS                "false" to keep food names out of the public file
//	PUBLISH_PHOTOS               "false" to keep meal photos off the public site
//	PUBLISH_HEALTH               "false" to keep Google Health data (sleep, heart
//	                             rate, workouts) out of the public file
//	PUBLISH_ROUTES               "false" to keep workout GPS tracks out of it
//	OPENFOODFACTS                "false" to skip the Open Food Facts lookups
//	                             (Nutri-Score, NOVA group, additives by barcode)
//	STATS_PASSWORD               encrypt stats.json; the page asks for this password
//	ANTHROPIC_API_KEY            Claude API key for the weekly review (-debug-week)
//	AI_MODEL                     Claude model for it (default claude-opus-5-5)
//	AI_EFFORT                    low, medium (default), high, xhigh or max
//	AI_CREDIT_USD                the Anthropic credit bought, for a warning when
//	                             it runs low (the cost of each review is counted)
//	PUBLISH_REVIEW               "false" to keep the weekly reviews off the page
//	TZ                           timezone that decides what "today" is
//	GOOGLE_CLIENT_ID,            OAuth client for Google Health (Fitbit / Pixel
//	GOOGLE_CLIENT_SECRET,        Watch workouts, sleep, heart rate); see google.go
//	GOOGLE_REDIRECT_URL
//
// With -serve it runs as a long-lived server instead (see serve.go):
//
//	SYNC_INTERVAL                time between syncs (default 30m)
//	ADMIN_TOKEN                  enables POST /api/sync, the page's refresh button
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // TZ works even in images without a zoneinfo database

	"eumetria/internal/demo"
	"eumetria/internal/google"
	"eumetria/internal/imgmeta"
	"eumetria/internal/seal"
	"eumetria/internal/stats"
	"eumetria/internal/store"
	"eumetria/internal/yazio"
)

func main() {
	log.SetFlags(log.Ltime)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// config holds the settings shared by a one-off sync and serve mode.
type config struct {
	dataDir     string // the folder all of the following live in by default
	dbPath      string // the app's own database (internal/db), serve mode only
	historyPath string
	outPath     string
	photoDir    string
	since       string
	refreshDays int
	refreshAll  bool
	workers     int
	demo        bool
	interval    time.Duration // written to stats.json for the footer; 0 if unknown
	googleToken string        // Google Health token file
}

func run() error {
	var (
		cfg      config
		debugDay = flag.String("debug-day", "", "print the raw consumed-items (with its recipes and products), daily-summary, exercises and meal-images responses for this day (YYYY-MM-DD) and exit")
		addr     = flag.String("serve", "", "serve the site on this address (e.g. :8080) and sync on a schedule instead of syncing once")
		interval = flag.Duration("interval", envDuration("SYNC_INTERVAL", 30*time.Minute), "time between syncs in serve mode")
		siteDir  = flag.String("site", "", "serve the page files from this folder instead of the compiled-in ones (serve mode; for working on the page without rebuilding)")
		health   = flag.String("healthcheck", "", "GET this URL, exit 0 if it answers 200 (Docker healthcheck) and exit")

		googleAuthFlag = flag.Bool("google-auth", false, "authorize access to Google Health (Fitbit / Pixel Watch) in the browser, save the refresh token and exit")
		debugWeek      = flag.String("debug-week", "", "write the AI review of the calendar week (Monday to Sunday) that contains this day (YYYY-MM-DD) from the local history and print it (needs ANTHROPIC_API_KEY; AI_MODEL, AI_EFFORT)")
		dryRun         = flag.Bool("dry-run", false, "with -debug-week: print the data that would be sent to Claude instead of sending it")
		rewrite        = flag.Bool("rewrite-reviews", false, "write every stored weekly review again (English and German) into the history and exit; stop the server first (needs ANTHROPIC_API_KEY; AI_MODEL, AI_EFFORT)")
		debugGoogleDay = flag.String("debug-google", "", "print the raw Google Health responses (workouts, GPS export, sleep, heart rate) for this day (YYYY-MM-DD) and exit")
	)
	flag.StringVar(&cfg.dataDir, "data", env("DATA_DIR", "data"), "folder for the history, photos, Google token, the app's database (app.db) and the published files (public/)")
	flag.StringVar(&cfg.historyPath, "history", "", "private history file (default <data>/history.json)")
	flag.StringVar(&cfg.outPath, "out", "", "stats file read by the page; photos/, routes/ and days/ are written next to it (default <data>/public/stats.json)")
	flag.StringVar(&cfg.since, "since", env("YAZIO_SINCE", ""), "first day to backfill, YYYY-MM-DD")
	flag.IntVar(&cfg.refreshDays, "refresh-days", envInt("YAZIO_REFRESH_DAYS", 3), "always re-fetch this many most recent days")
	flag.BoolVar(&cfg.refreshAll, "refresh-all", envBool("YAZIO_REFRESH_ALL", false), "re-fetch every day in the window, ignoring the history (serve mode: first sync only)")
	flag.IntVar(&cfg.workers, "workers", 4, "parallel requests")
	flag.StringVar(&cfg.photoDir, "photos", "", "private cache of downloaded meal photos (default <data>/photos)")
	flag.BoolVar(&cfg.demo, "demo", false, "write generated sample data instead of calling Yazio")
	flag.StringVar(&cfg.googleToken, "google-token", "", "private Google Health refresh token, written by -google-auth (default <data>/google-token.json)")
	flag.Parse()
	cfg.inData()

	// Serve mode syncs every -interval (the footer shows it); a one-off sync
	// has no schedule unless SYNC_INTERVAL names one.
	if *addr != "" || os.Getenv("SYNC_INTERVAL") != "" {
		cfg.interval = *interval
	}

	if *health != "" {
		return healthcheck(*health)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			log.Printf("TZ %q is not a valid time zone, using UTC", tz)
		}
	}

	switch {
	case *googleAuthFlag:
		gcfg, err := googleConfig()
		if err != nil {
			return err
		}
		return googleAuth(ctx, gcfg, cfg.googleToken)
	case *debugGoogleDay != "":
		gcfg, err := googleConfig()
		if err != nil {
			return err
		}
		gc, err := google.New(gcfg, cfg.googleToken)
		if err != nil {
			return err
		}
		return debugGoogle(ctx, gc, *debugGoogleDay)
	case *debugWeek != "":
		return debugReview(ctx, cfg, *debugWeek, *dryRun)
	case *rewrite:
		return rewriteReviews(ctx, cfg)
	case *debugDay != "":
		client, err := login(ctx)
		if err != nil {
			return err
		}
		return debugDump(ctx, client, *debugDay)
	case *addr != "":
		return serve(ctx, cfg, serveOptions{
			addr:       *addr,
			interval:   *interval,
			siteDir:    *siteDir,
			adminToken: os.Getenv("ADMIN_TOKEN"),
		})
	default:
		return syncOnce(ctx, cfg)
	}
}

// inData puts the files not given by their own flag into the data folder.
func (c *config) inData() {
	def := func(p *string, name string) {
		if *p == "" {
			*p = filepath.Join(c.dataDir, name)
		}
	}
	def(&c.historyPath, "history.json")
	def(&c.photoDir, "photos")
	def(&c.outPath, filepath.Join("public", "stats.json"))
	def(&c.googleToken, "google-token.json")
	c.dbPath = filepath.Join(c.dataDir, "app.db")
}

// checkYazioEnv reports missing Yazio credentials.
func checkYazioEnv() error {
	if os.Getenv("YAZIO_CLIENT_ID") == "" || os.Getenv("YAZIO_CLIENT_SECRET") == "" {
		return fmt.Errorf("YAZIO_CLIENT_ID and YAZIO_CLIENT_SECRET must be set (see README; or use -demo)")
	}
	if os.Getenv("YAZIO_EMAIL") == "" || os.Getenv("YAZIO_PASSWORD") == "" {
		return fmt.Errorf("YAZIO_EMAIL and YAZIO_PASSWORD must be set (or use -demo)")
	}
	return nil
}

// login checks the credentials in the environment and logs in to Yazio.
func login(ctx context.Context) (*yazio.Client, error) {
	if err := checkYazioEnv(); err != nil {
		return nil, err
	}
	email, password := os.Getenv("YAZIO_EMAIL"), os.Getenv("YAZIO_PASSWORD")
	clientID, clientSecret := os.Getenv("YAZIO_CLIENT_ID"), os.Getenv("YAZIO_CLIENT_SECRET")
	client := yazio.New(os.Getenv("YAZIO_API_BASE"))
	client.ClientID, client.ClientSecret = clientID, clientSecret
	if err := client.Login(ctx, email, password); err != nil {
		return nil, err
	}
	return client, nil
}

// syncOptions is what the page publishes, from the environment.
func syncOptions(cfg config) stats.Options {
	return stats.Options{
		Title:         env("STATS_TITLE", "Eumetria"),
		Timezone:      env("TZ", "UTC"),
		PublishFoods:  envBool("PUBLISH_FOODS", true),
		PublishPhotos: envBool("PUBLISH_PHOTOS", true),
		PublishHealth: envBool("PUBLISH_HEALTH", true),
		PublishRoutes: envBool("PUBLISH_ROUTES", true),
		PublishReview: envBool("PUBLISH_REVIEW", true),
		CreditUSD:     envFloat("AI_CREDIT_USD", 0),
		PhotoPath:     "photos/",
		SyncInterval:  cfg.interval,
	}
}

// syncOnce fetches what changed from Yazio, saves the history and writes
// stats.json and the published photos.
func syncOnce(ctx context.Context, cfg config) error {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	opts := syncOptions(cfg)
	sitePhotos := filepath.Join(filepath.Dir(cfg.outPath), "photos")

	if cfg.demo {
		hist := demo.History(today, 400)
		hist.Devices = demo.Devices(now)
		log.Printf("demo: generated %d days", len(hist.Days))
		return writeStats(cfg.outPath, stats.Build(hist, opts, now))
	}

	start := today.AddDate(0, 0, -365)
	if cfg.since != "" {
		t, err := time.ParseInLocation(time.DateOnly, cfg.since, time.Local)
		if err != nil {
			return fmt.Errorf("invalid -since %q: %w", cfg.since, err)
		}
		start = t
	}

	// Missing credentials are a setup error: stop before touching anything.
	if err := checkYazioEnv(); err != nil {
		return err
	}
	hist, err := store.Load(cfg.historyPath)
	if err != nil {
		return fmt.Errorf("loading history: %w", err)
	}

	w := window{start: start, today: today, now: now, refreshDays: cfg.refreshDays, refreshAll: cfg.refreshAll, workers: cfg.workers}
	failed, err := runSources(ctx, sources(cfg, opts), hist, w, func() error { return hist.Save(cfg.historyPath) })
	if err != nil {
		return fmt.Errorf("saving history: %w", err)
	}

	out := stats.Build(hist, opts, now)
	if err := publishPhotos(&out, cfg.photoDir, sitePhotos); err != nil {
		return fmt.Errorf("copying photos: %w", err)
	}
	if err := writeStats(cfg.outPath, out); err != nil {
		return fmt.Errorf("writing stats: %w", err)
	}
	log.Printf("done: published %d days", len(out.Days))
	// The page is published even when a source failed, but the run still
	// reports it.
	if failed != nil {
		return failed
	}
	return ctx.Err()
}

// writeStats writes stats.json and the workout routes next to it, encrypted
// when STATS_PASSWORD is set.
func writeStats(path string, out stats.Output) error {
	var sealer *seal.Sealer
	if password := os.Getenv("STATS_PASSWORD"); password != "" {
		var err error
		if sealer, err = seal.New(password); err != nil {
			return fmt.Errorf("encrypting stats: %w", err)
		}
	}
	if err := publishRoutes(&out, filepath.Join(filepath.Dir(path), "routes"), sealer); err != nil {
		return fmt.Errorf("writing routes: %w", err)
	}
	if err := publishArchive(&out, filepath.Join(filepath.Dir(path), "days"), sealer); err != nil {
		return fmt.Errorf("writing older days: %w", err)
	}
	if sealer == nil {
		return store.WriteJSON(path, out, false)
	}
	e, err := sealer.SealJSON(out)
	if err != nil {
		return fmt.Errorf("encrypting stats: %w", err)
	}
	log.Printf("stats.json is password-protected")
	return store.WriteJSON(path, e, false)
}

// publishRoutes moves every workout route out of stats.json into
// dir/<hash>.json and points the workout at it. A file is named after its
// content, so an unchanged route is never written again and browsers may
// cache it for good. Files no longer referenced are removed, so turning
// PUBLISH_ROUTES off takes the tracks off the site.
func publishRoutes(out *stats.Output, dir string, sealer *seal.Sealer) error {
	keep := map[string]bool{}
	for i := range out.Days {
		for j := range out.Days[i].Workouts {
			w := &out.Days[i].Workouts[j]
			if len(w.Route) == 0 {
				continue
			}
			plain, err := json.Marshal(w.Route)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(plain)
			hash := sum[:]
			if sealer != nil {
				hash = sealer.Name(plain)
			}
			name := hex.EncodeToString(hash[:8]) + ".json"
			if !keep[name] {
				if err := writeOnce(filepath.Join(dir, name), plain, sealer); err != nil {
					return err
				}
				keep[name] = true
			}
			w.Route, w.RouteSrc = nil, "routes/"+name
		}
	}
	return removeUnlisted(dir, keep)
}

// recentMonths is how many calendar months before the newest day's month
// stay in stats.json; the days before go to the archive files.
const recentMonths = 2

// publishArchive moves the days before the recent months out of stats.json
// into dir/<hash>.json, one file per calendar year, and lists them in
// out.Archive. Those days hardly ever change, so the browser keeps their
// files cached, and the page's refresh only downloads the recent days. As
// with routes, a file is named after its content and unreferenced files are
// removed.
func publishArchive(out *stats.Output, dir string, sealer *seal.Sealer) error {
	keep := map[string]bool{}
	out.Archive = nil
	if n := len(out.Days); n > 0 {
		last, err := time.Parse(time.DateOnly, out.Days[n-1].Date)
		if err != nil {
			return err
		}
		cutoff := time.Date(last.Year(), last.Month()-recentMonths, 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		i := 0
		for i < n && out.Days[i].Date < cutoff {
			// One file per year: the days from i up to the year's end or the cutoff.
			j := i
			for j < n && out.Days[j].Date < cutoff && out.Days[j].Date[:4] == out.Days[i].Date[:4] {
				j++
			}
			plain, err := json.Marshal(struct {
				Days []stats.Day `json:"days"`
			}{out.Days[i:j]})
			if err != nil {
				return err
			}
			sum := sha256.Sum256(plain)
			hash := sum[:]
			if sealer != nil {
				hash = sealer.Name(plain)
			}
			name := hex.EncodeToString(hash[:8]) + ".json"
			if err := writeOnce(filepath.Join(dir, name), plain, sealer); err != nil {
				return err
			}
			keep[name] = true
			out.Archive = append(out.Archive, "days/"+name)
			i = j
		}
		out.Days = out.Days[i:]
	}
	return removeUnlisted(dir, keep)
}

// removeUnlisted deletes the .json files in dir that keep doesn't name.
func removeUnlisted(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeOnce writes a content-named file (plain, or sealed with a password)
// unless it already exists.
func writeOnce(path string, plain []byte, sealer *seal.Sealer) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if sealer == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, plain, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	e, err := sealer.Seal(plain)
	if err != nil {
		return err
	}
	return store.WriteJSON(path, e, false)
}

// publishPhotos copies the photos referenced by out (meals and recipes) from
// the private cache into the site, so only photos of published days are
// ever deployed. Every
// copy is stripped of metadata (GPS, camera, timestamps); a photo that cannot
// be stripped is left off the page instead.
func publishPhotos(out *stats.Output, from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	for i := range out.Days {
		d := &out.Days[i]
		kept := d.Photos[:0]
		for _, p := range d.Photos {
			name := filepath.Base(p.Src)
			if err := publishPhoto(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
				log.Printf("day %s: not publishing photo %s: %v", d.Date, name, err)
				continue
			}
			kept = append(kept, p)
		}
		d.Photos = kept
	}
	for id, r := range out.Recipes {
		if r.Image == "" || strings.HasPrefix(r.Image, "data:") {
			continue
		}
		name := filepath.Base(r.Image)
		if err := publishPhoto(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
			log.Printf("recipe %s: not publishing photo %s: %v", r.Name, name, err)
			r.Image = ""
			out.Recipes[id] = r
		}
	}
	return nil
}

func publishPhoto(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	clean, err := imgmeta.Strip(data)
	if err != nil {
		return err
	}
	// Write and rename: in serve mode the photo may be read while it's replaced.
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, clean, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// debugDump prints the raw responses behind food details, activities and
// photos, to check the undocumented fields (is_ai_generated, serving,
// exercise distance units, meal-images) on a real
// account. It never prints credentials or the access token.
func debugDump(ctx context.Context, c *yazio.Client, date string) error {
	day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
	if err != nil {
		return fmt.Errorf("invalid -debug-day %q: %w", date, err)
	}
	var consumed json.RawMessage
	if err := c.Get(ctx, "/user/consumed-items", date, &consumed); err != nil {
		return fmt.Errorf("consumed items: %w", err)
	}
	fmt.Printf("== consumed-items %s\n%s\n", date, indent(consumed))
	// Recipes: the full response, to see the ingredients and the photo.
	var items yazio.Consumed
	_ = json.Unmarshal(consumed, &items)
	var products []string // the day's products and recipe ingredients, once each
	seen := map[string]bool{}
	addProduct := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			products = append(products, id)
		}
	}
	for _, p := range items.Products {
		addProduct(p.ProductID)
	}
	for _, r := range items.RecipePortions {
		if seen["recipe:"+r.RecipeID] {
			continue
		}
		seen["recipe:"+r.RecipeID] = true
		var raw json.RawMessage
		err := c.GetPath(ctx, "/recipes/"+url.PathEscape(r.RecipeID), &raw)
		fmt.Printf("== recipes/%s (error: %v)\n%s\n", r.RecipeID, err, indent(raw))
		var rec yazio.Recipe
		_ = json.Unmarshal(raw, &rec)
		for _, sv := range rec.Servings {
			addProduct(sv.ProductID)
		}
	}
	// Products: the full response, to see whether it carries a barcode (EAN)
	// to look the food up on Open Food Facts.
	for _, id := range products {
		var raw json.RawMessage
		err := c.GetPath(ctx, "/products/"+url.PathEscape(id), &raw)
		fmt.Printf("== products/%s (error: %v)\n%s\n", id, err, indent(raw))
	}
	var own json.RawMessage
	err = c.GetPath(ctx, "/user/recipes", &own)
	fmt.Printf("== user/recipes (error: %v)\n%s\n", err, indent(own))
	var summary json.RawMessage
	if err := c.Get(ctx, "/user/widgets/daily-summary", date, &summary); err != nil {
		return fmt.Errorf("daily summary: %w", err)
	}
	fmt.Printf("== daily-summary %s\n%s\n", date, indent(summary))
	var exercises json.RawMessage
	if err := c.Get(ctx, "/user/exercises", date, &exercises); err != nil {
		return fmt.Errorf("exercises: %w", err)
	}
	fmt.Printf("== exercises %s\n%s\n", date, indent(exercises))
	images, err := c.MealImagesRaw(ctx, day)
	fmt.Printf("== meal-images %s (error: %v)\n%s\n", date, err, indent(images))
	for _, img := range yazio.MealImageURLs(images) {
		fmt.Printf("parsed photo: daytime=%q url=%s\n", img.Daytime, img.URL)
	}
	return nil
}

func indent(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	b, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return b
}

func envDuration(key string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return def
	}
	return d
}

func envFloat(key string, def float64) float64 {
	f, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil {
		return def
	}
	return f
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return n
}
