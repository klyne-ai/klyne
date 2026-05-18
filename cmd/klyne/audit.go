package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/audit"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// defaultAuditLimit caps how many transcripts the audit walks by default.
// Tunable via --limit. 20 is enough to surface systemic bugs without
// reading hundreds of multi-megabyte JSONL files.
const defaultAuditLimit = 20

// Discovery globs for the two CLIs. Defining them as named constants
// keeps the cobra subcommand free of hard-coded paths and matches the
// real on-disk layout (verified 2026-05-08).
const (
	claudeProjectsGlob = ".claude/projects/*/*.jsonl"
	codexSessionsGlob  = ".codex/sessions/**/*.jsonl"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit-sessions",
		Short: "Compare klyne's stored session metrics against ground truth in raw JSONL",
		Long: `audit-sessions walks the most recently modified Claude Code transcripts,
re-derives "ground truth" directly from the JSONL, and compares against
klyne's stored values. It is the trust foundation for every MCP tool:
no number klyne exposes is credible until this audit passes.

The v1 slice checks one thing: latest-assistant input_tokens accuracy.
That is the value that drives the session-page context-fill bar (and
the bug behind the original Opus 4.7 1M context display issue).

Output is Markdown, written to stdout and optionally to --out. The
klyne daemon does NOT need to be running.`,
		RunE: runAudit,
	}
	cmd.Flags().Int("limit", defaultAuditLimit,
		"maximum number of transcripts to audit (most recently modified first)")
	cmd.Flags().String("out", "",
		"write the Markdown report to this file in addition to stdout")
	return cmd
}

func runAudit(cmd *cobra.Command, _ []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	outPath, _ := cmd.Flags().GetString("out")

	paths, err := discoverClaudeTranscripts(limit)
	if err != nil {
		return fmt.Errorf("discover transcripts: %w", err)
	}
	if len(paths) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(),
			"# klyne audit report\n\nNo Claude Code transcripts found under ~/.claude/projects.")
		return nil
	}

	dbPath := config.DBPath()
	db, dbErr := store.Open(cmd.Context(), dbPath)
	if dbErr != nil {
		// We can still run the audit — every session will be reported
		// as not-in-DB, which is itself a signal worth surfacing.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: could not open klyne DB at %s: %v\n", dbPath, dbErr)
	}
	var lookup audit.LookupFunc = func(string) (int64, bool) { return 0, false }
	if db != nil {
		defer db.Close()
		lookup = newDBLookup(cmd.Context(), db)
	}

	report := audit.Run(paths, lookup)
	md := audit.RenderMarkdown(report)

	// Codex slice — discovery + audit + report. No DB comparison
	// (deferred): see audit.CodexReport docs for the limitation.
	codexPaths, codexErr := discoverCodexTranscripts(limit)
	if codexErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: codex transcript discovery failed: %v\n", codexErr)
	}
	codexReport := audit.RunCodex(codexPaths)
	md += audit.RenderCodexMarkdown(codexReport)

	fmt.Fprint(cmd.OutOrStdout(), md)
	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(md), 0o644); err != nil {
			return fmt.Errorf("write report to %s: %w", outPath, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "report written: %s\n", outPath)
	}

	// Non-zero exit when there are real mismatches — useful for CI.
	// Codex coverage gaps are NOT exit failures: the slice intentionally
	// reports without comparing, so there is nothing for CI to fail on.
	if report.Mismatches > 0 {
		return errors.New("audit found mismatches; see report for details")
	}
	return nil
}

// discoverClaudeTranscripts returns up to `limit` JSONL files under
// ~/.claude/projects, sorted by modification time (newest first). Subagent
// transcripts (in nested `subagents/` directories) are excluded — they
// don't drive a top-level session-page view and add noise.
func discoverClaudeTranscripts(limit int) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(home, claudeProjectsGlob))
	if err != nil {
		return nil, err
	}
	type entry struct {
		path string
		mod  time.Time
	}
	rows := make([]entry, 0, len(matches))
	for _, p := range matches {
		// Filter out subagent transcripts (path contains "/subagents/").
		// glob would have matched them only if the path layout were
		// exactly `<project>/<file>.jsonl`; defensive in case future
		// Claude versions move them.
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		rows = append(rows, entry{path: p, mod: info.ModTime()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mod.After(rows[j].mod) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.path)
	}
	return out, nil
}

// discoverCodexTranscripts returns up to `limit` Codex rollout JSONL
// files under ~/.codex/sessions, sorted by modification time (newest
// first). Codex stores rollouts in a year/month/day directory tree, so
// the discovery uses filepath.Walk to recurse instead of the simple
// glob the Claude path uses.
func discoverCodexTranscripts(limit int) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".codex", "sessions")
	type entry struct {
		path string
		mod  time.Time
	}
	rows := make([]entry, 0, 64)
	err = filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// Missing root or permission errors are non-fatal — caller
			// will see an empty Codex section in the report.
			if os.IsNotExist(walkErr) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".jsonl" {
			return nil
		}
		rows = append(rows, entry{path: p, mod: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mod.After(rows[j].mod) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.path)
	}
	return out, nil
}

// newDBLookup builds an audit.LookupFunc backed by the real klyne
// store. Mirrors session_usage.go's logic: walk the most-recent N
// messages newest-first, return the first assistant row with TokensIn>0.
func newDBLookup(ctx context.Context, db *store.DB) audit.LookupFunc {
	const scanLimit = 50
	return func(sessionID string) (int64, bool) {
		if sessionID == "" {
			return 0, false
		}
		// Confirm the session is actually present before claiming a
		// "not found" — a missing session (ingest gap) is reported
		// separately from a present-but-zero one.
		if _, err := store.GetSession(ctx, db, sessionID); err != nil {
			return 0, false
		}
		msgs, err := store.ListMessagesBySessionFiltered(
			ctx, db, sessionID, scanLimit, 0, "desc", store.MessageFilter{},
		)
		if err != nil {
			return 0, true // session exists; we just couldn't read messages — caller treats as 0/found
		}
		for _, m := range msgs {
			if m.Role == connectors.RoleAssistant && m.TokensIn > 0 {
				return m.TokensIn, true
			}
		}
		return 0, true
	}
}
