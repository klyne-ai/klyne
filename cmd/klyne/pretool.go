package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/policy"
	"github.com/klyne-ai/klyne/internal/store"
)

// newPreToolCmd registers `klyne pretool`.
//
// This is the entry point for the Claude Code PreToolUse hook. On every
// tool call the hook spawns this subprocess which:
//  1. Reads the PreToolUse payload from stdin.
//  2. Matches the command against the risky-command policy.
//  3. When matched, takes a git stash (or cp-r fallback) snapshot.
//  4. Writes the snapshot row to the klyne SQLite store.
//  5. Emits a systemMessage JSON to stdout injecting restore instructions.
//
// In v0 the handler never blocks the tool call (snapshot-only mode).
func newPreToolCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pretool",
		Short: "Pre-action safety-net hook (PreToolUse handler)",
		Long: `Entry point for the Claude Code PreToolUse hook.

Reads the hook payload from stdin, matches the Bash command against the
risky-command policy, takes a snapshot when matched, and prints hook JSON
on stdout. In v0 the command never blocks the agent.

Stdin: JSON PreToolUse hook payload from Claude Code.
Stdout: hook JSON (systemMessage) or empty.
Exit code: always 0 (hook must not block on klyne errors).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPreTool(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return c
}

func runPreTool(cmd *cobra.Command) error {
	policyPath := os.Getenv("KLYNE_POLICY_PATH")
	if policyPath == "" {
		// Default: use the shipped policy from the binary's directory.
		// Falls back gracefully if not found (empty matcher).
		policyPath = defaultPolicyPath()
	}

	matcher, err := policy.LoadFile(policyPath)
	if err != nil {
		// Policy load failure is non-fatal: log and exit 0 (don't block).
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne pretool: load policy: %v\n", err)
		return nil
	}

	db, err := openPreToolDB()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne pretool: open db: %v\n", err)
		// Continue without DB — we still run the policy match and emit
		// a system message, just without persisting the snapshot row.
		db = nil
	}
	if db != nil {
		defer db.Close() //nolint:errcheck
	}

	ctx := cmd.Context()
	res, err := mcpserver.HandlePreToolUse(ctx, cmd.InOrStdin(), db, matcher)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne pretool: %v\n", err)
		return nil
	}
	if res.Output != "" {
		fmt.Fprintln(cmd.OutOrStdout(), res.Output)
	}
	return nil
}

// openPreToolDB opens the klyne SQLite store for the pretool handler.
// Returns (nil, err) on failure; callers must handle a nil DB gracefully.
func openPreToolDB() (*store.DB, error) {
	return store.Open(config.DBPath())
}

// defaultPolicyPath returns the path of the shipped risky_commands.json
// relative to the klyne binary's directory.
func defaultPolicyPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "policy/risky_commands.json"
	}
	// Try <binary-dir>/policy/risky_commands.json, the layout produced
	// by `go build` + `make install`.
	import_path := exe + "/../policy/risky_commands.json"
	_ = import_path
	// Most reliable: relative to CWD for development; binary-adjacent for prod.
	candidates := []string{
		exe[:len(exe)-len(policyBinBase(exe))] + "policy/risky_commands.json",
		"policy/risky_commands.json",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "policy/risky_commands.json"
}

// policyBinBase returns the base filename component of a path (no import of path/filepath
// needed since we already have one for the store).
func policyBinBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
