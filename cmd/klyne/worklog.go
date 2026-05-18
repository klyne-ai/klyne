package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// newWorklogCmd is the umbrella for the cross-AI worklog surfaces.
// Subcommands grow over time: export-week is the first user-facing
// rendering surface (T12); reflection ops land in later tasks.
func newWorklogCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "worklog",
		Short: "Cross-AI worklog (memory + reflection) — surfaces, exports, and ops",
	}
	c.AddCommand(newWorklogExportWeekCmd())
	return c
}

// newWorklogExportWeekCmd writes a per-project Markdown digest for
// one ISO week. Quiet weeks (no visible entries) produce no file and
// print a single line so the command stays safe to fan out across a
// fleet of projects without polluting trees.
func newWorklogExportWeekCmd() *cobra.Command {
	var (
		project string
		week    string
		output  string
	)
	c := &cobra.Command{
		Use:   "export-week",
		Short: "Render a per-project Markdown digest for one ISO week (only if the week has entries)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				cwd, _ := os.Getwd()
				project = cwd
			}
			if week == "" {
				week = worklog.IsoWeek(time.Now())
			}
			if output == "" {
				output = project
			}
			db, err := store.Open(cmd.Context(), config.DBPath())
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer db.Close()
			res, err := worklog.ExportWeek(context.Background(), db, worklog.ExportArgs{
				ProjectPath: project, Week: week, OutputRoot: output,
			})
			if err != nil {
				return err
			}
			if res.FileWritten {
				fmt.Fprintln(cmd.OutOrStdout(), "Wrote", res.Path)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "No worklog entries for", project, "in", week, "— nothing to write.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&project, "project", "", "project path (default: cwd)")
	c.Flags().StringVar(&week, "week", "", "ISO week, e.g. 2026-W20 (default: this week)")
	c.Flags().StringVar(&output, "output", "", "output root (default: --project value)")
	return c
}
