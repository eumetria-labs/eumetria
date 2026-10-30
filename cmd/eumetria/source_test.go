package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"eumetria/internal/off"
	"eumetria/internal/review"
	"eumetria/internal/stats"
	"eumetria/internal/store"
)

type fakeSource struct {
	name string
	err  error
	ran  *[]string
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Sync(ctx context.Context, h *store.History, w window) error {
	*f.ran = append(*f.ran, f.name)
	return f.err
}

func TestRunSources(t *testing.T) {
	var ran []string
	saves := 0
	srcs := []Source{
		fakeSource{"a", errors.New("login failed"), &ran},
		fakeSource{"b", nil, &ran},
	}
	failed, err := runSources(context.Background(), srcs, &store.History{}, window{}, func() error { saves++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "a,b" || saves != 2 {
		t.Errorf("ran %v with %d saves; want both sources and a save after each", ran, saves)
	}
	if failed == nil || !strings.Contains(failed.Error(), "a: login failed") {
		t.Errorf("failed = %v", failed)
	}

	// A failing save stops the run.
	ran = nil
	_, err = runSources(context.Background(), srcs, &store.History{}, window{}, func() error { return errors.New("disk full") })
	if err == nil || len(ran) != 1 {
		t.Errorf("err %v after %v; want a stop after the first save", err, ran)
	}
}

type fakeOFF struct {
	calls []string
	fail  string // barcode that returns a server error
}

func (f *fakeOFF) Product(ctx context.Context, ean string) (*store.OFFProduct, error) {
	f.calls = append(f.calls, ean)
	switch ean {
	case f.fail:
		return nil, errors.New("HTTP 503")
	case "404":
		return nil, off.ErrNotFound
	}
	return &store.OFFProduct{Name: "P" + ean, NutriScore: "b", NOVA: 3}, nil
}

func TestOFFSource(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	h := &store.History{
		Foods: map[string]*store.Food{
			"product:1": {EAN: "new"}, "product:2": {EAN: "old"}, "product:3": {EAN: "fresh"},
			"product:4": {EAN: "404"}, "product:5": {}, "product:6": {EAN: "new"}, // duplicate barcode
		},
		OFF: map[string]*store.OFFProduct{
			"old":   {Name: "stale", FetchedAt: now.Add(-40 * 24 * time.Hour)},
			"fresh": {Name: "kept", FetchedAt: now.Add(-24 * time.Hour)},
		},
	}
	f := &fakeOFF{}
	src := offSource{client: f, perRun: 10}
	if err := src.Sync(context.Background(), h, window{now: now}); err != nil {
		t.Fatal(err)
	}
	// New barcodes first, then the stale one; the fresh one is left alone.
	if strings.Join(f.calls, ",") != "404,new,old" {
		t.Errorf("looked up %v", f.calls)
	}
	if h.OFF["new"].NOVA != 3 || h.OFF["old"].Name != "Pold" || h.OFF["fresh"].Name != "kept" || !h.OFF["404"].NotFound || !h.OFF["404"].FetchedAt.Equal(now) {
		t.Errorf("cache = new %+v old %+v fresh %+v 404 %+v", h.OFF["new"], h.OFF["old"], h.OFF["fresh"], h.OFF["404"])
	}

	// The per-run limit, and an outage stops the run without failing it.
	h.OFF = nil
	f = &fakeOFF{fail: "new"}
	src = offSource{client: f, perRun: 2}
	if err := src.Sync(context.Background(), h, window{now: now}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, ",") != "404,fresh" || len(h.OFF) != 2 {
		t.Errorf("limited run looked up %v, cached %d", f.calls, len(h.OFF))
	}
	f = &fakeOFF{fail: "new"}
	h.OFF = nil
	src = offSource{client: f, perRun: 10}
	_ = src.Sync(context.Background(), h, window{now: now})
	if _, ok := h.OFF["new"]; ok || len(f.calls) != 3 {
		t.Errorf("after an outage: calls %v, cached new=%v", f.calls, ok)
	}
}

func TestOFFSourceMostEatenFirst(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	h := &store.History{
		Days: map[string]*store.Day{
			"2026-10-19": {Items: []store.Item{
				{Name: "Milk"}, // stored before barcodes: matched by name
				{Name: "Bowl", Unit: "portion", RecipeID: "r", RecipeVersion: "v"},
			}},
			"2026-10-18": {Items: []store.Item{{Name: "Milk"}}},
			"2026-01-01": {Items: []store.Item{{Name: "Old", EAN: "aaa"}, {Name: "Old", EAN: "aaa"}, {Name: "Old", EAN: "aaa"}}},
		},
		Foods: map[string]*store.Food{
			"product:m": {Name: "Milk", EAN: "zzz"}, "product:o": {Name: "Oats", EAN: "yyy"}, "product:x": {Name: "Old", EAN: "aaa"},
			store.RecipeKey("r", "v"): {Name: "Bowl", Unit: "portion", Ingredients: []store.Ingredient{{Name: "Oats", EAN: "yyy"}}},
		},
	}
	f := &fakeOFF{}
	if err := (offSource{client: f, perRun: 10}).Sync(context.Background(), h, window{now: now}); err != nil {
		t.Fatal(err)
	}
	// Milk twice, oats once (in the recipe), the old food not in the last 60 days.
	if strings.Join(f.calls, ",") != "zzz,yyy,aaa" {
		t.Errorf("looked up %v", f.calls)
	}
}

func TestReviewSource(t *testing.T) {
	today := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) // Wednesday
	h := &store.History{Days: map[string]*store.Day{
		"2026-10-14": {Date: "2026-10-14", Total: store.Macros{Energy: 1800}}, // last week
		"2026-10-20": {Date: "2026-10-20", Total: store.Macros{Energy: 1500}}, // this week
	}}
	var weeks []string
	fail := review.ErrBilling
	src := reviewSource{model: "m", effort: "medium", opts: stats.Options{}, write: func(ctx context.Context, model, effort string, w review.Week) (*review.Result, error) {
		weeks = append(weeks, w.From+".."+w.To)
		if fail != nil {
			return nil, fmt.Errorf("%w (HTTP 400)", fail)
		}
		return &review.Result{Text: "A good week.", Model: model, InputTokens: 100, OutputTokens: 10, CostUSD: 0.08}, nil
	}}
	win := window{today: today, now: today.Add(8 * time.Hour)}

	// The credit is used up: recorded, nothing stored, tried again next time.
	_ = src.Sync(context.Background(), h, win)
	if h.ReviewState == nil || !h.ReviewState.Billing || len(h.Reviews) != 0 {
		t.Fatalf("after a billing refusal: state %+v, reviews %d", h.ReviewState, len(h.Reviews))
	}
	fail = nil
	_ = src.Sync(context.Background(), h, win)
	_ = src.Sync(context.Background(), h, win) // already written: no second request
	if strings.Join(weeks, ",") != "2026-10-12..2026-10-18,2026-10-12..2026-10-18" {
		t.Errorf("requests for %v", weeks)
	}
	r := h.Reviews["2026-10-12"]
	if r == nil || r.Text != "A good week." || r.CostUSD != 0.08 || h.ReviewState != nil {
		t.Errorf("review %+v, state %+v", r, h.ReviewState)
	}

	// A week without data costs nothing.
	weeks = nil
	empty := &store.History{Days: map[string]*store.Day{"2026-10-20": {Date: "2026-10-20", Total: store.Macros{Energy: 1}}}}
	_ = src.Sync(context.Background(), empty, win)
	if len(weeks) != 0 {
		t.Errorf("asked for an empty week: %v", weeks)
	}
}
