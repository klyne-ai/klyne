package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// newPatternsCmd registers `klyne patterns`.
//
// Surfaces deterministic inefficiency signals (tight loops, Bash overuse,
// low cache reuse) discovered in local sessions. Inspired by claudestat's
// Pattern Analyzer, faithful to klyne's "no AI in the core flow" rule.
func newPatternsCmd() *cobra.Command {
	var (
		projectPath string
		sinceStr    string
		kindFilter  string
		outputJSON  bool
		limit       int
	)
	c := &cobra.Command{
		Use:   "patterns",
		Short: "Detect inefficiency patterns across sessions (tight loops, bash overuse, low cache reuse)",
		Long: `Walk locally-ingested sessions and report deterministic inefficiency
signals. Three rules in v1:

  - tight_loop       : N+ consecutive same-tool calls
  - bash_overuse     : >= 40%% of tool calls are Bash
  - low_cache_reuse  : cached_read / tokens_in below the floor in a big session

Each finding includes severity (info | warn | alert), the offending session
id, and the measured metric vs. its threshold.

Flags:
  --project=PATH      restrict to one project
  --since=DURATION    only count sessions newer than DURATION
  --kind=NAME         filter by kind (tight_loop | bash_overuse | low_cache_reuse)
  --limit=N           cap the number of findings (default 50)
  --json              emit JSON instead of Markdown`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPatterns(cmd, projectPath, sinceStr, kindFilter, limit, outputJSON)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "restrict to one project")
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only sessions newer than this")
	c.Flags().StringVar(&kindFilter, "kind", "", "filter by kind (tight_loop | bash_overuse | low_cache_reuse)")
	c.Flags().IntVar(&limit, "limit", 50, "cap the number of findings")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON instead of Markdown")
	return c
}

func runPatterns(cmd *cobra.Command, projectPath, sinceStr, kindFilter string, limit int, outputJSON bool) error {
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
	all := insights.DetectPatterns(stats, insights.DefaultThresholds())

	if kindFilter != "" {
		out := make([]insights.Pattern, 0, len(all))
		for _, p := range all {
			if p.Kind == kindFilter {
				out = append(out, p)
			}
		}
		all = out
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	if outputJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"generated_at":    time.Now().UnixMilli(),
			"sessions_walked": len(stats),
			"patterns":        all,
		})
	}

	var b stringBuilder
	b.Writef("# klyne patterns\n\n")
	b.Writef("Walked %d sessions.\n\n", len(stats))
	if len(all) == 0 {
		b.Writef("_No inefficiency patterns matched. Either you are disciplined or sessions are tiny._\n")
		fmt.Fprint(cmd.OutOrStdout(), b.String())
		return nil
	}
	b.Writef("| Severity | Kind | Session | Detail | Metric / Threshold |\n")
	b.Writef("|---|---|---|---|---|\n")
	for _, p := range all {
		b.Writef("| %s | %s | `%s` | %s | %s |\n",
			strings.ToUpper(p.Severity), p.Kind, shortID(p.SessionID), p.Message, fmtMetric(p))
	}
	fmt.Fprint(cmd.OutOrStdout(), b.String())
	return nil
}

func fmtMetric(p insights.Pattern) string {
	switch p.Kind {
	case insights.KindTightLoop:
		return fmt.Sprintf("%.0f / %.0f", p.Metric, p.Threshold)
	case insights.KindBashOveruse, insights.KindLowCacheReuse:
		return fmt.Sprintf("%.0f%% / %.0f%%", p.Metric*100, p.Threshold*100)
	default:
		return fmt.Sprintf("%.2f / %.2f", p.Metric, p.Threshold)
	}
}
