package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
)

// newPreCompactCmd registers `klyne precompact`.
//
// This is the entry point for the Claude Code PreCompact hook. On every
// auto-compact event the hook spawns this subprocess which:
//  1. Reads the PreCompact hook payload from stdin.
//  2. Computes context fill from the payload's context_window.
//  3. At fill ≥ 92%: allows native compact (graceful fold).
//  4. At fill in [70%, 92%) with a snapshot present: emits block JSON.
//  5. Otherwise: emits nothing (native compact proceeds).
//
// Pass --status to print the last decision counts instead of running
// the hook handler.
func newPreCompactCmd() *cobra.Command {
	var statusFlag bool

	c := &cobra.Command{
		Use:   "precompact",
		Short: "Compact Shield hook (PreCompact handler)",
		Long: `Entry point for the Claude Code PreCompact hook.

Reads the hook payload from stdin, checks context fill against the arming
band (70–92%) and a snapshot, then emits block JSON or nothing.

At fill ≥ 92% klyne gracefully folds: native compact runs undisturbed.
At fill in [70%, 92%) with a snapshot: klyne blocks and emits
  {"decision":"block","reason":"klyne shielded compact (snapshot=N)"}

Stdin:   JSON PreCompact hook payload from Claude Code.
Stdout:  hook JSON (block) or empty (allow).
Exit code: always 0 (hook must not block on klyne errors).

Pass --status to print recent decision counts instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if statusFlag {
				return runPreCompactStatus(cmd)
			}
			return runPreCompact(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.Flags().BoolVar(&statusFlag, "status", false, "print last compact-shield decision counts and exit")
	return c
}

func runPreCompact(cmd *cobra.Command) error {
	db, err := openPreCompactDB()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne precompact: open db: %v\n", err)
		// Continue without DB — handler degrades gracefully (always allows).
		db = nil
	}
	if db != nil {
		defer db.Close() //nolint:errcheck
	}

	ctx := cmd.Context()
	res, err := mcpserver.HandlePreCompact(ctx, cmd.InOrStdin(), db)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne precompact: %v\n", err)
		return nil
	}
	if res.Output != "" {
		fmt.Fprintln(cmd.OutOrStdout(), res.Output)
	}
	return nil
}

func runPreCompactStatus(cmd *cobra.Command) error {
	db, err := openPreCompactDB()
	if err != nil {
		return fmt.Errorf("klyne precompact: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := cmd.Context()
	total, blocked, folded, err := store.CountShieldDecisions(ctx, db, "")
	if err != nil {
		return fmt.Errorf("klyne precompact: count decisions: %w", err)
	}
	allowed := total - blocked - folded
	fmt.Fprintf(cmd.OutOrStdout(), "compact-shield status\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  total decisions : %d\n", total)
	fmt.Fprintf(cmd.OutOrStdout(), "  blocked (shield): %d\n", blocked)
	fmt.Fprintf(cmd.OutOrStdout(), "  folded  (≥92%%): %d\n", folded)
	fmt.Fprintf(cmd.OutOrStdout(), "  allowed         : %d\n", allowed)
	return nil
}

// openPreCompactDB opens the klyne SQLite store for the precompact handler.
// Returns (nil, err) on failure; callers must handle a nil DB gracefully.
func openPreCompactDB() (*store.DB, error) {
	return store.Open(config.DBPath())
}

// preCompactBinBase returns the base filename component of the binary path.
// Named distinctly from policyBinBase to avoid package-level name collisions.
func preCompactBinBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// Silence "declared but not used" — preCompactBinBase is available for future
// use matching policyBinBase's pattern.
var _ = preCompactBinBase

// shieldExe resolves the binary path for hook installation.
func shieldExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "klyne"
	}
	return exe
}

// Silence unused — shieldExe is the canonical resolver for the install path.
var _ = shieldExe
