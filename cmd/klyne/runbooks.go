package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
)

// `klyne runbooks` — pattern → runbook proposal CLI.
//
// Surfaces recurring shell-command sequences as candidate runbooks.
// Three subcommands:
//
//   klyne runbooks           list candidates (alias: list)
//   klyne runbooks accept    save a candidate as a memory
//   klyne runbooks dismiss   never re-propose this candidate
//
// All read-only with respect to JSONL transcripts. The accept path
// writes one row to the decisions table; the dismiss path writes one
// row to runbook_dismissals. No AI calls.

func newRunbooksCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "runbooks",
		Short: "Surface recurring Bash command sequences in this project as candidate runbooks",
		Long: `Walk the user's recent Claude / Codex sessions for the current
project, normalise each Bash command (paths, UUIDs, IPs, timestamps
collapsed to placeholders), and index N-grams that recur across
multiple sessions. Each candidate is a workflow done at least 3
times across 2+ distinct sessions — strong evidence it'll happen
again.

Subcommands:
  klyne runbooks                list candidates (alias)
  klyne runbooks accept <id>    save the candidate as a memory
  klyne runbooks dismiss <id>   never propose this candidate again

Inspired by everything-claude-code's continuous-learning workflow,
adapted to klyne's contract: local-first, deterministic, no AI.`,
	}
	c.AddCommand(newRunbooksListCmd())
	c.AddCommand(newRunbooksAcceptCmd())
	c.AddCommand(newRunbooksDismissCmd())
	c.RunE = func(cmd *cobra.Command, args []string) error {
		// Default to list when invoked bare.
		return runRunbooksList(cmd, "", "", false)
	}
	c.Flags().String("project", "", "restrict to one project (default: cwd)")
	c.Flags().String("format", "markdown", "output format (markdown | json)")
	c.Flags().Bool("json", false, "shortcut for --format=json")
	return c
}

func newRunbooksListCmd() *cobra.Command {
	var project, format string
	var asJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List recurring command sequences as candidate runbooks (alias for bare `klyne runbooks`)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRunbooksList(cmd, project, format, asJSON)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&project, "project", "", "restrict to one project (default: cwd)")
	c.Flags().StringVar(&format, "format", "markdown", "output format (markdown | json)")
	c.Flags().BoolVar(&asJSON, "json", false, "shortcut for --format=json")
	return c
}

func newRunbooksAcceptCmd() *cobra.Command {
	var project, name string
	c := &cobra.Command{
		Use:   "accept <id-or-signature>",
		Short: "Accept a proposed runbook and save it as a project memory",
		Long: `Accept turns a candidate from 'klyne runbooks list' into a
memory row (decisions table) tagged "runbook" + "klyne-proposed".
Pass either the candidate id (12-char hex) or the full signature.

The memory body defaults to a numbered command list. Override the
auto-suggested name with --name; you can edit the memory later via
'klyne decisions list' (or call 'update_memory' through the MCP).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunbooksAccept(cmd, args[0], project, name)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&project, "project", "", "restrict to one project (default: cwd)")
	c.Flags().StringVar(&name, "name", "", "override auto-suggested runbook name")
	return c
}

func newRunbooksDismissCmd() *cobra.Command {
	var project, reason, scope string
	c := &cobra.Command{
		Use:   "dismiss <id-or-signature>",
		Short: "Stop proposing a candidate runbook in this project (or globally)",
		Long: `Dismiss adds the candidate's signature to a project-scoped
suppression list. 'klyne runbooks list' will never re-surface a
dismissed signature. Pass --scope=global to suppress everywhere
on this machine (use sparingly).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunbooksDismiss(cmd, args[0], project, scope, reason)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&project, "project", "", "restrict to one project (default: cwd)")
	c.Flags().StringVar(&scope, "scope", "project", "project | global")
	c.Flags().StringVar(&reason, "reason", "", "optional human-readable note")
	return c
}

func runRunbooksList(cmd *cobra.Command, projectPath, format string, asJSON bool) error {
	// Re-parse parent flags when invoked bare.
	if projectPath == "" {
		if v, err := cmd.Flags().GetString("project"); err == nil {
			projectPath = v
		}
	}
	if format == "" {
		format = "markdown"
		if v, err := cmd.Flags().GetString("format"); err == nil && v != "" {
			format = v
		}
	}
	if !asJSON {
		if v, err := cmd.Flags().GetBool("json"); err == nil && v {
			asJSON = true
		}
	}
	if asJSON {
		format = "json"
	}

	if projectPath == "" {
		w, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		projectPath = w
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

	cands, err := insights.ProposeRunbooks(ctx, db, projectPath, insights.DefaultRunbookConfig())
	if err != nil {
		return err
	}
	sessions, _ := insights.GatherSessions(ctx, db, insights.Filter{
		ProjectPath: projectPath,
		MaxSessions: 500,
	})

	switch format {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"project_path":    projectPath,
			"sessions_walked": len(sessions),
			"candidates":      cands,
		})
	default:
		body := renderRunbooksCLI(projectPath, cands, len(sessions))
		fmt.Fprint(cmd.OutOrStdout(), body)
		return nil
	}
}

// renderRunbooksCLI is the terminal-friendly rendering. Keeps each
// line under ~90 chars and indents step lists for readability.
func renderRunbooksCLI(projectPath string, cands []insights.Candidate, walked int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "klyne runbooks — proposals for %s\n", projectPath)
	fmt.Fprintf(&b, "Walked %d sessions.\n\n", walked)
	if len(cands) == 0 {
		b.WriteString("No recurring command sequences detected yet.\n")
		b.WriteString("klyne needs >=3 occurrences across >=2 sessions to propose.\n")
		return b.String()
	}
	for i, c := range cands {
		fmt.Fprintf(&b, "%d) %s  (id: %s)\n", i+1, c.SuggestedName, c.ID)
		fmt.Fprintf(&b, "   seen %d× in %d session(s)", c.Occurrences, c.DistinctSessions)
		if c.LastSeenMs > 0 {
			fmt.Fprintf(&b, " · last %s", time.UnixMilli(c.LastSeenMs).UTC().Format("2006-01-02"))
		}
		fmt.Fprintf(&b, " · score %.2f\n", c.Score)
		for j, cmd := range c.Commands {
			fmt.Fprintf(&b, "   %d. %s\n", j+1, cmd)
		}
		b.WriteString("\n")
	}
	b.WriteString("Accept:  klyne runbooks accept <id>\n")
	b.WriteString("Dismiss: klyne runbooks dismiss <id>\n")
	return b.String()
}

func runRunbooksAccept(cmd *cobra.Command, idOrSig, projectPath, name string) error {
	if projectPath == "" {
		w, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		projectPath = w
	}
	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	sig, err := resolveSignature(ctx, db, projectPath, idOrSig)
	if err != nil {
		return err
	}

	_, out, err := mcpserver.HandleAcceptRunbook(ctx, nil, mcpserver.AcceptRunbookInput{
		Signature:   sig,
		ProjectPath: projectPath,
		Name:        name,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Accepted runbook: %s\n", out.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Memory id: %s (project: %s)\n", out.MemoryID, out.ProjectPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Recall later with: klyne decisions list --project=%q\n", out.ProjectPath)
	return nil
}

func runRunbooksDismiss(cmd *cobra.Command, idOrSig, projectPath, scope, reason string) error {
	if projectPath == "" {
		w, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		projectPath = w
	}
	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	sig, err := resolveSignature(ctx, db, projectPath, idOrSig)
	if err != nil {
		return err
	}

	dismissalScope := mcpserver.MemoryScopeProject
	if strings.EqualFold(scope, "global") {
		dismissalScope = mcpserver.MemoryScopeGlobal
	}

	_, out, err := mcpserver.HandleDismissRunbook(ctx, nil, mcpserver.DismissRunbookInput{
		Signature:   sig,
		ProjectPath: projectPath,
		Scope:       dismissalScope,
		Reason:      reason,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Dismissed (%s scope): %s\n",
		out.Scope, insights.SignatureShortID(out.Signature))
	return nil
}

// resolveSignature accepts either a short id (12 hex chars) or the
// raw signature itself. When given a short id, walks the live
// candidate set for projectPath and finds the matching signature.
// Returns a friendly error when neither shape resolves.
func resolveSignature(ctx context.Context, db *store.DB, projectPath, idOrSig string) (string, error) {
	idOrSig = strings.TrimSpace(idOrSig)
	if idOrSig == "" {
		return "", errors.New("id or signature required")
	}
	// Heuristic: anything containing " ;; " is the raw signature.
	if strings.Contains(idOrSig, " ;; ") {
		return idOrSig, nil
	}
	// Walk the live candidate set to map id → signature.
	cfg := insights.DefaultRunbookConfig()
	cfg.MaxCandidates = 0 // unlimited
	cands, err := insights.ProposeRunbooks(ctx, db, projectPath, cfg)
	if err != nil {
		return "", err
	}
	for _, c := range cands {
		if c.ID == idOrSig {
			return c.Signature, nil
		}
	}
	return "", fmt.Errorf("no candidate with id %q in project %s (run `klyne runbooks list` to see fresh ids)",
		idOrSig, projectPath)
}
