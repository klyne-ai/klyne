package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/fileheat"
	"github.com/klyne-ai/klyne/internal/store"
)

// newFilesCmd registers `klyne files`.
//
// Surfaces a per-file heatmap built by walking each session's tool
// calls and aggregating by file path. Inspired by token-dashboard's
// "hotspot" view (https://github.com/nateherkai/token-dashboard).
// Deterministic; no AI calls.
func newFilesCmd() *cobra.Command {
	var (
		projectPath string
		sinceStr    string
		limit       int
		outputJSON  bool
		mutatedOnly bool
	)
	c := &cobra.Command{
		Use:   "files",
		Short: "Per-file usage heatmap (which files do you touch the most?)",
		Long: `Rank files by how often Claude Code or Codex touched them. Walks
each session's tool-call inputs, extracts file paths, and aggregates.

Default output: a Markdown table sorted by total touches.

Flags:
  --project=PATH      restrict to one project root
  --since=DURATION    only include touches newer than DURATION (e.g. 24h)
  --limit=N           cap rows (default 20)
  --mutated-only      hide read-only files (Edit/Write/MultiEdit only)
  --json              emit JSON instead of Markdown`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFiles(cmd, projectPath, sinceStr, limit, outputJSON, mutatedOnly)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "restrict to one project root")
	c.Flags().StringVar(&sinceStr, "since", "", "Go duration; only touches newer than this")
	c.Flags().IntVar(&limit, "limit", 20, "max rows to print")
	c.Flags().BoolVar(&mutatedOnly, "mutated-only", false, "hide files that were only Read")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON instead of Markdown")
	return c
}

func runFiles(cmd *cobra.Command, projectPath, sinceStr string, limit int, outputJSON, mutatedOnly bool) error {
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

	stats, err := fileheat.Compute(ctx, db, fileheat.Filter{
		ProjectPath: projectPath,
		SinceMs:     since,
		MaxSessions: 1000,
	})
	if err != nil {
		return err
	}

	if mutatedOnly {
		filtered := stats[:0]
		for _, s := range stats {
			if s.Mutated() > 0 {
				filtered = append(filtered, s)
			}
		}
		stats = filtered
	}
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}

	if outputJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"generated_at": time.Now().UnixMilli(),
			"files":        stats,
		})
	}

	var b strings.Builder
	fmt.Fprintln(&b, "# klyne files — heatmap")
	fmt.Fprintln(&b)
	if len(stats) == 0 {
		fmt.Fprintln(&b, "_No file-bearing tool calls found. Run `klyne start` and use a CLI to ingest sessions._")
		fmt.Fprint(cmd.OutOrStdout(), b.String())
		return nil
	}
	fmt.Fprintln(&b, "| Rank | File | Reads | Edits | Writes | Sessions | Last touched |")
	fmt.Fprintln(&b, "|---:|---|---:|---:|---:|---:|---|")
	for i, f := range stats {
		fmt.Fprintf(&b, "| %d | %s | %d | %d | %d | %d | %s |\n",
			i+1, truncPath(f.Path, 60),
			f.Reads, f.Edits, f.Writes,
			f.SessionCount, fmtAgoMillis(f.LastTouched))
	}
	fmt.Fprint(cmd.OutOrStdout(), b.String())
	return nil
}

// truncPath keeps a path readable in terminal tables. Leaves the
// last-N chars intact and ellipsises the head when the path is too
// long.
func truncPath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	if max <= 3 {
		return p[len(p)-max:]
	}
	return "…" + p[len(p)-(max-1):]
}

// fmtAgoMillis renders "how long ago" for a UNIX-ms timestamp.
func fmtAgoMillis(ms int64) string {
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
