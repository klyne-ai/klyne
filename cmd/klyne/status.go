package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// `klyne status` — portable installation snapshot.
//
// Different from /klyne:handoff (per-task) and /klyne:bootstrap
// (per-session start). This is the per-installation "weekly review"
// view: what has klyne actually observed in the last N days?
//
// Defaults to a 7-day window for the current project's cwd. Pass
// --all-projects for a machine-wide view, or --since=Xh to widen
// / narrow the window.

func newStatusCmd() *cobra.Command {
	var (
		projectPath string
		allProjects bool
		since       string
		format      string
		asJSON      bool
		writePath   string
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Portable snapshot of klyne's installation state",
		Long: `Aggregate the last N hours (default 168 = 7 days) of klyne data
into a single Markdown snapshot: sessions ingested, total messages
and tokens, top projects by token volume, recent sessions, /compact
events, memory counts, and stop-hook session summaries.

Different from 'klyne handoff' (per-task) and 'klyne audit-sessions'
(integrity check). This is the per-installation weekly review.

Flags:
  --project=PATH         scope to one project (default: cwd)
  --all-projects         aggregate across every project on this machine
  --since=DURATION       window length (default 168h)
  --format=markdown|json output format (markdown is default)
  --write=PATH           also write output to a file

Examples:
  klyne status
  klyne status --since=24h --write=/tmp/today.md
  klyne status --all-projects --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd, projectPath, allProjects, since, format, asJSON, writePath)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "scope to one project (default: cwd)")
	c.Flags().BoolVar(&allProjects, "all-projects", false, "aggregate across every project")
	c.Flags().StringVar(&since, "since", "168h", "window length (Go duration like 24h, 7d=168h)")
	c.Flags().StringVar(&format, "format", "markdown", "output format (markdown | json)")
	c.Flags().BoolVar(&asJSON, "json", false, "shortcut for --format=json")
	c.Flags().StringVar(&writePath, "write", "", "also write output to this path")
	c.Flags().BoolVar(&allProjects, "markdown", false, "[deprecated alias for default behavior]")
	_ = c.Flags().MarkHidden("markdown")
	return c
}

func runStatus(cmd *cobra.Command, projectPath string, allProjects bool, sinceStr, format string, asJSON bool, writePath string) error {
	win := insights.DefaultStatusWindow()
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since %q: %w", sinceStr, err)
		}
		win.SinceMs = time.Now().Add(-d).UnixMilli()
	}
	if !allProjects {
		if projectPath == "" {
			w, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve cwd: %w", err)
			}
			projectPath = w
		}
		win.ProjectPath = projectPath
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db at %s: %w (run `klyne start` to ingest sessions)", config.DBPath(), err)
	}
	defer db.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	snap, err := insights.Snapshot(ctx, db, win)
	if err != nil {
		return err
	}

	if asJSON {
		format = "json"
	}
	var body string
	if format == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			return err
		}
	} else {
		body = insights.RenderStatusAsMarkdown(snap)
		fmt.Fprint(cmd.OutOrStdout(), body)
	}
	if writePath != "" {
		out := body
		if out == "" {
			// JSON was already written to stdout; re-marshal for the file.
			raw, err := json.MarshalIndent(snap, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal for --write: %w", err)
			}
			out = string(raw)
		}
		if err := os.WriteFile(writePath, []byte(out), 0o644); err != nil { //nolint:gosec
			return fmt.Errorf("write %s: %w", writePath, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote snapshot to %s\n", strings.TrimSpace(writePath))
	}
	return nil
}
