package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// proposeReflectionEntryBody caps how much per-entry context the proposer
// surfaces in the markdown render. The AI host already gets the full
// AI-drafted summary and last-user line via the structured `entries`
// field; the markdown is the at-a-glance brief.
const proposeReflectionEntryBody = 200

// ProposeReflectionInput is the MCP-facing input schema.
//
// Day scopes the proposed slice to a single local calendar day in the
// iterative-reflection workflow (docs/features/iterative-reflection.md):
// the proposer fetches only stop_summaries inside [day-start, day-end)
// AND with ts after the day's reflection cursor. Omitted ⇒ today.
type ProposeReflectionInput struct {
	ProjectPath string `json:"project_path" jsonschema:"absolute project path"`
	Day         string `json:"day,omitempty" jsonschema:"calendar day to scope the proposal to, YYYY-MM-DD (local); defaults to today"`
	Threshold   int    `json:"threshold,omitempty" jsonschema:"importance-sum threshold for trigger classification (default 150)"`
}

// ProposeReflectionOutput is the structured payload returned to the host.
// The `markdown` field is render-ready; the `entries` field is the
// structured form the host hands to record_reflection after synthesizing.
type ProposeReflectionOutput struct {
	ProjectPath string                 `json:"project_path"`
	Reason      string                 `json:"reason"`
	Entries     []worklog.PendingEntry `json:"entries"`
	Markdown    string                 `json:"markdown"`
}

func handleProposeReflection(ctx context.Context, db *store.DB, in ProposeReflectionInput) (*ProposeReflectionOutput, error) {
	// Roll worktrees up to the canonical main-repo path so /klyne:reflect
	// from a worktree finds the entries seeded under that repo.
	in.ProjectPath = projectpath.Canonical(in.ProjectPath)
	now := time.Now()
	var day time.Time
	if in.Day != "" {
		parsed, err := time.ParseInLocation("2006-01-02", in.Day, now.Location())
		if err != nil {
			return nil, fmt.Errorf("propose_reflection: bad day %q (want YYYY-MM-DD): %w", in.Day, err)
		}
		day = parsed
	}
	entries, reason, err := worklog.LoadPendingEntries(ctx, db, in.ProjectPath, in.Threshold, now, day)
	if err != nil {
		return nil, err
	}
	md := formatProposeReflectionMD(in.ProjectPath, reason, entries)

	// Layer-2 grounding (spec §7.2 improvements 2 & 4): append the
	// deterministic, salience-ranked git brief + the §7.1 grounding
	// contract so the AI host leads with the highest-impact work and
	// never invents a PR/ticket id. The brief is empty for a non-git
	// project — the proposal markdown is then unchanged (D8).
	since := now.Add(-7 * 24 * time.Hour)
	if rep, serr := worklog.BuildProjectSubstrate(in.ProjectPath, since, now); serr == nil {
		if brief := worklog.ProposalGitBrief(rep, in.ProjectPath); brief != "" {
			md = md + "\n\n" + brief + "\n"
		}
	}

	return &ProposeReflectionOutput{
		ProjectPath: in.ProjectPath,
		Reason:      reason,
		Entries:     entries,
		Markdown:    md,
	}, nil
}

// formatProposeReflectionMD renders the proposal brief verbatim-ready.
// Empty entries → a single explanatory line so the host can tell the
// user "nothing pending" without inventing insights.
func formatProposeReflectionMD(project, reason string, entries []worklog.PendingEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("# Reflection proposal for %s\n\n_No pending entries since the last reflection. Nothing to synthesize._\n", project)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Reflection proposal for %s\n\n", project)
	fmt.Fprintf(&sb, "Trigger reason: **%s**\n\n", reason)
	fmt.Fprintf(&sb, "## Pending entries (%d)\n\n", len(entries))
	for _, e := range entries {
		body := e.AIDraftedSummary
		if body == "" {
			body = e.LastUser
		}
		body = truncateProposerBody(body, proposeReflectionEntryBody)
		topic := e.RecapTopic
		if topic == "" {
			topic = "(no topic)"
		}
		if body == "" {
			fmt.Fprintf(&sb, "- id=%s [%s] importance=%d topic=%q\n",
				e.SessionID, e.CLI, e.Importance, topic)
		} else {
			fmt.Fprintf(&sb, "- id=%s [%s] importance=%d topic=%q — %s\n",
				e.SessionID, e.CLI, e.Importance, topic, body)
		}
	}
	return sb.String()
}

// truncateProposerBody collapses newlines and caps at n runes. Kept local
// to avoid pulling the worklog helper across package boundaries — the
// proposer surfaces a slightly different brief than the export writer.
func truncateProposerBody(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// HandleProposeReflection is the MCP entry-point. Opens the DB, delegates
// to handleProposeReflection, returns the structured payload + a short
// text summary for the tool-log line.
func HandleProposeReflection(ctx context.Context, _ *mcp.CallToolRequest, in ProposeReflectionInput) (*mcp.CallToolResult, ProposeReflectionOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, ProposeReflectionOutput{}, fmt.Errorf("propose_reflection: open db: %w", err)
	}
	defer db.Close()
	out, err := handleProposeReflection(ctx, db, in)
	if err != nil {
		return nil, ProposeReflectionOutput{}, err
	}
	summary := fmt.Sprintf("propose_reflection: %d entries pending for %s (%s)", len(out.Entries), in.ProjectPath, out.Reason)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
