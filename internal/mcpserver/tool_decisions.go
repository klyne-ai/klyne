package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)

// Decisions tools
// ===============
// Inspired by mkreyman/mcp-memory-keeper, but native to klyne so users
// don't have to install a second MCP server. Writes/reads go directly
// against klyne's SQLite store (WAL mode — safe to run alongside the
// daemon). The data model is intentionally tiny: one immutable row per
// decision, with optional project + session scoping plus tags.

// RecordDecisionInput is the input schema for record_decision.
type RecordDecisionInput struct {
	// Text is the decision body. Required.
	Text string `json:"text" jsonschema:"the decision to record; required, free-form text"`
	// ProjectPath scopes the decision to one project. Omit (or pass
	// empty) for a global decision. Defaults to the CWD when omitted
	// AND `cwd` is set.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; defaults to cwd when blank"`
	// SessionID, when set, ties the decision to a specific session row.
	SessionID string `json:"session_id,omitempty" jsonschema:"optional session id this decision belongs to"`
	// Tags is an optional list of short labels (≤ 5 recommended).
	Tags []string `json:"tags,omitempty" jsonschema:"optional tags for filtering on the list side"`
	// CWD lets the host pass the working directory; used only to fill
	// ProjectPath when omitted.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
}

// RecordDecisionOutput is the response from record_decision.
type RecordDecisionOutput struct {
	ID          string `json:"id"`
	Ts          int64  `json:"ts"`
	ProjectPath string `json:"project_path,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

// ListDecisionsInput is the input schema for list_decisions.
type ListDecisionsInput struct {
	// ProjectPath scopes the result. Empty + AllProjects=false defaults
	// to the CWD.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"limit to this project; defaults to cwd"`
	// SessionID scopes further to one session.
	SessionID string `json:"session_id,omitempty" jsonschema:"limit to one session"`
	// Tag scopes further to one tag.
	Tag string `json:"tag,omitempty" jsonschema:"limit to decisions carrying this tag"`
	// Limit caps the result. Default 50; max 500.
	Limit int `json:"limit,omitempty" jsonschema:"max rows; default 50, max 500"`
	// AllProjects: when true, ignore ProjectPath and return everything.
	AllProjects bool `json:"all_projects,omitempty" jsonschema:"include decisions from every project"`
	// CWD lets the host pass the working directory; used only when
	// ProjectPath is blank.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
}

// SearchDecisionsInput is the input schema for search_decisions.
type SearchDecisionsInput struct {
	// Query is a case-insensitive substring match against the text.
	Query string `json:"query" jsonschema:"case-insensitive substring; required"`
	// ProjectPath scopes the search. Empty + AllProjects=false defaults
	// to the CWD.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"limit to this project; defaults to cwd"`
	// Limit caps the result. Default 50; max 500.
	Limit int `json:"limit,omitempty" jsonschema:"max rows; default 50, max 500"`
	// AllProjects: when true, ignore ProjectPath and return everything.
	AllProjects bool `json:"all_projects,omitempty" jsonschema:"include decisions from every project"`
	// CWD lets the host pass the working directory.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
}

// DecisionsListOutput is shared between list_decisions and search_decisions.
type DecisionsListOutput struct {
	Decisions []store.Decision `json:"decisions"`
	Count     int              `json:"count"`
}

// HandleRecordDecision writes a new decision to the store. Returns the
// generated id so the caller can echo it back to the user.
func HandleRecordDecision(ctx context.Context, _ *mcp.CallToolRequest, in RecordDecisionInput) (*mcp.CallToolResult, RecordDecisionOutput, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, RecordDecisionOutput{}, errors.New("text is required")
	}
	proj := in.ProjectPath
	if proj == "" && in.CWD != "" {
		proj = projectpath.Canonical(in.CWD)
	}
	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, RecordDecisionOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	d := &store.Decision{
		ID:          mintDecisionID(),
		Ts:          time.Now().UnixMilli(),
		ProjectPath: proj,
		SessionID:   in.SessionID,
		Text:        strings.TrimSpace(in.Text),
		Tags:        sanitizeTags(in.Tags),
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		return nil, RecordDecisionOutput{}, err
	}
	out := RecordDecisionOutput{ID: d.ID, Ts: d.Ts, ProjectPath: d.ProjectPath, SessionID: d.SessionID}
	summary := fmt.Sprintf("decision recorded: %s", d.ID)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// HandleListDecisions returns decisions matching the input filter.
func HandleListDecisions(ctx context.Context, _ *mcp.CallToolRequest, in ListDecisionsInput) (*mcp.CallToolResult, DecisionsListOutput, error) {
	proj := in.ProjectPath
	if !in.AllProjects && proj == "" && in.CWD != "" {
		proj = projectpath.Canonical(in.CWD)
	}
	if in.AllProjects {
		proj = ""
	}
	limit := clampLimit(in.Limit, 50, 500)

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, DecisionsListOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: proj,
		SessionID:   in.SessionID,
		Tag:         in.Tag,
		Limit:       limit,
	})
	if err != nil {
		return nil, DecisionsListOutput{}, err
	}
	out := DecisionsListOutput{Decisions: rows, Count: len(rows)}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%d decisions", len(rows))}},
	}, out, nil
}

// HandleSearchDecisions runs a substring search.
func HandleSearchDecisions(ctx context.Context, _ *mcp.CallToolRequest, in SearchDecisionsInput) (*mcp.CallToolResult, DecisionsListOutput, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, DecisionsListOutput{}, errors.New("query is required")
	}
	proj := in.ProjectPath
	if !in.AllProjects && proj == "" && in.CWD != "" {
		proj = projectpath.Canonical(in.CWD)
	}
	if in.AllProjects {
		proj = ""
	}
	limit := clampLimit(in.Limit, 50, 500)

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, DecisionsListOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	rows, err := store.SearchDecisions(ctx, db, q, proj, limit)
	if err != nil {
		return nil, DecisionsListOutput{}, err
	}
	out := DecisionsListOutput{Decisions: rows, Count: len(rows)}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%d matches", len(rows))}},
	}, out, nil
}

// --- helpers ------------------------------------------------------

func mintDecisionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("d-%d", time.Now().UnixNano())
	}
	return "d-" + hex.EncodeToString(b[:])
}

func sanitizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// clampLimit applies a default + max ceiling without ever returning <= 0.
func clampLimit(req, def, max int) int {
	if req <= 0 {
		return def
	}
	if req > max {
		return max
	}
	return req
}
