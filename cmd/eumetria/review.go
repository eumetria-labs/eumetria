package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"eumetria/internal/review"
	"eumetria/internal/stats"
	"eumetria/internal/store"
)

// debugReview prints the AI review of the calendar week that contains date,
// written from the local history (no sync). With dryRun it prints the data
// that would be sent instead.
func debugReview(ctx context.Context, cfg config, date string, dryRun bool) error {
	day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
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
	now := time.Now()
	// Everything the history holds: the review is printed here, not published.
	out := stats.Build(hist, stats.Options{PublishFoods: true, PublishHealth: true}, now)
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
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is not set (use -dry-run to see the data without it)")
	}
	model, effort := env("AI_MODEL", review.DefaultModel), env("AI_EFFORT", "medium")
	fmt.Fprintf(os.Stderr, "asking %s (effort %s) for the review of %s to %s…\n", model, effort, week.From, week.To)
	start := time.Now()
	r, err := review.Generate(ctx, model, effort, week)
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
// it is written. The server's next sync publishes them. Run it while the
// server is stopped: both write the history file.
func rewriteReviews(ctx context.Context, cfg config) error {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	hist, err := store.Load(cfg.historyPath)
	if err != nil {
		return fmt.Errorf("loading history: %w", err)
	}
	if len(hist.Reviews) == 0 {
		fmt.Println("no weekly reviews stored yet; the next sync after a week ends writes the first")
		return nil
	}
	weeks := make([]string, 0, len(hist.Reviews))
	for k := range hist.Reviews {
		weeks = append(weeks, k)
	}
	sort.Strings(weeks)
	now := time.Now()
	today := now.Format(time.DateOnly)
	out := stats.Build(hist, syncOptions(cfg), now)
	model, effort := env("AI_MODEL", review.DefaultModel), env("AI_EFFORT", "medium")
	var total float64
	for _, key := range weeks {
		monday, err := time.ParseInLocation(time.DateOnly, key, time.Local)
		if err != nil {
			return fmt.Errorf("stored review %q: %w", key, err)
		}
		fmt.Fprintf(os.Stderr, "asking %s (effort %s) for the week of %s…\n", model, effort, key)
		r, err := review.Generate(ctx, model, effort, review.BuildWeek(out, monday, today))
		if err != nil {
			return fmt.Errorf("week of %s: %w (the weeks before it are saved)", key, err)
		}
		// The cost adds up: the credit warning counts what was spent on the
		// first version, too.
		spent := hist.Reviews[key].CostUSD
		hist.Reviews[key] = &store.Review{
			Week: key, Text: r.Text, TextDE: r.TextDE, Model: r.Model, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CostUSD: spent + r.CostUSD, CreatedAt: now,
		}
		if err := hist.Save(cfg.historyPath); err != nil {
			return err
		}
		german := "with German"
		if r.TextDE == "" {
			german = "WITHOUT German (no separator in the answer)"
		}
		fmt.Printf("week of %s: %s, %d+%d tokens, about $%.3f\n", key, german, r.InputTokens, r.OutputTokens, r.CostUSD)
		total += r.CostUSD
	}
	fmt.Printf("%d reviews rewritten, about $%.3f in total; start the server to publish them\n", len(weeks), total)
	return nil
}
