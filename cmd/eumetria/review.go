package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"eumetria/internal/review"
	"eumetria/internal/stats"
	"eumetria/internal/store"
)

type writeFunc = func(context.Context, string, string, review.Week) (*review.Result, error)

// debugReview prints the AI review of the calendar week that contains date,
// written from the local history (no sync). With dryRun it prints the data
// that would be sent instead.
func debugReview(ctx context.Context, cfg config, date string, dryRun bool) error {
	day, err := time.ParseInLocation(time.DateOnly, date, cfg.zone())
	if err != nil {
		return fmt.Errorf("invalid -debug-week %q: %w", date, err)
	}
	hist, err := store.Load(cfg.historyPath)
	if err != nil {
		return fmt.Errorf("loading history: %w", err)
	}
	if len(hist.Days) == 0 {
		return fmt.Errorf("%s holds no days; run a sync first", cfg.historyPath)
	}
	now := cfg.now()
	// Everything the history holds: the review is printed here, not published.
	out := stats.Build(hist, stats.Options{}.Everything(), now)
	week := review.BuildWeek(out, review.Monday(day), now.Format(time.DateOnly))
	if len(week.Days) == 0 {
		return fmt.Errorf("the week of %s hasn't started yet", date)
	}

	if dryRun {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false) // as sent to Claude
		enc.SetIndent("", "  ")
		return enc.Encode(week)
	}
	if cfg.values.AnthropicKey == "" {
		return fmt.Errorf("no Anthropic API key in the settings (use -dry-run to see the data without it)")
	}
	model, effort := cfg.values.Model, cfg.values.Effort
	fmt.Fprintf(os.Stderr, "asking %s (effort %s) for the review of %s to %s…\n", model, effort, week.From, week.To)
	start := time.Now()
	r, err := review.Generate(ctx, cfg.values.AnthropicKey, model, effort, week)
	if err != nil {
		return err
	}
	fmt.Printf("# Week of %s to %s\n\n%s\n\n", week.From, week.To, r.Text)
	if r.TextDE != "" {
		fmt.Printf("# Woche vom %s bis %s\n\n%s\n\n", week.From, week.To, r.TextDE)
	}
	cost := "price unknown for this model"
	if r.KnownPrice {
		cost = fmt.Sprintf("about $%.4f", r.CostUSD)
	}
	fmt.Printf("---\n%s · %d input + %d output tokens (thinking included) · %s · %s\n",
		r.Model, r.InputTokens, r.OutputTokens, cost, time.Since(start).Round(time.Second))
	return nil
}

// rewriteReviews writes every stored weekly review again, in English and
// German, from the data the sync would send, and saves each one as soon as
// it is written, and tells progress about each. The server runs it between
// syncs, since both write the history file (Settings → Weekly review); as a
// command, stop the server first and its next sync publishes them. It returns
// the history as far as it got.
func rewriteReviews(ctx context.Context, cfg config, write writeFunc, progress func(done, total int)) (*store.History, error) {
	hist, err := store.Load(cfg.historyPath)
	if err != nil {
		return nil, fmt.Errorf("loading history: %w", err)
	}
	if len(hist.Reviews) == 0 {
		log.Printf("no weekly reviews stored yet; the next sync after a week ends writes the first")
		return hist, nil
	}
	weeks := make([]string, 0, len(hist.Reviews))
	for k := range hist.Reviews {
		weeks = append(weeks, k)
	}
	sort.Strings(weeks)
	now := cfg.now()
	today := now.Format(time.DateOnly)
	out := stats.Build(hist, baseOptions(cfg).Everything(), now)
	model, effort := cfg.values.Model, cfg.values.Effort
	var total float64
	progress(0, len(weeks))
	for i, key := range weeks {
		monday, err := time.ParseInLocation(time.DateOnly, key, now.Location())
		if err != nil {
			return hist, fmt.Errorf("stored review %q: %w", key, err)
		}
		log.Printf("asking %s (effort %s) for the week of %s", model, effort, key)
		r, err := write(ctx, model, effort, review.BuildWeek(out, monday, today))
		if err != nil {
			return hist, fmt.Errorf("week of %s: %w (the weeks before it are saved)", key, err)
		}
		// The cost adds up: the credit warning counts what was spent on the
		// first version, too.
		spent := hist.Reviews[key].CostUSD
		hist.Reviews[key] = &store.Review{
			Week: key, Text: r.Text, TextDE: r.TextDE, Model: r.Model, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CostUSD: spent + r.CostUSD, CreatedAt: now,
		}
		if err := hist.Save(cfg.historyPath); err != nil {
			return hist, err
		}
		german := "with German"
		if r.TextDE == "" {
			german = "WITHOUT German (no separator in the answer)"
		}
		log.Printf("week of %s: %s, %d+%d tokens, about $%.3f", key, german, r.InputTokens, r.OutputTokens, r.CostUSD)
		total += r.CostUSD
		progress(i+1, len(weeks))
	}
	log.Printf("%d reviews rewritten, about $%.3f in total", len(weeks), total)
	return hist, nil
}
