package mcpserver

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/connectors/codereviewgraph"
)

// CodeReviewContextInput is the input schema for code_review_context.
type CodeReviewContextInput struct {
	// ProjectRoot is the absolute path of the repo whose
	// `.code-review-graph/` directory should be inspected. When empty
	// the tool falls back to the current process working directory —
	// the common case when klyne is launched from inside the repo.
	ProjectRoot string `json:"project_root,omitempty" jsonschema:"absolute project root containing the .code-review-graph/ directory; defaults to process cwd"`
}

// CodeReviewContextOutput is the structured output of code_review_context.
// All slice fields are non-nil after a successful call so consumers can
// iterate without nil checks.
type CodeReviewContextOutput struct {
	// ProjectRoot echoes the resolved project root the tool inspected.
	ProjectRoot string `json:"project_root" jsonschema:"the project root that was inspected"`
	// Detected is true when `<project_root>/.code-review-graph/` exists
	// and is non-empty. Lets the AI report "tool installed" vs "tool
	// absent" without needing to look at the slice lengths.
	Detected bool `json:"detected" jsonschema:"true when .code-review-graph/ exists and is non-empty"`
	// HighRiskFiles is the upstream tool's list of files that have a
	// history of regressions or hotspot churn. Empty when unavailable.
	HighRiskFiles []string `json:"high_risk_files" jsonschema:"files flagged as historically risky by code-review-graph"`
	// RecentBlockers is the open review-blocking issues for this repo.
	RecentBlockers []codereviewgraph.Blocker `json:"recent_blockers" jsonschema:"recent open review blockers (PRs, comments, follow-ups)"`
	// FrequentReviewers is the de-duped reviewer-handle list.
	FrequentReviewers []string `json:"frequent_reviewers" jsonschema:"reviewer handles that most often touch this project"`
}

// HandleCodeReviewContext exposes the optional code-review-graph
// enrichment to MCP consumers. Gracefully no-ops when the upstream
// tool's `.code-review-graph/` directory is absent — the AI just
// learns there's no enrichment data to cite.
//
// Disambiguation: unlike list_sessions / get_context_health, this tool
// keys on the repo root rather than a session, so the resolution is
// just project_root → cwd → error.
func HandleCodeReviewContext(_ context.Context, _ *mcp.CallToolRequest, in CodeReviewContextInput) (*mcp.CallToolResult, CodeReviewContextOutput, error) {
	root := in.ProjectRoot
	if root == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, CodeReviewContextOutput{}, fmt.Errorf("resolve cwd: %w", err)
		}
		root = w
	}

	out := CodeReviewContextOutput{
		ProjectRoot:       root,
		HighRiskFiles:     []string{},
		RecentBlockers:    []codereviewgraph.Blocker{},
		FrequentReviewers: []string{},
	}

	rg, err := codereviewgraph.Load(root)
	if err != nil {
		// Malformed `summary.json` is a real user-facing error — flag
		// it so the AI prompts the user to fix it rather than silently
		// pretending there's no data.
		return nil, out, fmt.Errorf("load code-review-graph: %w", err)
	}
	if rg == nil {
		// Tool not installed for this repo — the AI should surface
		// this gently, hence the explicit message.
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("No .code-review-graph/ found under %s — enrichment unavailable.", root),
			}},
		}, out, nil
	}

	out.Detected = true
	out.HighRiskFiles = rg.HighRiskFiles
	out.RecentBlockers = rg.RecentBlockers
	out.FrequentReviewers = rg.FrequentReviewers

	summary := fmt.Sprintf(
		"code-review-graph enrichment: %d high-risk files, %d recent blockers, %d frequent reviewers.",
		len(out.HighRiskFiles), len(out.RecentBlockers), len(out.FrequentReviewers),
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}
