package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"eumetria/internal/auth"
	"eumetria/internal/backup"
	"eumetria/internal/db"
	"eumetria/internal/notes"
	"eumetria/internal/settings"
	"eumetria/internal/store"
	"eumetria/site"
)

// Serve mode: one process serves the site and keeps it up to date, so the
// whole dashboard runs as a single container behind a reverse proxy. The
// routes and who may use them are in api.go, the data each user gets in
// data.go.

type serveOptions struct {
	addr     string
	interval time.Duration
	siteDir  string // "" serves the compiled-in page files
}

// csp matches the <meta> policy in index.html; as a header it can also forbid
// framing, which a <meta> policy cannot.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: https://tile.openstreetmap.org; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; worker-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

const (
	syncTimeout    = 15 * time.Minute // a full backfill with photos takes a few minutes
	rewriteTimeout = 2 * time.Hour    // a minute or so per week
)

func serve(ctx context.Context, cfg config, o serveOptions) error {
	if o.interval < settings.MinInterval {
		return fmt.Errorf("-interval %s is too short, use at least %s", o.interval, settings.MinInterval)
	}
	pages, from := fs.FS(site.Files), "compiled-in page files"
	if o.siteDir != "" {
		pages, from = os.DirFS(o.siteDir), o.siteDir
	}
	if _, err := fs.Stat(pages, "index.html"); err != nil {
		return fmt.Errorf("%s have no index.html: %w", from, err)
	}
	if os.Getenv("ADMIN_TOKEN") != "" {
		log.Printf("ADMIN_TOKEN is no longer used: sign in as an admin for the refresh button, or make an API token with -create-token for scripts")
	}

	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	if v, err := database.Version(ctx); err == nil {
		log.Printf("database %s at schema version %d", cfg.dbPath, v)
	}
	accounts := auth.NewStore(database)
	if err := setupAccounts(ctx, accounts, cfg.demo); err != nil {
		return err
	}
	conf, err := settings.New(database, os.Getenv("SETTINGS_KEY"))
	if err != nil {
		return err
	}
	if !conf.CanEncrypt() {
		log.Printf("SETTINGS_KEY isn't set: secrets stay in the environment and can't be changed in the page")
	}
	if n, err := conf.Import(ctx); err != nil {
		return fmt.Errorf("importing the settings: %w", err)
	} else if n > 0 {
		log.Printf("settings: copied %d values from the environment; the page shows them under Settings", n)
	}
	values, err := conf.Values(ctx)
	if err != nil {
		return err
	}
	cfg.interval = o.interval
	cfg = cfg.with(values)
	noteImages := filepath.Join(cfg.dataDir, "note-images")
	go imageLoop(ctx, &notes.Store{DB: database, Now: time.Now}, noteImages)

	data := newPublished(baseOptions(cfg))
	if !cfg.demo {
		// The last sync's data, until the first sync here is done.
		hist, err := store.Load(cfg.historyPath)
		if err != nil {
			return fmt.Errorf("loading history: %w", err)
		}
		if info, err := os.Stat(cfg.historyPath); err == nil {
			data.set(hist, info.ModTime())
		}
	}
	r := newRunner(cfg, cfg.interval, data)
	r.settings = conf
	srv := &http.Server{
		Addr:              o.addr,
		Handler:           newHandler(r, pages, data, cfg.photoDir, noteImages, filepath.Join(cfg.dataDir, "avatars"), accounts),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go r.schedule(ctx)
	go r.backupLoop(ctx)

	errc := make(chan error, 1)
	go func() {
		log.Printf("serving %s on %s, syncing every %s", from, o.addr, o.interval)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	r.wait()
	return nil
}

// runner runs one job at a time, a sync or rewriting the reviews, on a
// schedule or on request, and hands each new history to data. Each job reads
// the settings when it starts.
type runner struct {
	cfg      config
	started  time.Time
	data     *published
	settings *settings.Store                                       // nil in tests that don't need it
	sync     func(context.Context, config) (*store.History, error) // syncHistory; replaced in tests
	rewrite  func(context.Context, config, func(done, total int)) (*store.History, error)
	backup   func(context.Context, config, func(context.Context, string) error, *backup.Run) error // writeBackup; replaced in tests
	retimed  chan struct{}                                                                         // the interval changed

	mu          sync.Mutex
	interval    time.Duration
	running     bool
	runningAll  bool   // the running sync re-fetches everything
	job         string // what runs, when it isn't a sync
	jobDone     int
	jobTotal    int
	jobError    string
	lastAttempt time.Time
	lastSuccess time.Time
	lastError   string
	wg          sync.WaitGroup
}

func newRunner(cfg config, interval time.Duration, data *published) *runner {
	return &runner{cfg: cfg, interval: interval, started: time.Now(), data: data, sync: syncHistory, rewrite: rewriteServer, backup: writeBackup, retimed: make(chan struct{}, 1)}
}

func rewriteServer(ctx context.Context, cfg config, progress func(done, total int)) (*store.History, error) {
	if cfg.values.AnthropicKey == "" {
		return nil, errors.New("no Anthropic API key in the settings")
	}
	return rewriteReviews(ctx, cfg, writer(cfg.values.AnthropicKey), progress)
}

// current is the config for a job that starts now.
func (r *runner) current(ctx context.Context) config {
	if r.settings == nil {
		return r.cfg
	}
	v, err := r.settings.Values(ctx)
	if err != nil {
		log.Printf("settings: %v", err)
		return r.cfg
	}
	return r.cfg.with(v)
}

// reconfigure takes new settings: the schedule moves to the new interval at
// once. It returns the config they make.
func (r *runner) reconfigure(v settings.Values) config {
	cfg := r.cfg.with(v)
	r.mu.Lock()
	changed := cfg.interval != r.interval && cfg.interval > 0
	if changed {
		r.interval = cfg.interval
	}
	r.mu.Unlock()
	if changed {
		select {
		case r.retimed <- struct{}{}:
		default:
		}
	}
	return cfg
}

func (r *runner) period() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interval
}

// schedule syncs right away and then every interval until ctx ends. A
// refresh-all request (flag or YAZIO_REFRESH_ALL) only applies to the first
// sync; otherwise every scheduled run would re-fetch a whole year.
func (r *runner) schedule(ctx context.Context) {
	r.start(ctx, r.cfg.refreshAll)
	t := time.NewTicker(r.period())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.retimed:
			t.Reset(r.period())
		case <-t.C:
			r.start(ctx, false)
		}
	}
}

// start begins a sync in the background and reports false if one is already
// running.
func (r *runner) start(ctx context.Context, refreshAll bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running, r.runningAll = true, refreshAll
	r.lastAttempt = time.Now()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		cfg := r.current(ctx)
		cfg.refreshAll = refreshAll
		syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
		hist, err := r.sync(syncCtx, cfg)
		cancel()
		if hist != nil {
			r.data.set(hist, time.Now())
		}

		r.mu.Lock()
		defer r.mu.Unlock()
		r.running, r.runningAll = false, false
		if err != nil {
			r.lastError = err.Error()
			log.Printf("sync failed: %v", err)
		} else {
			r.lastError = ""
			r.lastSuccess = time.Now()
		}
	}()
	return true
}

// startRewrite has every review written again in the background, and reports
// false if a job is running. The history comes in as far as it got.
func (r *runner) startRewrite(ctx context.Context) bool {
	return r.startJob(ctx, "rewrite", rewriteTimeout, func(ctx context.Context, cfg config, progress func(done, total int)) error {
		hist, err := r.rewrite(ctx, cfg, progress)
		if hist != nil {
			r.data.set(hist, time.Now())
		}
		return err
	})
}

// startJob runs fn in the background unless something runs already, which it
// reports with false. Nothing else writes the history meanwhile.
func (r *runner) startJob(ctx context.Context, name string, timeout time.Duration, fn func(context.Context, config, func(done, total int)) error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running, r.job, r.jobDone, r.jobTotal, r.jobError = true, name, 0, 0, ""
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		err := fn(jobCtx, r.current(ctx), func(done, total int) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.jobDone, r.jobTotal = done, total
		})
		r.mu.Lock()
		defer r.mu.Unlock()
		r.running, r.job = false, ""
		if err != nil {
			r.jobError = err.Error()
			log.Printf("%s: %v", name, err)
		}
	}()
	return true
}

func (r *runner) wait() { r.wg.Wait() }

type syncStatus struct {
	Running bool `json:"running"`
	// All marks a running refresh of the whole history.
	All bool `json:"all,omitempty"`
	// Job is what runs when it isn't a sync, with how far it got; JobError
	// how the last one ended.
	Job      string `json:"job,omitempty"`
	JobDone  int    `json:"job_done,omitempty"`
	JobTotal int    `json:"job_total,omitempty"`
	JobError string `json:"job_error,omitempty"`
	// RefreshDays is how many recent days a normal sync re-fetches.
	RefreshDays int        `json:"refresh_days"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Interval    string     `json:"interval"`
}

func (r *runner) status() syncStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	opt := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	return syncStatus{
		Running:     r.running,
		All:         r.runningAll,
		Job:         r.job,
		JobDone:     r.jobDone,
		JobTotal:    r.jobTotal,
		JobError:    r.jobError,
		RefreshDays: r.cfg.refreshDays,
		LastAttempt: opt(r.lastAttempt),
		LastSuccess: opt(r.lastSuccess),
		LastError:   r.lastError,
		Interval:    r.interval.String(),
	}
}

// healthy reports whether a sync succeeded within the last three intervals
// (or the server is younger than that and still catching up).
func (r *runner) healthy(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last := r.lastSuccess
	if last.IsZero() {
		last = r.started
	}
	return now.Sub(last) < 3*r.interval
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex, nofollow, noimageindex, noarchive")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		next.ServeHTTP(w, req)
	})
}

// etags returns a content hash per file of compiled-in page files. They have
// no modification time, so without an ETag a browser couldn't revalidate
// them and would download every file again on each visit. Files on disk
// (-site) get Last-Modified instead and need none.
func etags(pages fs.FS) map[string]string {
	if pages != fs.FS(site.Files) {
		return nil
	}
	tags := map[string]string{}
	_ = fs.WalkDir(pages, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(pages, p)
		if err != nil {
			return err
		}
		tags[p] = contentTag(data)
		return nil
	})
	tags[""] = tags["index.html"] // "/" serves index.html
	return tags
}

// contentTag is an ETag naming data by its hash.
func contentTag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// noDirs hides directories (except the root, which serves index.html), so
// the photo folder can't be listed.
type noDirs struct{ fs.FS }

func (n noDirs) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil || name == "." {
		return f, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// healthcheck is the container's HEALTHCHECK: distroless images have no curl.
func healthcheck(url string) error {
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Get(url)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New(res.Status)
	}
	return nil
}
