package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// newRoastCmd registers `klyne roast`.
//
// A sardonic, deterministic, templated summary of your local session
// data. Inspired by claudestat's `roast` command but explicitly NO AI
// calls — every line is a templated string interpolated with real
// numbers from the local DB.
func newRoastCmd() *cobra.Command {
	var (
		sinceStr   string
		max        int
		outputJSON bool
	)
	c := &cobra.Command{
		Use:   "roast",
		Short: "Templated, deterministic insights with attitude. No AI calls.",
		Long: `Read your local klyne DB and emit a short list of templated
zingers about how you've been using Claude Code / Codex. Lines are
selected by deterministic rules over real numbers (spend, cache reuse,
tight loops, bash share) — no AI calls, no telemetry. Safe to pipe.

Flags:
  --since=DURATION    only consider sessions newer than DURATION (e.g. 168h)
  --max=N             cap lines (default 5)
  --json              emit JSON instead of plain text`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRoast(cmd, sinceStr, max, outputJSON)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only sessions newer than this")
	c.Flags().IntVar(&max, "max", 5, "cap the number of lines")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON instead of plain text")
	return c
}

func runRoast(cmd *cobra.Command, sinceStr string, max int, outputJSON bool) error {
	db, err := store.Open(cmd.Context(), config.DBPath())
	if err != nil {
		return fmt.Errorf("open db at %s: %w (run `klyne start` to ingest sessions)", config.DBPath(), err)
	}
	defer db.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	since := int64(0)
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since %q: %w", sinceStr, err)
		}
		since = time.Now().Add(-d).UnixMilli()
	}

	stats, err := insights.CollectStats(ctx, db, insights.Filter{
		SinceMs:     since,
		MaxSessions: 1000,
	})
	if err != nil {
		return err
	}
	tools := insights.AggregateTools(stats)
	in := insights.BuildRoastInput(stats, tools)
	roasts := insights.GenerateRoasts(in, max)

	if outputJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"generated_at": time.Now().UnixMilli(),
			"input":        in,
			"roasts":       roasts,
		})
	}

	// CostUSD on the per-session aggregate has been zero for flat-
	// subscription users since /cost/summary was repurposed to activity
	// rollups (see internal/api/handlers/cost.go). Hide the dollar amount
	// from the header when it would print "$0.00 total" — that line was
	// noise, not a number anyone wants to see.
	if in.TotalCostUSD > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "klyne roast — %d sessions, $%.2f total\n\n", in.SessionCount, in.TotalCostUSD)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "klyne roast — %d sessions\n\n", in.SessionCount)
	}
	for i, r := range roasts {
		fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n", i+1, r.Line)
	}
	return nil
}
