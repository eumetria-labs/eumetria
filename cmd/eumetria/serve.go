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
	"eumetria/internal/db"
	"eumetria/site"
)

// Serve mode: one process serves the site and keeps it up to date, so the
// whole dashboard runs as a single container behind a reverse proxy. The
// routes and who may use them are in api.go.

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
	minInterval = 5 * time.Minute  // Yazio's API is private; don't hammer it
	syncTimeout = 15 * time.Minute // a full backfill with photos takes a few minutes
)

func serve(ctx context.Context, cfg config, o serveOptions) error {
	if o.interval < minInterval {
		return fmt.Errorf("-interval %s is too short, use at least %s", o.interval, minInterval)
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
	store := auth.NewStore(database)
	if err := setupAccounts(ctx, store, cfg.demo); err != nil {
		return err
	}
	go snapshotLoop(ctx, database, filepath.Join(cfg.dataDir, "backup"))

	r := newRunner(cfg, o.interval)
	srv := &http.Server{
		Addr:              o.addr,
		Handler:           newHandler(r, pages, filepath.Dir(cfg.outPath), store),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go r.schedule(ctx)

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

// runner runs syncs one at a time, on a schedule or on request.
type runner struct {
	cfg      config
	interval time.Duration
	started  time.Time
	sync     func(context.Context, config) error // syncOnce; replaced in tests

	mu          sync.Mutex
	running     bool
	runningAll  bool // the running sync re-fetches everything
	lastAttempt time.Time
	lastSuccess time.Time
	lastError   string
	wg          sync.WaitGroup
}

func newRunner(cfg config, interval time.Duration) *runner {
	return &runner{cfg: cfg, interval: interval, started: time.Now(), sync: syncOnce}
}

// schedule syncs right away and then every interval until ctx ends. A
// refresh-all request (flag or YAZIO_REFRESH_ALL) only applies to the first
// sync; otherwise every scheduled run would re-fetch a whole year.
func (r *runner) schedule(ctx context.Context) {
	r.start(ctx, r.cfg.refreshAll)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
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
		cfg := r.cfg
		cfg.refreshAll = refreshAll
		syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
		err := r.sync(syncCtx, cfg)
		cancel()

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

func (r *runner) wait() { r.wg.Wait() }

type syncStatus struct {
	Running bool `json:"running"`
	// All marks a running refresh of the whole history.
	All bool `json:"all,omitempty"`
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
		sum := sha256.Sum256(data)
		tags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	tags[""] = tags["index.html"] // "/" serves index.html
	return tags
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
