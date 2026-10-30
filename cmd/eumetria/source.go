package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"eumetria/internal/off"
	"eumetria/internal/review"
	"eumetria/internal/stats"
	"eumetria/internal/store"
)

// A Source brings the data of one service into the history. syncOnce runs
// the sources in order, saving the history after each, and publishes the
// page even when one fails. Each source decides what a failure means: Sync
// returns an error only when the run should count as failed.
type Source interface {
	Name() string
	Sync(ctx context.Context, h *store.History, w window) error
}

// runSources syncs every source in turn and saves the history after each,
// also after a failure, so the next run only retries what's missing. A
// failing source doesn't stop the others; failed joins their errors. err is
// set only when saving fails.
func runSources(ctx context.Context, srcs []Source, h *store.History, w window, save func() error) (failed, err error) {
	var errs []error
	for _, src := range srcs {
		if err := src.Sync(ctx, h, w); err != nil {
			log.Printf("%s: %v", src.Name(), err)
			errs = append(errs, fmt.Errorf("%s: %w", src.Name(), err))
		}
		if err := save(); err != nil {
			return errors.Join(errs...), err
		}
	}
	return errors.Join(errs...), nil
}

// window is the part of the timeline a sync covers.
type window struct {
	start, today time.Time // first day to backfill, and today
	now          time.Time
	refreshDays  int  // always re-fetch this many recent days
	refreshAll   bool // re-fetch every day in the window
	workers      int  // parallel requests
}

// sources lists the services to sync, in order: Google Health starts at the
// first day logged in Yazio, so Yazio goes first.
func sources(cfg config, opts stats.Options) []Source {
	srcs := []Source{
		yazioSource{photoDir: cfg.photoDir},
		googleSource{tokenPath: cfg.googleToken},
	}
	// Open Food Facts only sees barcodes; OPENFOODFACTS=false keeps even
	// those on the server.
	if envBool("OPENFOODFACTS", true) {
		// Open Food Facts allows 15 product reads a minute per IP and may ban
		// above that: 12 a minute, 30 per sync (2.5 minutes).
		srcs = append(srcs, offSource{client: off.New(), perRun: 30, pause: 5 * time.Second})
	}
	// Last: the weekly review reads what the others brought in.
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		srcs = append(srcs, reviewSource{
			opts: opts, model: env("AI_MODEL", review.DefaultModel), effort: env("AI_EFFORT", "medium"),
			write: review.Generate,
		})
	}
	return srcs
}

// yazioSource syncs the food diary, goals, weight, steps, water and
// activities, and the meal and recipe photos.
type yazioSource struct {
	photoDir string
}

func (yazioSource) Name() string { return "yazio" }

func (y yazioSource) Sync(ctx context.Context, h *store.History, w window) error {
	client, err := login(ctx)
	if err != nil {
		return err
	}
	s := &syncer{client: client, hist: h, photoDir: y.photoDir}
	days := daysToFetch(h, w.start, w.today, w.refreshDays, w.refreshAll)
	log.Printf("yazio: fetching %d days (%d already in history)", len(days), len(h.Days))
	failed := s.run(ctx, days, w.workers)
	s.upgradeProducts(ctx)
	s.upgradeRecipes(ctx)
	log.Printf("yazio: %d days fetched, %d failed, %d days in history", len(days)-failed, failed, len(h.Days))
	if len(days) > 0 && failed == len(days) {
		return errors.New("every request failed; is the API base URL still valid?")
	}
	return nil
}

// googleSource syncs Fitbit / Pixel Watch data through the Google Health
// API, when it is set up. It never fails the run: a problem is recorded
// for the page's warning, and everything else is published either way.
type googleSource struct {
	tokenPath string
}

func (googleSource) Name() string { return "google health" }

func (g googleSource) Sync(ctx context.Context, h *store.History, w window) error {
	gc, err := googleClient(g.tokenPath)
	if gc == nil && err == nil {
		return nil // not set up
	}
	if err == nil {
		// Fitbit data before the first Yazio day is never shown, so it is
		// not fetched either.
		from := healthWindow(h, firstLogged(h, w.start), w.today, w.refreshDays, w.refreshAll)
		err = syncHealth(ctx, gc, h, from, w.today)
	}
	if err != nil {
		log.Printf("WARNING: google health: %v", err)
	}
	recordHealthSync(h, err, w.now)
	return nil
}

// offSource adds Open Food Facts data (Nutri-Score, NOVA, additives) to the
// products Yazio knows a barcode for. It never fails the run.
type offSource struct {
	client interface {
		Product(ctx context.Context, ean string) (*store.OFFProduct, error)
	}
	perRun int           // lookups per sync, so a first run doesn't flood the API
	pause  time.Duration // between lookups: Open Food Facts allows 15 a minute
}

// recentUses counts how often each barcode was eaten in the last days,
// recipe ingredients included. Entries stored before barcodes were recorded
// are matched by product name.
func recentUses(h *store.History, now time.Time, days int) map[string]int {
	byName := map[string]string{}
	for _, f := range h.Foods {
		if f.EAN != "" && f.Unit != "portion" {
			byName[f.Name] = f.EAN
		}
	}
	ean := func(code, name string) string {
		if code != "" {
			return code
		}
		return byName[name]
	}
	from := now.AddDate(0, 0, -days).Format(time.DateOnly)
	uses := map[string]int{}
	for date, d := range h.Days {
		if date < from {
			continue
		}
		for _, it := range d.Items {
			if e := ean(it.EAN, it.Name); e != "" {
				uses[e]++
			}
			if it.RecipeID == "" {
				continue
			}
			r := h.Foods[store.RecipeKey(it.RecipeID, it.RecipeVersion)]
			if r == nil {
				r = h.Foods["recipe:"+it.RecipeID]
			}
			if r == nil {
				continue
			}
			for _, in := range r.Ingredients {
				if e := ean(in.EAN, in.Name); e != "" {
					uses[e]++
				}
			}
		}
	}
	return uses
}

// offRefresh is how long a lookup is kept before it is checked again (the
// data, and whether a product is known at all, changes over time).
const offRefresh = 30 * 24 * time.Hour

func (offSource) Name() string { return "open food facts" }

func (o offSource) Sync(ctx context.Context, h *store.History, w window) error {
	if h.OFF == nil {
		h.OFF = map[string]*store.OFFProduct{}
	}
	// Barcodes never looked up come first, the most eaten of them first;
	// then the oldest lookups.
	uses := recentUses(h, w.now, 60)
	var due []string
	seen := map[string]bool{}
	for _, f := range h.Foods {
		if f.EAN == "" || seen[f.EAN] {
			continue
		}
		seen[f.EAN] = true
		if p := h.OFF[f.EAN]; p == nil || w.now.Sub(p.FetchedAt) > offRefresh {
			due = append(due, f.EAN)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := h.OFF[due[i]], h.OFF[due[j]]
		if (a == nil) != (b == nil) {
			return a == nil
		}
		if a == nil && uses[due[i]] != uses[due[j]] {
			return uses[due[i]] > uses[due[j]]
		}
		if a != nil && !a.FetchedAt.Equal(b.FetchedAt) {
			return a.FetchedAt.Before(b.FetchedAt)
		}
		return due[i] < due[j]
	})
	waiting := len(due)
	if len(due) > o.perRun {
		due = due[:o.perRun]
	}
	if len(due) > 0 {
		// The lookups are slow on purpose (rate limit): say so up front.
		log.Printf("open food facts: looking up %d of %d due barcodes (about %s)", len(due), waiting,
			(time.Duration(len(due)-1) * o.pause).Round(time.Second))
	}
	found := 0
	for i, ean := range due {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(o.pause):
			}
		}
		p, err := o.client.Product(ctx, ean)
		switch {
		case errors.Is(err, off.ErrNotFound):
			p = &store.OFFProduct{NotFound: true}
		case err != nil:
			// Down or rate-limited (429/503): try again next sync.
			log.Printf("open food facts: %v; stopping until the next sync", err)
			return nil
		default:
			found++
		}
		p.FetchedAt = w.now
		h.OFF[ean] = p
	}
	if len(due) > 0 {
		log.Printf("open food facts: looked up %d barcodes, %d known", len(due), found)
	}
	return nil
}

// reviewSource has Claude write the review of the last completed calendar
// week, once: a review is never written again, so each costs only once. It
// sees all of the data (opts) and never fails the run; a failure is
// recorded and tried again on the next sync (refused requests cost nothing).
type reviewSource struct {
	opts          stats.Options
	model, effort string
	write         func(ctx context.Context, model, effort string, w review.Week) (*review.Result, error)
}

func (reviewSource) Name() string { return "weekly review" }

func (s reviewSource) Sync(ctx context.Context, h *store.History, w window) error {
	monday := review.Monday(w.today).AddDate(0, 0, -7)
	key := monday.Format(time.DateOnly)
	if h.Reviews[key] != nil {
		return nil
	}
	out := stats.Build(h, s.opts.Everything(), w.now)
	week := review.BuildWeek(out, monday, w.today.Format(time.DateOnly))
	if week.Summary.DaysLogged == 0 && week.Summary.Workouts == 0 {
		return nil // nothing to write about
	}
	log.Printf("weekly review: asking %s to write the week of %s", s.model, key)
	r, err := s.write(ctx, s.model, s.effort, week)
	if err != nil {
		h.ReviewState = &store.ReviewState{LastAttempt: w.now, Error: err.Error(), Billing: errors.Is(err, review.ErrBilling)}
		log.Printf("WARNING: weekly review of %s: %v", key, err)
		return nil
	}
	if h.Reviews == nil {
		h.Reviews = map[string]*store.Review{}
	}
	h.Reviews[key] = &store.Review{
		Week: key, Text: r.Text, TextDE: r.TextDE, Model: r.Model, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		CostUSD: r.CostUSD, CreatedAt: w.now,
	}
	h.ReviewState = nil
	log.Printf("weekly review of %s written by %s: %d+%d tokens, about $%.3f", key, r.Model, r.InputTokens, r.OutputTokens, r.CostUSD)
	return nil
}
