package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// Status snapshot tool
// ====================
// Per-installation Markdown snapshot of what klyne has observed in
// the last N days. Different from per-task handoff (which is one
// session's story) and from bootstrap (which is the Day-1 brief for
// a fresh agent). The status snapshot answers "what has klyne *seen*
// across the last week?" and is meant for weekly review, sharing,
// or pasting into a teammate's chat.

// StatusSnapshotInput is the JSON-Schema for the tool. Every field
// is optional; sensible defaults make `{}` a valid call.
type StatusSnapshotInput struct {
	// CWD lets the host pass the working directory; used when
	// ProjectPath is blank and AllProjects is false.
	CWD string `json:"cwd,omitempty" jsonschema:"working directory; used to default project_path"`
	// ProjectPath scopes the snapshot to one project. When empty
	// and AllProjects is false, the cwd is used. When AllProjects
	// is true, ProjectPath is ignored.
	ProjectPath string `json:"project_path,omitempty" jsonschema:"absolute project path; default cwd"`
	// AllProjects, when true, aggregates across every project on
	// this machine (overrides ProjectPath/cwd).
	AllProjects bool `json:"all_projects,omitempty" jsonschema:"include every project on this machine"`
	// SinceHours overrides the default 7-day window. Pass 168 for
	// the default, 24 for a daily check-in, 720 for a 30-day view.
	SinceHours int `json:"since_hours,omitempty" jsonschema:"window length in hours (default 168)"`
}

// StatusSnapshotOutput is the structured payload + a markdown body
// for verbatim display.
type StatusSnapshotOutput struct {
	Snapshot insights.StatusSnapshot `json:"snapshot"`
	Markdown string                  `json:"markdown" jsonschema:"verbatim-render-ready markdown body"`
}

// HandleStatusSnapshot computes the per-installation snapshot.
func HandleStatusSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in StatusSnapshotInput) (*mcp.CallToolResult, StatusSnapshotOutput, error) {
	win := insights.DefaultStatusWindow()
	if in.SinceHours > 0 {
		win.SinceMs = time.Now().Add(-time.Duration(in.SinceHours) * time.Hour).UnixMilli()
	}

	if !in.AllProjects {
		projectPath := strings.TrimSpace(in.ProjectPath)
		if projectPath == "" {
			cwd := strings.TrimSpace(in.CWD)
			if cwd == "" {
				w, err := os.Getwd()
				if err != nil {
					return nil, StatusSnapshotOutput{}, fmt.Errorf("resolve cwd: %w", err)
				}
				cwd = w
			}
			projectPath = cwd
		}
		win.ProjectPath = projectPath
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, StatusSnapshotOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	snap, err := insights.Snapshot(ctx, db, win)
	if err != nil {
		return nil, StatusSnapshotOutput{}, err
	}

	out := StatusSnapshotOutput{
		Snapshot: snap,
		Markdown: insights.RenderStatusAsMarkdown(snap),
	}
	summary := fmt.Sprintf("status: %d sessions, %d compact events, $%.2f priced compute",
		snap.TotalSessions, snap.CompactCount, snap.TotalCostUSD)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}
