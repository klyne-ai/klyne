package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/safety"
	"github.com/klyne-ai/klyne/internal/store"
)

// newRestoreCmd registers `klyne restore`.
//
// Subcommands:
//
//	klyne restore --list         — list all recorded snapshots
//	klyne restore <id>           — restore working tree to snapshot <id>
func newRestoreCmd() *cobra.Command {
	var list bool
	var limit int

	c := &cobra.Command{
		Use:   "restore [id]",
		Short: "List or restore a pre-action safety-net snapshot",
		Long: `List recorded safety-net snapshots or restore one by id.

klyne restore --list
  Prints all recorded snapshots in reverse chronological order.

klyne restore <id>
  Restores the working tree to the state captured in snapshot <id>.
  For git repos this runs: git stash apply <stash_sha>
  For non-git dirs this copies the backup directory back over the cwd.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if list || len(args) == 0 {
				return runRestoreList(cmd, limit)
			}
			return runRestoreApply(cmd, args[0])
		},
		SilenceUsage: true,
	}
	c.Flags().BoolVar(&list, "list", false, "list recorded snapshots")
	c.Flags().IntVar(&limit, "limit", 20, "max rows to show with --list")
	return c
}

func runRestoreList(cmd *cobra.Command, limit int) error {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	rows, err := store.ListSafetySnapshots(cmd.Context(), db, store.SafetySnapshotFilter{
		Limit: limit,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "klyne: no safety-net snapshots recorded yet.")
		return nil
	}
	renderSnapshotsTable(cmd.OutOrStdout(), rows)
	return nil
}

func runRestoreApply(cmd *cobra.Command, idStr string) error {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return fmt.Errorf("restore: id must be an integer, got %q", idStr)
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	snap, err := store.GetSafetySnapshot(cmd.Context(), db, id)
	if err != nil {
		return fmt.Errorf("snapshot %d not found: %w", id, err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	switch {
	case snap.StashSHA != "":
		fmt.Fprintf(cmd.OutOrStdout(), "klyne restore: applying git stash %s in %s…\n", snap.StashSHA, snap.CWD)
		if err := safety.RestoreGitStash(ctx, snap.CWD, snap.StashSHA); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "klyne restore: done — working tree restored to pre-`%s` state.\n", snap.Command)

	case snap.FallbackDir != "":
		fmt.Fprintf(cmd.OutOrStdout(), "klyne restore: copying backup from %s to %s…\n", snap.FallbackDir, snap.CWD)
		if err := safety.RestoreFallbackCopy(ctx, snap.CWD, snap.FallbackDir); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "klyne restore: done — files restored from backup.\n")

	default:
		fmt.Fprintf(cmd.OutOrStdout(),
			"klyne restore: snapshot %d has no restorable state (working tree was clean when the command ran).\n", id)
	}
	return nil
}

// renderSnapshotsTable prints a human-readable table of snapshot rows.
func renderSnapshotsTable(w interface{ Write([]byte) (int, error) }, rows []store.SafetySnapshot) {
	var b strings.Builder
	b.WriteString("# klyne safety-net snapshots\n\n")
	b.WriteString("| ID | When | Severity | Pattern | Command | Restore |\n|---|---|---|---|---|---|\n")
	for _, s := range rows {
		restore := "clean"
		if s.StashSHA != "" {
			restore = "git stash"
		} else if s.FallbackDir != "" {
			restore = "backup"
		}
		b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s |\n",
			s.ID, fmtSnapshotAgo(s.Ts), s.Severity, s.PatternID,
			truncate(s.Command, 50), restore))
	}
	b.WriteString(fmt.Sprintf("\nRun `klyne restore <id>` to restore a snapshot.\n"))
	_, _ = w.Write([]byte(b.String()))
}

func fmtSnapshotAgo(ms int64) string {
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
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
