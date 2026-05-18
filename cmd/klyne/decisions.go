package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// newDecisionsCmd registers `klyne decisions` with sub-commands:
//
//	klyne decisions add "text"  [--tags=a,b] [--project=PATH] [--session=ID]
//	klyne decisions list        [--project=PATH] [--session=ID] [--tag=NAME] [--limit=N] [--json]
//	klyne decisions search "q"  [--project=PATH] [--json]
//	klyne decisions delete <id>
//
// Inspired by mcp-memory-keeper's persistent-context surface, kept
// minimal (one table, no compression) and aligned with klyne's
// "local-first / deterministic" rules.
func newDecisionsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "decisions",
		Short: "Record / list / search short decisions pinned to a project",
		Long: `Persist short, immutable notes ("we picked X over Y because…")
in klyne's local SQLite store. Inspired by mcp-memory-keeper but built
natively into klyne so there is no extra MCP server to install.

Subcommands:
  add     Write a new decision row.
  list    List recent decisions, optionally filtered.
  search  Substring search by text (case-insensitive).
  delete  Remove a decision by id.

The same surface is exposed as MCP tools (record_decision, list_decisions,
search_decisions) so the AI can pin and recall decisions mid-conversation.`,
	}
	c.AddCommand(newDecisionsAddCmd())
	c.AddCommand(newDecisionsListCmd())
	c.AddCommand(newDecisionsSearchCmd())
	c.AddCommand(newDecisionsDeleteCmd())
	return c
}

func newDecisionsAddCmd() *cobra.Command {
	var (
		tags        string
		projectPath string
		sessionID   string
	)
	c := &cobra.Command{
		Use:   "add \"text\"",
		Short: "Record a new decision",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				return errors.New("decision text required")
			}
			if projectPath == "" {
				projectPath, _ = os.Getwd()
			}
			tagList := splitTags(tags)
			d := &store.Decision{
				ID:          newDecisionID(),
				Ts:          time.Now().UnixMilli(),
				ProjectPath: projectPath,
				SessionID:   sessionID,
				Text:        text,
				Tags:        tagList,
			}
			db, err := store.Open(cmd.Context(), config.DBPath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()
			if err := store.InsertDecision(cmd.Context(), db, d); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded: %s\n", d.ID)
			return nil
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&tags, "tags", "", "comma-separated tags (e.g. db,infra)")
	c.Flags().StringVar(&projectPath, "project", "", "project path (default: cwd)")
	c.Flags().StringVar(&sessionID, "session", "", "session id (default: empty)")
	return c
}

func newDecisionsListCmd() *cobra.Command {
	var (
		projectPath string
		sessionID   string
		tag         string
		limit       int
		outputJSON  bool
		allProjects bool
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List recent decisions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !allProjects && projectPath == "" {
				projectPath, _ = os.Getwd()
			}
			if allProjects {
				projectPath = ""
			}
			db, err := store.Open(cmd.Context(), config.DBPath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()
			rows, err := store.ListDecisions(cmd.Context(), db, store.DecisionFilter{
				ProjectPath: projectPath,
				SessionID:   sessionID,
				Tag:         tag,
				Limit:       limit,
			})
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			renderDecisionsTable(cmd.OutOrStdout(), rows)
			return nil
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "project path (default: cwd)")
	c.Flags().StringVar(&sessionID, "session", "", "filter by session id")
	c.Flags().StringVar(&tag, "tag", "", "filter by tag")
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON")
	c.Flags().BoolVar(&allProjects, "all", false, "include decisions from every project")
	return c
}

func newDecisionsSearchCmd() *cobra.Command {
	var (
		projectPath string
		limit       int
		outputJSON  bool
		allProjects bool
	)
	c := &cobra.Command{
		Use:   "search \"query\"",
		Short: "Search decisions by text (case-insensitive substring)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := strings.TrimSpace(strings.Join(args, " "))
			if q == "" {
				return errors.New("query required")
			}
			if !allProjects && projectPath == "" {
				projectPath, _ = os.Getwd()
			}
			if allProjects {
				projectPath = ""
			}
			db, err := store.Open(cmd.Context(), config.DBPath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()
			rows, err := store.SearchDecisions(cmd.Context(), db, q, projectPath, limit)
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			renderDecisionsTable(cmd.OutOrStdout(), rows)
			return nil
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&projectPath, "project", "", "project path (default: cwd)")
	c.Flags().IntVar(&limit, "limit", 50, "max rows")
	c.Flags().BoolVar(&outputJSON, "json", false, "emit JSON")
	c.Flags().BoolVar(&allProjects, "all", false, "search across every project")
	return c
}

func newDecisionsDeleteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a decision by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(cmd.Context(), config.DBPath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()
			if err := store.DeleteDecision(cmd.Context(), db, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted: %s\n", args[0])
			return nil
		},
		SilenceUsage: true,
	}
	return c
}

// --- helpers -------------------------------------------------------

func splitTags(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// newDecisionID returns a 16-hex-char identifier — short enough for
// terminals, long enough that collisions are astronomically unlikely.
// Falls back to a timestamp suffix if crypto/rand is unavailable.
func newDecisionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("d-%d", time.Now().UnixNano())
	}
	return "d-" + hex.EncodeToString(b[:])
}

func renderDecisionsTable(w interface{ Write([]byte) (int, error) }, rows []store.Decision) {
	var b strings.Builder
	b.WriteString("# klyne decisions\n\n")
	if len(rows) == 0 {
		b.WriteString("_No decisions recorded yet. Use `klyne decisions add \"…\"` or the `record_decision` MCP tool._\n")
		_, _ = w.Write([]byte(b.String()))
		return
	}
	b.WriteString("| When | ID | Project | Tags | Text |\n|---|---|---|---|---|\n")
	for _, d := range rows {
		tags := strings.Join(d.Tags, ", ")
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s | %s | %s |\n",
			fmtAgo(d.Ts), d.ID, shortProj(d.ProjectPath), tags, escapeCell(d.Text)))
	}
	_, _ = w.Write([]byte(b.String()))
}

func fmtAgo(ms int64) string {
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

func shortProj(p string) string {
	if p == "" {
		return "(global)"
	}
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// escapeCell replaces pipes and newlines so a long decision text doesn't
// break the markdown table layout.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}
