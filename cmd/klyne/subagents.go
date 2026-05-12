package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/subagent"
)

// newSubagentsCmd registers `klyne subagents`.
//
// Walks ~/.claude/projects/<project>/<parent-session>/subagents/*.jsonl,
// parses each, and rolls up token usage per parent session. Surfaces the
// hidden cost of Task-tool subagents that the parent session's
// `sessions.cost_usd` column doesn't include. Inspired by
// token-dashboard's subagent-attribution feature.
//
// Read-only, no AI calls, no network. Operates on disk directly so it
// works even before the daemon has ingested anything.
func newSubagentsCmd() *cobra.Command {
	var (
		sinceStr   string
		limit      int
		outputJSON bool
	)
	c := &cobra.Command{
		Use:   "subagents",
		Short: "Roll up Task-tool subagent activity per parent session",
		Long: `Discover every subagent JSONL klyne can see (Task tool transcripts
written under ~/.claude/projects/<project>/<session>/subagents/) and
aggregate token usage back to the parent session.

The parent session's headline cost number does NOT include subagent
spend, because Claude Code writes those transcripts to a sibling JSONL
that the cost engine doesn't roll into the parent. This command makes
that hidden cost visible.

Flags:
  --since=DURATION    only include files modified after DURATION ago
  --limit=N           cap rows (default 20)
  --json              emit JSON instead of Markdown`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSubagents(cmd, sinceStr, limit, outputJSON)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only files newer than this")
	c.Flags().IntVar(&limit, "limit", 20, "max rows")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON instead of Markdown")
	return c
}

func runSubagents(cmd *cobra.Command, sinceStr string, limit int, outputJSON bool) error {
	since := int64(0)
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since %q: %w", sinceStr, err)
		}
		since = time.Now().Add(-d).UnixMilli()
	}
	rollups, err := subagent.CollectAll(since)
	if err != nil {
		return err
	}
	if limit > 0 && len(rollups) > limit {
		rollups = rollups[:limit]
	}

	if outputJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"generated_at": time.Now().UnixMilli(),
			"rollups":      rollups,
		})
	}

	var b strings.Builder
	b.WriteString("# klyne subagents — Task-tool attribution\n\n")
	if len(rollups) == 0 {
		b.WriteString("_No subagent transcripts found under ~/.claude/projects/*/*/subagents/._\n")
		fmt.Fprint(cmd.OutOrStdout(), b.String())
		return nil
	}
	var totalIn, totalOut, totalCacheRead int64
	var totalAgents int
	for _, r := range rollups {
		totalIn += r.TokensIn
		totalOut += r.TokensOut
		totalCacheRead += r.CachedRead
		totalAgents += r.SubagentCount
	}
	cachePct := 0
	if totalIn > 0 {
		cachePct = int(float64(totalCacheRead) / float64(totalIn) * 100)
	}
	fmt.Fprintf(&b, "**Totals:** %s subagents across %d parent sessions · %s tokens in (%d%% cached) · %s tokens out\n\n",
		fmtInt(totalAgents), len(rollups), fmtTokens(totalIn), cachePct, fmtTokens(totalOut))
	b.WriteString("| Parent session | Project | Subagents | Tokens in | Tokens out | Cache % | Last activity |\n")
	b.WriteString("|---|---|---:|---:|---:|---:|---|\n")
	for _, r := range rollups {
		cache := 0
		if r.TokensIn > 0 {
			cache = int(float64(r.CachedRead) / float64(r.TokensIn) * 100)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d | %s | %s | %d%% | %s |\n",
			truncSession(r.ParentSessionID), shortProject(r.ProjectPath),
			r.SubagentCount, fmtTokens(r.TokensIn), fmtTokens(r.TokensOut),
			cache, fmtAgoMs(r.LastActivityMs))
	}
	fmt.Fprint(cmd.OutOrStdout(), b.String())
	return nil
}

// --- helpers ---------------------------------------------------------

func truncSession(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func shortProject(p string) string {
	if p == "" {
		return "—"
	}
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func fmtInt(n int) string {
	return fmt.Sprintf("%d", n)
}

// fmtTokens renders a token count with a k / M suffix once it crosses
// the threshold. Mirrors humanTokensShort but with one-decimal precision
// for slightly larger context.
func fmtTokens(n int64) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtAgoMs(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}
