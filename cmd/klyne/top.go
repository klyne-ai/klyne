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

// newTopCmd registers `klyne top`.
//
// Surfaces tool-usage rankings across sessions. Inspired by claudestat's
// "Top" tab but limited to deterministic, read-only aggregation over the
// klyne local DB.
func newTopCmd() *cobra.Command {
	var (
		projectPath string
		sinceStr    string
		limit       int
		outputJSON  bool
	)
	c := &cobra.Command{
		Use:   "top",
		Short: "Rank tools by usage across sessions",
		Long: `Rank tool invocations across all locally-ingested sessions.

Default output: a Markdown table sorted by call count, with error count and
the number of sessions each tool appears in.

Flags:
  --project=PATH       restrict to one project root
  --since=DURATION     only count tool calls newer than DURATION (e.g. 24h)
  --limit=N            max rows (default 20)
  --json               emit JSON instead of Markdown`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTop(cmd, projectPath, sinceStr, limit, outputJSON)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "restrict to one project root")
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only tool calls newer than this")
	c.Flags().IntVar(&limit, "limit", 20, "max rows to print")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON instead of Markdown")
	return c
}

func runTop(cmd *cobra.Command, projectPath, sinceStr string, limit int, outputJSON bool) error {
	db, err := store.Open(config.DBPath())
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
		ProjectPath: projectPath,
		SinceMs:     since,
		MaxSessions: 1000,
	})
	if err != nil {
		return err
	}
	tools := insights.AggregateTools(stats)
	if limit <= 0 {
		limit = 20
	}
	if len(tools) > limit {
		tools = tools[:limit]
	}

	if outputJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"generated_at": time.Now().UnixMilli(),
			"sessions":     len(stats),
			"tools":        tools,
		})
	}

	var b stringBuilder
	b.Writef("# klyne top — tool rankings\n\n")
	b.Writef("Across %d sessions.\n\n", len(stats))
	if len(tools) == 0 {
		b.Writef("_No tool calls found. Run `klyne start` and use a CLI to ingest sessions._\n")
		fmt.Fprint(cmd.OutOrStdout(), b.String())
		return nil
	}
	totalCalls := 0
	for _, t := range tools {
		totalCalls += t.Count
	}
	b.Writef("| Rank | Tool | Calls | Share | Errors | Sessions |\n")
	b.Writef("|---:|---|---:|---:|---:|---:|\n")
	for i, t := range tools {
		share := 0.0
		if totalCalls > 0 {
			share = float64(t.Count) / float64(totalCalls) * 100
		}
		b.Writef("| %d | %s | %d | %.1f%% | %d | %d |\n",
			i+1, t.Name, t.Count, share, t.ErrorCount, t.SessionCount)
	}
	fmt.Fprint(cmd.OutOrStdout(), b.String())
	return nil
}
