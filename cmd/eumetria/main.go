// Command eumetria pulls your diary from Yazio (and Fitbit data from Google
// Health) and keeps a private history file up to date. With -serve it serves
// the page and the data, to each user only what their role allows, and syncs
// on a schedule (the Docker container); without it, it syncs once, which is
// handy for local development together with -out and the -debug-* commands.
//
// Configuration comes from flags or environment variables:
//
//	DATA_DIR                     the data folder (-data, default "data"; /data in the
//	                             container): history, photos, tokens, app.db
//	YAZIO_EMAIL, YAZIO_PASSWORD  account credentials (required unless -demo)
//	YAZIO_CLIENT_ID,             OAuth client of the Yazio app (required unless
//	YAZIO_CLIENT_SECRET          -demo; current values are in the README)
//	YAZIO_SINCE                  first day to backfill (YYYY-MM-DD, default: 365 days ago)
//	YAZIO_REFRESH_DAYS           always re-fetch this many most recent days (default 3)
//	YAZIO_REFRESH_ALL            "true" re-fetches every day in the window and
//	                             ignores the history; use it once after editing
//	                             an old diary entry (a past weigh-in, say)
//	YAZIO_API_BASE               API base URL (default https://yzapi.yazio.com/v22)
//	STATS_TITLE                  the page's title
//	OPENFOODFACTS                "false" to skip the Open Food Facts lookups
//	                             (Nutri-Score, NOVA group, additives by barcode)
//	ANTHROPIC_API_KEY            Claude API key for the weekly review (-debug-week)
//	AI_MODEL                     Claude model for it (default claude-opus-5-5)
//	AI_EFFORT                    low, medium (default), high, xhigh or max
//	AI_CREDIT_USD                the Anthropic credit bought, for a warning when
//	                             it runs low (the cost of each review is counted)
//	TZ                           timezone that decides what "today" is
//	GOOGLE_CLIENT_ID,            OAuth client for Google Health (Fitbit / Pixel
//	GOOGLE_CLIENT_SECRET,        Watch workouts, sleep, heart rate); see google.go
//	GOOGLE_REDIRECT_URL
//
// With -serve it runs as a long-lived server instead (see serve.go):
//
//	SYNC_INTERVAL                time between syncs (default 30m)
//
// Accounts and roles live in app.db; -create-user, -set-role, -create-role,
// -delete-role, -list-roles, -reset-password, -reset-second-factors and
// -create-token manage them (accounts.go).
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
	outPath     string // -out: a local copy of everything for the page; "" writes none
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
		accounts       accountCommand
		debugGoogleDay = flag.String("debug-google", "", "print the raw Google Health responses (workouts, GPS export, sleep, heart rate) for this day (YYYY-MM-DD) and exit")
	)
	flag.StringVar(&cfg.dataDir, "data", env("DATA_DIR", "data"), "folder for the history, photos, Google token and the app's database (app.db)")
	flag.StringVar(&cfg.historyPath, "history", "", "private history file (default <data>/history.json)")
	flag.StringVar(&cfg.outPath, "out", "", "without -serve: also write all of the data to this file (e.g. site/stats.json), with photos/, routes/ and days/ next to it, to work on the page from plain files")
	flag.StringVar(&cfg.since, "since", env("YAZIO_SINCE", ""), "first day to backfill, YYYY-MM-DD")
	flag.IntVar(&cfg.refreshDays, "refresh-days", envInt("YAZIO_REFRESH_DAYS", 3), "always re-fetch this many most recent days")
	flag.BoolVar(&cfg.refreshAll, "refresh-all", envBool("YAZIO_REFRESH_ALL", false), "re-fetch every day in the window, ignoring the history (serve mode: first sync only)")
	flag.IntVar(&cfg.workers, "workers", 4, "parallel requests")
	flag.StringVar(&cfg.photoDir, "photos", "", "private cache of downloaded meal photos (default <data>/photos)")
	flag.BoolVar(&cfg.demo, "demo", false, "write generated sample data instead of calling Yazio")
	flag.StringVar(&cfg.googleToken, "google-token", "", "private Google Health refresh token, written by -google-auth (default <data>/google-token.json)")
	flag.StringVar(&accounts.createUser, "create-user", "", "create an account with this name and exit (asks for the password; -role)")
	flag.StringVar(&accounts.role, "role", "viewer", "with -create-user and -set-role: the role (admin, viewer or one made with -create-role)")
	flag.StringVar(&accounts.setRole, "set-role", "", "give this account the role named by -role and exit")
	flag.StringVar(&accounts.createRole, "create-role", "", "create a role with this name, or change the scopes of one made before, from -scopes, and exit")
	flag.StringVar(&accounts.deleteRole, "delete-role", "", "delete a role nobody has any more and exit")
	flag.BoolVar(&accounts.listRoles, "list-roles", false, "list the roles with their scopes and users and exit")
	flag.StringVar(&accounts.resetPassword, "reset-password", "", "set a new password for this account and end its sessions, then exit")
	flag.StringVar(&accounts.resetFactors, "reset-second-factors", "", "remove every passkey, key, authenticator app and recovery code of this account (when all are lost) and end its sessions, then exit")
	flag.StringVar(&accounts.createToken, "create-token", "", "make an API token for this account for scripts and exit (-token-name, -scopes, -expires)")
	flag.StringVar(&accounts.tokenName, "token-name", "script", "with -create-token: what the token is for")
	flag.StringVar(&accounts.scopes, "scopes", "", "with -create-token: read (the data the account's role sees) and/or sync, comma-separated (default sync); with -create-role: the role's scopes (see -list-roles)")
	flag.IntVar(&accounts.expiresDays, "expires", 365, "with -create-token: days until it expires, 0 for never")
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
	case accounts.any():
		return runAccountCommand(ctx, cfg, accounts)
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
			addr:     *addr,
			interval: *interval,
			siteDir:  *siteDir,
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

// baseOptions are the settings of the stats from the environment; which
// parts of the data they include depends on who reads them.
func baseOptions(cfg config) stats.Options {
	return stats.Options{
		Title:        env("STATS_TITLE", "Eumetria"),
		Timezone:     env("TZ", "UTC"),
		CreditUSD:    envFloat("AI_CREDIT_USD", 0),
		PhotoPath:    "photos/",
		SyncInterval: cfg.interval,
	}
}

// syncOnce fetches what changed and saves the history; with -out it also
// writes all of the data for working on the page from plain files.
func syncOnce(ctx context.Context, cfg config) error {
	hist, err := syncHistory(ctx, cfg)
	if hist == nil {
		return err
	}
	if cfg.outPath != "" {
		out := stats.Build(hist, baseOptions(cfg).Everything(), time.Now())
		if err := exportStats(cfg.outPath, out, cfg.photoDir); err != nil {
			return fmt.Errorf("writing %s: %w", cfg.outPath, err)
		}
		log.Printf("wrote %d days to %s", len(out.Days), cfg.outPath)
	}
	return err
}

// syncHistory fetches what changed from Yazio and the other sources and
// saves the history (-demo generates one instead). The history comes back
// even when a source failed, along with that error; nil means nothing
// could be synced.
func syncHistory(ctx context.Context, cfg config) (*store.History, error) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	if cfg.demo {
		hist := demo.History(today, 400)
		hist.Devices = demo.Devices(now)
		log.Printf("demo: generated %d days", len(hist.Days))
		return hist, nil
	}

	start := today.AddDate(0, 0, -365)
	if cfg.since != "" {
		t, err := time.ParseInLocation(time.DateOnly, cfg.since, time.Local)
		if err != nil {
			return nil, fmt.Errorf("invalid -since %q: %w", cfg.since, err)
		}
		start = t
	}

	// Missing credentials are a setup error: stop before touching anything.
	if err := checkYazioEnv(); err != nil {
		return nil, err
	}
	hist, err := store.Load(cfg.historyPath)
	if err != nil {
		return nil, fmt.Errorf("loading history: %w", err)
	}

	w := window{start: start, today: today, now: now, refreshDays: cfg.refreshDays, refreshAll: cfg.refreshAll, workers: cfg.workers}
	failed, err := runSources(ctx, sources(cfg, baseOptions(cfg)), hist, w, func() error { return hist.Save(cfg.historyPath) })
	if err != nil {
		return nil, fmt.Errorf("saving history: %w", err)
	}
	log.Printf("done: %d days in the history", len(hist.Days))
	// What was synced counts even when a source failed, but the run still
	// reports it.
	if failed != nil {
		return hist, failed
	}
	return hist, ctx.Err()
}

// exportStats writes out to path with the routes, older days and photos
// next to it, as the server serves them.
func exportStats(path string, out stats.Output, photoDir string) error {
	dir := filepath.Dir(path)
	if err := publishPhotos(&out, photoDir, filepath.Join(dir, "photos")); err != nil {
		return fmt.Errorf("copying photos: %w", err)
	}
	files, err := splitFiles(&out)
	if err != nil {
		return err
	}
	keep := map[string]map[string]bool{"routes": {}, "days": {}}
	for name, data := range files {
		folder, file, _ := strings.Cut(name, "/")
		keep[folder][file] = true
		if err := writeOnce(filepath.Join(dir, folder, file), data); err != nil {
			return err
		}
	}
	for folder, names := range keep {
		if err := removeUnlisted(filepath.Join(dir, folder), names); err != nil {
			return err
		}
	}
	return store.WriteJSON(path, out, false)
}

// splitFiles moves every workout route and the days before the recent
// months out of out, into files named after their content: routes/<hash>.json
// and days/<hash>.json, one per calendar year. Workouts point at their
// route (RouteSrc) and out.Archive lists the year files. Names never change
// for the same content, so browsers may keep the files cached for good, and
// a refresh only downloads the recent days.
func splitFiles(out *stats.Output) (map[string][]byte, error) {
	files := map[string][]byte{}
	add := func(folder string, v any) (string, error) {
		plain, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(plain)
		name := folder + "/" + hex.EncodeToString(sum[:8]) + ".json"
		files[name] = plain
		return name, nil
	}
	for i := range out.Days {
		for j := range out.Days[i].Workouts {
			w := &out.Days[i].Workouts[j]
			if len(w.Route) == 0 {
				continue
			}
			name, err := add("routes", w.Route)
			if err != nil {
				return nil, err
			}
			w.Route, w.RouteSrc = nil, name
		}
	}
	out.Archive = nil
	if n := len(out.Days); n > 0 {
		last, err := time.Parse(time.DateOnly, out.Days[n-1].Date)
		if err != nil {
			return nil, err
		}
		cutoff := time.Date(last.Year(), last.Month()-recentMonths, 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		i := 0
		for i < n && out.Days[i].Date < cutoff {
			// One file per year: the days from i up to the year's end or the cutoff.
			j := i
			for j < n && out.Days[j].Date < cutoff && out.Days[j].Date[:4] == out.Days[i].Date[:4] {
				j++
			}
			name, err := add("days", struct {
				Days []stats.Day `json:"days"`
			}{out.Days[i:j]})
			if err != nil {
				return nil, err
			}
			out.Archive = append(out.Archive, name)
			i = j
		}
		out.Days = out.Days[i:]
	}
	return files, nil
}

// recentMonths is how many calendar months before the newest day's month
// stay in the stats; the days before go to the year files.
const recentMonths = 2

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

// writeOnce writes a content-named file unless it already exists.
func writeOnce(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// publishPhotos copies the photos referenced by out (meals and recipes) from
// the private cache next to an -out file. Every copy is stripped of
// metadata (GPS, camera, timestamps); a photo that cannot be stripped is
// left off the page instead.
func publishPhotos(out *stats.Output, from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	for i := range out.Days {
		d := &out.Days[i]
		kept := d.Photos[:0]
		for _, p := range d.Photos {
			if strings.HasPrefix(p.Src, "data:") { // demo data
				kept = append(kept, p)
				continue
			}
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
