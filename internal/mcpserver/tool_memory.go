package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)

// Memory tools
// ============
// A user-facing facade over the underlying `decisions` table. Two
// concepts the user actually cares about — *remembering* something
// (with explicit project vs global scope) and *recalling* everything
// relevant to the current cwd at once (project ∪ global).
//
// These tools share the same SQLite table as record_decision /
// list_decisions / search_decisions. They exist because the trigger
// phrases the user types — "klyne remember this …", "klyne remember
// this globally …", "refer klyne and …" — map cleanly onto a small
// surface (remember, recall) and awkwardly onto the old verbs.
//
// The data model:
//   * project_path = "" means GLOBAL (applies to every project)
//   * project_path = <abs path> means PROJECT-scoped
// recall always returns BOTH lists, labelled, so the AI doesn't have
// to make two calls and stitch them together.

// MemoryScope is the user-visible scope marker. "project" stores
// under the current project; "global" stores under empty project_path
// and applies everywhere.
type MemoryScope string

const (
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeGlobal  MemoryScope = "global"
)

// RememberMemoryInput is the input schema for the `remember` tool.
//
// Trigger phrases handled by the matching CLAUDE.md rule:
//
//	"klyne remember this …"           → scope=project (default)
//	"klyne remember … for this project" → scope=project
//	"klyne remember this globally …"  → scope=global
//	"klyne remember … everywhere"     → scope=global
type RememberMemoryInput struct {
	// Text is the memory body. Required. Free-form — fine for short
	// "we picked X" notes AND for multi-line runbooks. The schema
	// does not impose a length limit.
	Text string `json:"text" jsonschema:"the memory body; required, free-form text (multi-line OK)"`
	// Scope controls whether the memory is scoped to one project
	// (default) or applies globally. Pass "global" to make this
	// surface in every project's recall.
	Scope MemoryScope `json:"scope,omitempty" jsonschema:"project (default) | global"`
	// ProjectPath overrides cwd-derived project resolution when
	// scope=project. Ignored when scope=global.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; defaults to cwd when scope=project and blank"`
	// SessionID optionally pins the memory to one session.
	SessionID string `json:"session_id,omitempty" jsonschema:"optional session id this memory belongs to"`
	// Tags label the memory for filtering. Useful values: "runbook",
	// "decision", "secrets", "infra", "deploy".
	Tags []string `json:"tags,omitempty" jsonschema:"optional tags for filtering on recall"`
	// CWD lets the host pass the working directory; used to fill
	// ProjectPath when scope=project and ProjectPath is blank.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path under scope=project"`
}

// RememberMemoryOutput echoes the persisted row's identity.
type RememberMemoryOutput struct {
	ID          string      `json:"id"`
	Ts          int64       `json:"ts"`
	Scope       MemoryScope `json:"scope"`
	ProjectPath string      `json:"project_path,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
}

// RecallMemoryInput is the input schema for the `recall` tool.
//
// Returns BOTH project-scoped memories AND global memories, labelled.
// The AI calls this once before acting on operational requests (e.g.
// "refer klyne and add new secret …") so it has the full picture
// without making two list calls.
type RecallMemoryInput struct {
	// ProjectPath overrides cwd-derived project resolution. Set
	// explicitly when the AI needs memories for a service other than
	// the current cwd.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; defaults to cwd"`
	// Query optionally filters by case-insensitive substring against
	// the memory text. Applies to both project and global lists.
	Query string `json:"query,omitempty" jsonschema:"optional substring filter, case-insensitive"`
	// Tag optionally filters by a single tag. Applies to both lists.
	Tag string `json:"tag,omitempty" jsonschema:"optional tag filter (e.g. runbook)"`
	// Limit caps each scope's results. Default 50, max 500.
	Limit int `json:"limit,omitempty" jsonschema:"max rows per scope; default 50, max 500"`
	// CWD lets the host pass the working directory.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
}

// RecallMemoryOutput labels the two scopes so the AI can reason
// about which memories are project-specific vs global.
type RecallMemoryOutput struct {
	// ProjectPath is the resolved project root the project list is
	// scoped to. Echoed back so the caller can confirm resolution.
	ProjectPath string `json:"project_path,omitempty"`
	// ProjectMemories are decisions where project_path == ProjectPath.
	ProjectMemories []store.Decision `json:"project_memories"`
	// GlobalMemories are decisions where project_path == "".
	GlobalMemories []store.Decision `json:"global_memories"`
	// ProjectCount and GlobalCount mirror the slice lengths so JSON
	// consumers can render counts without re-iterating.
	ProjectCount int `json:"project_count"`
	GlobalCount  int `json:"global_count"`
	// Total is ProjectCount + GlobalCount.
	Total int `json:"total"`
}

// HandleRememberMemory writes a new memory row with explicit scope.
// Wraps the existing decisions storage — same table, friendlier verb.
func HandleRememberMemory(ctx context.Context, _ *mcp.CallToolRequest, in RememberMemoryInput) (*mcp.CallToolResult, RememberMemoryOutput, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, RememberMemoryOutput{}, errors.New("text is required")
	}

	scope := in.Scope
	if scope == "" {
		scope = MemoryScopeProject
	}
	if scope != MemoryScopeProject && scope != MemoryScopeGlobal {
		return nil, RememberMemoryOutput{}, fmt.Errorf("invalid scope %q: must be project or global", scope)
	}

	// project_path resolution:
	//   * scope=global → always ""
	//   * scope=project → explicit ProjectPath wins (verbatim), else
	//     canonicalize CWD (worktree → main repo), else error. We refuse
	//     to silently fall back to global.
	var projectPath string
	if scope == MemoryScopeGlobal {
		projectPath = ""
	} else {
		projectPath = strings.TrimSpace(in.ProjectPath)
		if projectPath == "" {
			cwd := strings.TrimSpace(in.CWD)
			if cwd != "" {
				projectPath = projectpath.Canonical(cwd)
			}
		}
		if projectPath == "" {
			return nil, RememberMemoryOutput{}, errors.New("scope=project requires project_path or cwd to be set")
		}
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, RememberMemoryOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	d := &store.Decision{
		ID:          mintDecisionID(),
		Ts:          time.Now().UnixMilli(),
		ProjectPath: projectPath,
		SessionID:   in.SessionID,
		Text:        strings.TrimSpace(in.Text),
		Tags:        sanitizeTags(in.Tags),
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		return nil, RememberMemoryOutput{}, err
	}
	out := RememberMemoryOutput{
		ID:          d.ID,
		Ts:          d.Ts,
		Scope:       scope,
		ProjectPath: d.ProjectPath,
		SessionID:   d.SessionID,
	}
	summary := fmt.Sprintf("remembered %s (%s scope)", d.ID, scope)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// HandleRecallMemory returns project-scoped and global memories in
// one call. The AI uses this BEFORE acting on operational requests so
// it knows whether a runbook or prior decision applies.
func HandleRecallMemory(ctx context.Context, _ *mcp.CallToolRequest, in RecallMemoryInput) (*mcp.CallToolResult, RecallMemoryOutput, error) {
	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		cwd := strings.TrimSpace(in.CWD)
		if cwd != "" {
			projectPath = projectpath.Canonical(cwd)
		}
	}
	// projectPath may still be "" — that's fine. recall then returns
	// only the global list (since there is no project to scope to).

	limit := clampLimit(in.Limit, 50, 500)

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, RecallMemoryOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	query := strings.TrimSpace(in.Query)
	tag := strings.TrimSpace(in.Tag)

	fetch := func(scopePath string) ([]store.Decision, error) {
		if query != "" {
			rows, err := store.SearchDecisions(ctx, db, query, scopePath, limit)
			if err != nil {
				return nil, err
			}
			if tag == "" {
				return rows, nil
			}
			return filterByTag(rows, tag), nil
		}
		return store.ListDecisions(ctx, db, store.DecisionFilter{
			ProjectPath: scopePath,
			Tag:         tag,
			Limit:       limit,
		})
	}

	var projectMemories []store.Decision
	if projectPath != "" {
		projectMemories, err = fetch(projectPath)
		if err != nil {
			return nil, RecallMemoryOutput{}, err
		}
	} else {
		projectMemories = []store.Decision{}
	}

	// Globals: project_path = "". ListDecisions/SearchDecisions
	// short-circuit on empty ProjectPath (treating it as "no filter"),
	// so we have to filter client-side instead. Small win to do this
	// here rather than adding a new store primitive.
	allCandidates, err := fetch("")
	if err != nil {
		return nil, RecallMemoryOutput{}, err
	}
	globalMemories := make([]store.Decision, 0)
	for _, d := range allCandidates {
		if d.ProjectPath == "" {
			globalMemories = append(globalMemories, d)
		}
	}

	out := RecallMemoryOutput{
		ProjectPath:     projectPath,
		ProjectMemories: projectMemories,
		GlobalMemories:  globalMemories,
		ProjectCount:    len(projectMemories),
		GlobalCount:     len(globalMemories),
		Total:           len(projectMemories) + len(globalMemories),
	}
	summary := fmt.Sprintf("recalled %d memories (%d project, %d global)",
		out.Total, out.ProjectCount, out.GlobalCount)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// filterByTag is a small client-side filter for the case where the
// search path is taken (which doesn't accept a tag). Linear over the
// returned rows; fine at the recall-limit sizes (≤ 500).
func filterByTag(rows []store.Decision, tag string) []store.Decision {
	out := make([]store.Decision, 0, len(rows))
	for _, d := range rows {
		for _, t := range d.Tags {
			if t == tag {
				out = append(out, d)
				break
			}
		}
	}
	return out
}
