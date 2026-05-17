package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// Runbook proposer tools
// ======================
// Three tools surface the deterministic runbook-proposal pipeline
// to the agent so it can offer the user a recurring shell workflow
// as a saved runbook *without* writing to memory unilaterally.
//
//   propose_runbooks  — list candidates ranked by recurrence
//   accept_runbook    — accept one candidate; writes a memory row
//                       via the same path `remember` uses
//   dismiss_runbook   — never propose this signature again (for
//                       this project, or globally)
//
// All three operate on klyne's local SQLite + the JSONL ingest
// already on disk. Zero AI calls. Read-only across transcripts.

// ProposeRunbooksInput is the JSON-Schema for the propose tool.
// All fields optional; sensible defaults make the tool callable
// with `{}`.
type ProposeRunbooksInput struct {
	// CWD overrides os.Getwd() for project resolution. Useful when
	// the AI knows the project root differs from where it spawned
	// the MCP subprocess.
	CWD string `json:"cwd,omitempty" jsonschema:"override the working directory; defaults to current process cwd"`
	// ProjectPath bypasses CWD-based resolution entirely.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; default cwd"`
	// Limit caps how many candidates the response includes. Zero
	// uses the detector's MaxCandidates default (10).
	Limit int `json:"limit,omitempty" jsonschema:"max candidates to return (default 10, max 50)"`
}

// ProposeRunbooksOutput is the structured payload.
type ProposeRunbooksOutput struct {
	// ProjectPath echoes the resolved project so the caller knows
	// what scope the candidates apply to.
	ProjectPath string `json:"project_path" jsonschema:"the project the candidates apply to"`
	// Candidates is the ranked candidate list, highest score first.
	Candidates []insights.Candidate `json:"candidates" jsonschema:"ranked recurring command sequences"`
	// SessionsWalked is the number of sessions the detector
	// inspected. Surfaced so the caller can tell "zero results
	// because no data" from "zero results because nothing recurs".
	SessionsWalked int `json:"sessions_walked" jsonschema:"number of sessions walked while building proposals"`
	// Markdown is the slash-prompt-ready rendering. Always
	// populated so skills can echo verbatim.
	Markdown string `json:"markdown" jsonschema:"markdown rendering ready for verbatim display"`
}

// HandleProposeRunbooks walks recent sessions for the resolved
// project and returns candidate recurring command sequences.
func HandleProposeRunbooks(ctx context.Context, _ *mcp.CallToolRequest, in ProposeRunbooksInput) (*mcp.CallToolResult, ProposeRunbooksOutput, error) {
	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		cwd := strings.TrimSpace(in.CWD)
		if cwd == "" {
			w, err := os.Getwd()
			if err != nil {
				return nil, ProposeRunbooksOutput{}, fmt.Errorf("resolve cwd: %w", err)
			}
			cwd = w
		}
		projectPath = cwd
	}

	cfg := insights.DefaultRunbookConfig()
	if in.Limit > 0 {
		if in.Limit > 50 {
			in.Limit = 50
		}
		cfg.MaxCandidates = in.Limit
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, ProposeRunbooksOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	cands, err := insights.ProposeRunbooks(ctx, db, projectPath, cfg)
	if err != nil {
		return nil, ProposeRunbooksOutput{}, err
	}
	// SessionsWalked is approximated by re-counting via the same
	// filter; cheap given we cap at 500 sessions.
	sessions, _ := insights.GatherSessions(ctx, db, insights.Filter{
		ProjectPath: projectPath,
		MaxSessions: 500,
	})

	out := ProposeRunbooksOutput{
		ProjectPath:    projectPath,
		Candidates:     cands,
		SessionsWalked: len(sessions),
	}
	out.Markdown = renderRunbookProposals(out)
	summary := fmt.Sprintf("propose_runbooks: %d candidate(s) across %d session(s)",
		len(cands), len(sessions))
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// AcceptRunbookInput is the JSON-Schema for the accept tool.
type AcceptRunbookInput struct {
	// Signature is the canonical key for the candidate. The agent
	// usually passes ID + Signature together so the human knows
	// what they accepted; only Signature is required to dedupe.
	Signature string `json:"signature" jsonschema:"the candidate signature (required)"`
	// ProjectPath overrides cwd-derived project resolution.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; default cwd"`
	// CWD lets the host pass the working directory; used when
	// ProjectPath is blank.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
	// Name overrides the auto-suggested runbook title.
	Name string `json:"name,omitempty" jsonschema:"optional runbook name (defaults to auto-suggested verb-chain)"`
	// Body, when set, replaces the auto-rendered runbook body. The
	// agent typically passes a tidied multi-line version with the
	// user's explanatory wording.
	Body string `json:"body,omitempty" jsonschema:"optional pre-rendered runbook body (defaults to a numbered command list)"`
	// Tags supplements the default ["runbook", "klyne-proposed"]
	// tag set.
	Tags []string `json:"tags,omitempty" jsonschema:"optional additional tags"`
}

// AcceptRunbookOutput echoes the persisted memory's identity so
// the agent can confirm the save back to the user.
type AcceptRunbookOutput struct {
	MemoryID    string   `json:"memory_id"`
	Title       string   `json:"title"`
	ProjectPath string   `json:"project_path"`
	Tags        []string `json:"tags"`
}

// HandleAcceptRunbook turns a candidate into a memory row. We
// reuse the existing decisions table — same shape as `remember` —
// and tag the row so `recall` can filter for it.
func HandleAcceptRunbook(ctx context.Context, _ *mcp.CallToolRequest, in AcceptRunbookInput) (*mcp.CallToolResult, AcceptRunbookOutput, error) {
	sig := strings.TrimSpace(in.Signature)
	if sig == "" {
		return nil, AcceptRunbookOutput{}, errors.New("signature is required")
	}
	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(in.CWD)
	}
	if projectPath == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, AcceptRunbookOutput{}, fmt.Errorf("resolve cwd: %w", err)
		}
		projectPath = w
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, AcceptRunbookOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// Re-fetch candidates with a wide net so we can locate the
	// requested signature and pre-render a default body if the
	// caller didn't supply one.
	cfg := insights.DefaultRunbookConfig()
	cfg.MaxCandidates = 0 // unlimited; we filter to one signature
	cands, err := insights.ProposeRunbooks(ctx, db, projectPath, cfg)
	if err != nil {
		return nil, AcceptRunbookOutput{}, err
	}
	var match *insights.Candidate
	for i := range cands {
		if cands[i].Signature == sig {
			match = &cands[i]
			break
		}
	}
	if match == nil {
		return nil, AcceptRunbookOutput{},
			fmt.Errorf("no candidate matches signature %q (it may have been dismissed or aged out)", sig)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = match.SuggestedName
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		body = defaultRunbookBody(name, match)
	}

	tags := append([]string{"runbook", "klyne-proposed"}, in.Tags...)
	tags = dedupeTags(tags)

	d := &store.Decision{
		ID:          mintDecisionID(),
		Ts:          time.Now().UnixMilli(),
		ProjectPath: projectPath,
		Text:        body,
		Tags:        tags,
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		return nil, AcceptRunbookOutput{}, fmt.Errorf("insert memory: %w", err)
	}

	out := AcceptRunbookOutput{
		MemoryID:    d.ID,
		Title:       name,
		ProjectPath: projectPath,
		Tags:        tags,
	}
	summary := fmt.Sprintf("accepted runbook %s as memory %s", name, d.ID)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// DismissRunbookInput is the JSON-Schema for the dismiss tool.
type DismissRunbookInput struct {
	// Signature is the canonical key. Required.
	Signature string `json:"signature" jsonschema:"the candidate signature (required)"`
	// ProjectPath overrides cwd-derived project resolution. Pass
	// an empty string AND Scope="global" to dismiss everywhere.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; default cwd"`
	// CWD lets the host pass the working directory.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
	// Scope picks project vs global dismissal. Default project.
	Scope MemoryScope `json:"scope,omitempty" jsonschema:"project (default) | global"`
	// Reason is an optional human-readable note.
	Reason string `json:"reason,omitempty" jsonschema:"optional reason"`
}

// DismissRunbookOutput confirms persistence.
type DismissRunbookOutput struct {
	Signature   string      `json:"signature"`
	Scope       MemoryScope `json:"scope"`
	ProjectPath string      `json:"project_path,omitempty"`
}

// HandleDismissRunbook records a dismissal so the proposer never
// re-surfaces the same signature.
func HandleDismissRunbook(ctx context.Context, _ *mcp.CallToolRequest, in DismissRunbookInput) (*mcp.CallToolResult, DismissRunbookOutput, error) {
	sig := strings.TrimSpace(in.Signature)
	if sig == "" {
		return nil, DismissRunbookOutput{}, errors.New("signature is required")
	}
	scope := in.Scope
	if scope == "" {
		scope = MemoryScopeProject
	}
	if scope != MemoryScopeProject && scope != MemoryScopeGlobal {
		return nil, DismissRunbookOutput{}, fmt.Errorf("invalid scope %q", scope)
	}

	var projectPath string
	if scope == MemoryScopeProject {
		projectPath = strings.TrimSpace(in.ProjectPath)
		if projectPath == "" {
			projectPath = strings.TrimSpace(in.CWD)
		}
		if projectPath == "" {
			w, err := os.Getwd()
			if err != nil {
				return nil, DismissRunbookOutput{}, fmt.Errorf("resolve cwd: %w", err)
			}
			projectPath = w
		}
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, DismissRunbookOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	d := store.RunbookDismissal{
		Signature:   sig,
		ProjectPath: projectPath,
		Ts:          time.Now().UnixMilli(),
		Reason:      strings.TrimSpace(in.Reason),
	}
	if err := store.UpsertRunbookDismissal(ctx, db, d); err != nil {
		return nil, DismissRunbookOutput{}, err
	}

	out := DismissRunbookOutput{
		Signature:   sig,
		Scope:       scope,
		ProjectPath: projectPath,
	}
	summary := fmt.Sprintf("dismissed runbook %s (%s scope)", insights.SignatureShortID(sig), scope)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// renderRunbookProposals renders the candidate list as Markdown.
// Empty candidate list still produces a usable response so the
// skill can show "no recurring workflows detected" instead of an
// awkward blank slate.
func renderRunbookProposals(out ProposeRunbooksOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# klyne runbook proposals\n\n")
	fmt.Fprintf(&b, "Project: `%s`\n", out.ProjectPath)
	fmt.Fprintf(&b, "Sessions walked: %d\n\n", out.SessionsWalked)
	if len(out.Candidates) == 0 {
		b.WriteString("_No recurring command sequences detected yet. ")
		b.WriteString("klyne needs at least 3 occurrences across 2 distinct sessions to propose a runbook._\n")
		return b.String()
	}
	for i, c := range out.Candidates {
		fmt.Fprintf(&b, "## %d. %s · `%s`\n\n", i+1, c.SuggestedName, c.ID)
		fmt.Fprintf(&b, "- Occurrences: **%d** across **%d** session(s)\n",
			c.Occurrences, c.DistinctSessions)
		if c.LastSeenMs > 0 {
			fmt.Fprintf(&b, "- Last seen: %s\n",
				time.UnixMilli(c.LastSeenMs).UTC().Format("2006-01-02 15:04 UTC"))
		}
		fmt.Fprintf(&b, "- Score: %.2f\n\n", c.Score)
		b.WriteString("Steps (most recent observation):\n\n")
		for j, cmd := range c.Commands {
			fmt.Fprintf(&b, "  %d. `%s`\n", j+1, cmd)
		}
		b.WriteString("\n")
	}
	b.WriteString("Accept any of these with `accept_runbook` (signature from the candidate). ")
	b.WriteString("Dismiss with `dismiss_runbook` to stop seeing them.\n")
	return b.String()
}

// defaultRunbookBody renders a memory body for an accepted runbook
// when the caller doesn't pass one explicitly. Matches the shape
// the existing memory tool already invites users to write.
func defaultRunbookBody(name string, c *insights.Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RUNBOOK: %s\n", name)
	fmt.Fprintf(&b, "Source: klyne propose_runbooks (signature %s, seen %d× across %d session(s))\n\n",
		c.ID, c.Occurrences, c.DistinctSessions)
	b.WriteString("Steps:\n")
	for i, cmd := range c.Commands {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, cmd)
	}
	b.WriteString("\nReview before re-running; substitute paths/IDs as needed.\n")
	return b.String()
}

// dedupeTags returns tags with duplicates removed, preserving the
// first occurrence's position. Empty strings are dropped.
func dedupeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
