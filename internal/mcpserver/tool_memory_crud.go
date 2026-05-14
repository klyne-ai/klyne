package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// Memory CRUD tools
// =================
// Edit / delete / browse companions to the `remember` and `recall`
// surface defined in tool_memory.go. Together they give the AI full
// CRUD parity with Serena's memory verbs:
//
//	remember        → write
//	recall          → query (project ∪ global, query-filtered)
//	list_memories   → browse (project ∪ global, no query filter, with
//	                  derived `name` per row for human-friendly display)
//	update_memory   → edit text and/or tags by id
//	delete_memory   → remove by id
//
// All five share the same underlying `decisions` table.

// MemoryScopeAll is the third value accepted by list_memories. It
// returns BOTH project and global lists in one call. The remember /
// recall surface doesn't need this value because remember always
// requires an explicit project|global, and recall always returns both.
const MemoryScopeAll MemoryScope = "all"

// UpdateMemoryInput patches an existing memory row.
//
// Text and Tags are pointers so the agent can distinguish "omitted"
// (no change) from "empty" (clear). Passing both omitted is an error
// — at least one field must be patched.
type UpdateMemoryInput struct {
	// ID is the memory id to patch. Required.
	ID string `json:"id" jsonschema:"required: memory id to update"`
	// Text, when non-nil, overwrites the memory body. Whitespace-only
	// strings are rejected.
	Text *string `json:"text,omitempty" jsonschema:"new memory text; omit to leave unchanged"`
	// Tags, when non-nil, overwrites the FULL tag set. Pass an empty
	// array to clear tags entirely; omit to leave them unchanged.
	Tags *[]string `json:"tags,omitempty" jsonschema:"new tag set; omit to leave unchanged. Pass empty array to clear tags"`
}

// UpdateMemoryOutput echoes the patched id.
type UpdateMemoryOutput struct {
	ID      string `json:"id"`
	Updated bool   `json:"updated"`
}

// DeleteMemoryInput removes one memory by id.
type DeleteMemoryInput struct {
	// ID is the memory id to delete. Required.
	ID string `json:"id" jsonschema:"required: memory id to delete"`
}

// DeleteMemoryOutput echoes the deleted id.
type DeleteMemoryOutput struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// ListMemoriesInput is the explicit-browse surface. Unlike recall it
// does NOT take a query — its job is to enumerate. It accepts a
// scope value that adds "all" (default) to the existing project|global
// enum so the AI can ask for one or both lists in one shot.
type ListMemoriesInput struct {
	// CWD is the working directory; used to default project_path.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
	// ProjectPath overrides cwd-derived project resolution.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; defaults to cwd"`
	// Scope selects which list(s) to return: all (default) | project | global.
	Scope string `json:"scope,omitempty" jsonschema:"all (default) | project | global"`
	// Limit caps each scope's results. Default 50, max 500.
	Limit int `json:"limit,omitempty" jsonschema:"max rows per scope; default 50, max 500"`
	// Tag optionally filters by a single tag. Applies to both lists.
	Tag string `json:"tag,omitempty" jsonschema:"optional tag filter"`
}

// NamedMemory wraps a Decision with a derived display Name: the first
// non-empty line of `text`, trimmed and truncated to 60 runes. Lets
// the agent present memories Serena-style as a named list without
// re-doing the derivation client-side.
type NamedMemory struct {
	store.Decision
	Name string `json:"name"`
}

// ListMemoriesOutput labels the two scopes the same way recall does
// so the caller can render them side-by-side.
type ListMemoriesOutput struct {
	ProjectPath     string        `json:"project_path,omitempty"`
	ProjectMemories []NamedMemory `json:"project_memories"`
	GlobalMemories  []NamedMemory `json:"global_memories"`
	Total           int           `json:"total"`
}

// HandleUpdateMemory patches text and/or tags on an existing memory.
// At least one of text / tags must be supplied. Unknown ids return a
// sql.ErrNoRows-wrapped error.
func HandleUpdateMemory(ctx context.Context, _ *mcp.CallToolRequest, in UpdateMemoryInput) (*mcp.CallToolResult, UpdateMemoryOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, UpdateMemoryOutput{}, errors.New("id is required")
	}
	if in.Text == nil && in.Tags == nil {
		return nil, UpdateMemoryOutput{}, errors.New("at least one of text or tags must be provided")
	}

	var (
		text       string
		tags       []string
		updateText bool
		updateTags bool
	)
	if in.Text != nil {
		updateText = true
		text = strings.TrimSpace(*in.Text)
		if text == "" {
			return nil, UpdateMemoryOutput{}, errors.New("text must be non-empty when provided")
		}
	}
	if in.Tags != nil {
		updateTags = true
		tags = sanitizeTags(*in.Tags)
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, UpdateMemoryOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := store.UpdateDecision(ctx, db, id, text, tags, updateText, updateTags); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, UpdateMemoryOutput{ID: id}, fmt.Errorf("memory %q not found", id)
		}
		return nil, UpdateMemoryOutput{ID: id}, err
	}
	out := UpdateMemoryOutput{ID: id, Updated: true}
	summary := fmt.Sprintf("updated %s", id)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// HandleDeleteMemory removes one memory by id. Unknown ids return an
// error so the agent can surface a "not found" to the user.
func HandleDeleteMemory(ctx context.Context, _ *mcp.CallToolRequest, in DeleteMemoryInput) (*mcp.CallToolResult, DeleteMemoryOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, DeleteMemoryOutput{}, errors.New("id is required")
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, DeleteMemoryOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := store.DeleteDecision(ctx, db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, DeleteMemoryOutput{ID: id}, fmt.Errorf("memory %q not found", id)
		}
		return nil, DeleteMemoryOutput{ID: id}, err
	}
	out := DeleteMemoryOutput{ID: id, Deleted: true}
	summary := fmt.Sprintf("deleted %s", id)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// HandleListMemories enumerates memories without applying a query
// filter. Default scope=all returns both project + global lists.
// Each row carries a derived `name` (first non-empty line, ≤60 runes)
// so callers can render Serena-style named lists directly.
func HandleListMemories(ctx context.Context, _ *mcp.CallToolRequest, in ListMemoriesInput) (*mcp.CallToolResult, ListMemoriesOutput, error) {
	scope := strings.TrimSpace(in.Scope)
	if scope == "" {
		scope = string(MemoryScopeAll)
	}
	switch MemoryScope(scope) {
	case MemoryScopeAll, MemoryScopeProject, MemoryScopeGlobal:
	default:
		return nil, ListMemoriesOutput{}, fmt.Errorf("invalid scope %q: must be all, project, or global", scope)
	}

	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(in.CWD)
	}

	limit := clampLimit(in.Limit, 50, 500)
	tag := strings.TrimSpace(in.Tag)

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, ListMemoriesOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	wantProject := scope == string(MemoryScopeAll) || scope == string(MemoryScopeProject)
	wantGlobal := scope == string(MemoryScopeAll) || scope == string(MemoryScopeGlobal)

	out := ListMemoriesOutput{
		ProjectPath:     projectPath,
		ProjectMemories: []NamedMemory{},
		GlobalMemories:  []NamedMemory{},
	}

	if wantProject && projectPath != "" {
		rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
			ProjectPath: projectPath,
			Tag:         tag,
			Limit:       limit,
		})
		if err != nil {
			return nil, ListMemoriesOutput{}, err
		}
		out.ProjectMemories = wrapNamed(rows)
	}

	if wantGlobal {
		// ListDecisions treats ProjectPath="" as "no filter", so we
		// pull a broader page and filter to ProjectPath=="" client-side.
		// Same trick recall uses; fine at limit ceilings (≤ 500).
		rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
			Tag:   tag,
			Limit: limit * 4, // headroom so a project-heavy DB still surfaces globals
		})
		if err != nil {
			return nil, ListMemoriesOutput{}, err
		}
		filtered := make([]store.Decision, 0, len(rows))
		for _, d := range rows {
			if d.ProjectPath == "" {
				filtered = append(filtered, d)
				if len(filtered) >= limit {
					break
				}
			}
		}
		out.GlobalMemories = wrapNamed(filtered)
	}

	out.Total = len(out.ProjectMemories) + len(out.GlobalMemories)
	summary := fmt.Sprintf("listed %d memories (%d project, %d global)",
		out.Total, len(out.ProjectMemories), len(out.GlobalMemories))
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// wrapNamed decorates each Decision with its derived display Name.
func wrapNamed(in []store.Decision) []NamedMemory {
	out := make([]NamedMemory, 0, len(in))
	for _, d := range in {
		out = append(out, NamedMemory{Decision: d, Name: deriveMemoryName(d.Text)})
	}
	return out
}

// deriveMemoryName takes the first non-empty line of text, trims it,
// and truncates to 60 runes (NOT bytes) appending "…" when cut.
func deriveMemoryName(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	// First non-empty line.
	var firstLine string
	for _, line := range strings.Split(trimmed, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			firstLine = s
			break
		}
	}
	if firstLine == "" {
		return ""
	}
	const maxRunes = 60
	runes := []rune(firstLine)
	if len(runes) <= maxRunes {
		return firstLine
	}
	return string(runes[:maxRunes]) + "…"
}
