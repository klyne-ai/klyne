package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// PreCompactInput is the input schema for get_pre_compact_context.
type PreCompactInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
	// Limit caps how many pre-compact messages to return. Defaults to
	// 30 (the most common useful slice); hard ceiling 100.
	Limit int `json:"limit,omitempty" jsonschema:"how many of the most recent pre-compact messages to return; default 30, max 100"`
}

// PreCompactMessageRow is the typed shape of one pre-compact message
// in the structured output. Trimmed to the fields an AI actually
// needs to reconstruct context — full canonical Message would carry
// noise like tool_results' giant payloads.
type PreCompactMessageRow struct {
	Role    string `json:"role" jsonschema:"user, assistant, tool, or system"`
	Content string `json:"content" jsonschema:"the text body of the message; truncated"`
	Model   string `json:"model,omitempty" jsonschema:"model id, when role is assistant"`
	TS      int64  `json:"ts" jsonschema:"epoch-millisecond timestamp of the message"`
}

// PreCompactOutput is the structured result of get_pre_compact_context.
type PreCompactOutput struct {
	SessionID         string                  `json:"session_id,omitempty" jsonschema:"the session id whose pre-compact context is reported"`
	Path              string                  `json:"path,omitempty" jsonschema:"absolute path of the source transcript"`
	FoundCompact      bool                    `json:"found_compact" jsonschema:"true when the session has at least one /compact event"`
	Trigger           string                  `json:"trigger,omitempty" jsonschema:"manual or auto when compactMetadata is present"`
	PreTokens         int64                   `json:"pre_tokens,omitempty" jsonschema:"cache-aware token count immediately before the compact"`
	CompactTimestamp  string                  `json:"compact_timestamp,omitempty" jsonschema:"RFC3339 timestamp of the last compact event"`
	Messages          []PreCompactMessageRow  `json:"messages,omitempty" jsonschema:"messages immediately preceding the last compact, oldest first"`
	Ambiguous         bool                    `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates        []CandidateRow          `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// preCompactRowPreviewBytes caps per-message body length in the
// structured output so a single huge tool_result does not blow the
// payload. Mirrors the handoff per-message cap intentionally.
const preCompactRowPreviewBytes = 600

// HandleGetPreCompactContext recovers the messages immediately before
// the last /compact event in a session. This is the only tool in the
// v1 surface that exposes information the AI literally cannot get on
// its own — once /compact runs, the original messages are gone from
// the AI's context, and only the JSONL still has them.
func HandleGetPreCompactContext(_ context.Context, _ *mcp.CallToolRequest, in PreCompactInput) (*mcp.CallToolResult, PreCompactOutput, error) {
	path, ambiguous, cands, err := resolveSession(GetContextHealthInput{
		SessionID: in.SessionID,
		CWD:       in.CWD,
	})
	if err != nil {
		return nil, PreCompactOutput{}, err
	}
	if ambiguous {
		rows := make([]CandidateRow, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, CandidateRow{
				SessionID: c.SessionID,
				Preview:   c.Preview,
				IsActive:  c.IsActive,
				ModTime:   c.ModTime.UTC().Format(timeRFC3339),
				MsgCount:  c.MsgCount,
			})
		}
		const reason = "Multiple Claude Code sessions in this project. Pick one and call get_pre_compact_context again with session_id."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{Ambiguous: true, Candidates: rows}, nil
	}
	if path == "" {
		const reason = "No Claude Code session found for this working directory."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{}, nil
	}

	bundle, err := LoadPreCompactMessages(path, in.Limit)
	if err != nil {
		return nil, PreCompactOutput{}, fmt.Errorf("load pre-compact messages: %w", err)
	}
	if !bundle.FoundCompact {
		const reason = "This session has not been /compact'd yet — nothing to recover."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{Path: path, FoundCompact: false}, nil
	}

	// Build per-row Content from text + tool-call activity. The canonical
	// Message stores tool_use as ToolCalls and tool_result as ToolResults
	// — both are typically empty-text on the wire. Without enrichment the
	// renderer ends up showing "(empty)" for every row that did real
	// work. Enrich the Content with a one-line summary of the tool
	// activity so the recovered context is actually useful.
	rows := make([]PreCompactMessageRow, 0, len(bundle.Messages))
	for _, m := range bundle.Messages {
		body := buildPreCompactBody(m)
		if body == "" {
			// Truly empty after enrichment — drop the row rather than
			// surfacing a noise line the user has to scroll past.
			continue
		}
		if len(body) > preCompactRowPreviewBytes {
			body = body[:preCompactRowPreviewBytes] + "…"
		}
		rows = append(rows, PreCompactMessageRow{
			Role:    string(m.Role),
			Content: body,
			Model:   m.Model,
			TS:      m.Ts,
		})
	}

	out := PreCompactOutput{
		Path:             path,
		FoundCompact:     true,
		Trigger:          bundle.Trigger,
		PreTokens:        bundle.PreTokens,
		CompactTimestamp: bundle.CompactTimestamp,
		Messages:         rows,
	}
	if len(bundle.Messages) > 0 && bundle.Messages[0].SessionID != "" {
		out.SessionID = bundle.Messages[0].SessionID
	}

	summary := fmt.Sprintf(
		"Recovered %d messages from before the last /compact event (trigger=%s, pre-compact size %d tokens).",
		len(rows), defaultStr(bundle.Trigger, "unknown"), bundle.PreTokens,
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// defaultStr returns s when non-empty, otherwise fallback. Tiny
// helper to keep the summary line readable.
func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// buildPreCompactBody enriches a canonical Message into a single
// human-readable summary for the pre-compact recovery view. The
// underlying canonical type carries text in Content, tool calls in
// ToolCalls, and tool results in ToolResults — most messages on
// the wire have at most one of those non-empty, but the renderer
// previously only surfaced Content. The result was dozens of
// "(empty)" rows for any session that actually used tools.
//
// Output rules:
//   - Plain text: rendered as-is (still subject to the per-row
//     length cap one level up).
//   - Assistant message with tool calls only: "→ Read foo.go" /
//     "→ Bash: go test ./..." / "→ Tool: <name>" so the user sees
//     what the AI actually did.
//   - Tool-result message: "← <ToolName> result: <preview>" or
//     "← <ToolName> error: <preview>" when IsError.
//   - Plain text + tool calls: text first, then a "→ tool" suffix
//     so both signals survive.
//
// Returns "" when the message has no signal at all (which is rare
// but does happen on stub system rows). The caller drops empty
// rows so the recovery surface stays dense.
func buildPreCompactBody(m *connectors.Message) string {
	text := strings.TrimSpace(m.Content)
	calls := summariseToolCalls(m.ToolCalls)
	results := summariseToolResults(m.ToolResults)

	parts := make([]string, 0, 3)
	if text != "" {
		parts = append(parts, text)
	}
	if calls != "" {
		parts = append(parts, calls)
	}
	if results != "" {
		parts = append(parts, results)
	}
	return strings.Join(parts, "  ·  ")
}

// summariseToolCalls renders a list of tool calls as a single
// arrow-prefixed string. Multiple calls in the same turn are
// joined with commas. Returns "" when there are none.
func summariseToolCalls(tcs []connectors.ToolCall) string {
	if len(tcs) == 0 {
		return ""
	}
	const maxCalls = 3
	parts := make([]string, 0, maxCalls+1)
	for i, tc := range tcs {
		if i >= maxCalls {
			parts = append(parts, fmt.Sprintf("…+%d more", len(tcs)-maxCalls))
			break
		}
		parts = append(parts, summariseOneToolCall(tc))
	}
	return "→ " + strings.Join(parts, ", ")
}

// summariseOneToolCall renders one tool call as "Read foo.go" /
// "Bash: go test" / "Tool: <name>" depending on the tool family.
func summariseOneToolCall(tc connectors.ToolCall) string {
	name := tc.Name
	if name == "" {
		name = "Tool"
	}
	if isShellToolPC(name) {
		if cmd := strings.TrimSpace(extractCommandPC(tc.Input)); cmd != "" {
			if len(cmd) > 60 {
				cmd = cmd[:60] + "…"
			}
			return fmt.Sprintf("%s: %s", name, cmd)
		}
		return name
	}
	if path := strings.TrimSpace(extractPathPC(tc.Input)); path != "" {
		return fmt.Sprintf("%s %s", name, displayPathPC(path))
	}
	return name
}

// summariseToolResults renders a list of tool results as a single
// arrow-prefixed string. Returns "" when there are none. Truncates
// the output preview so a giant tool blob does not dominate the
// row.
func summariseToolResults(trs []connectors.ToolResult) string {
	if len(trs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(trs))
	for _, tr := range trs {
		preview := strings.TrimSpace(tr.Output)
		preview = strings.ReplaceAll(preview, "\n", " ")
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		label := "result"
		if tr.IsError {
			label = "error"
		}
		if preview == "" {
			parts = append(parts, label)
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", label, preview))
		}
	}
	return "← " + strings.Join(parts, "; ")
}

// isShellToolPC, extractCommandPC, extractPathPC, displayPathPC are
// local copies of the helpers in handoff.go — the pre-compact
// renderer keeps its own to avoid a circular import friction with
// future test scaffolding. The behaviour mirrors the canonical
// helpers in handoff.go and contexthealth/classifier.go.
func isShellToolPC(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "sh", "exec", "run":
		return true
	}
	return false
}

func extractCommandPC(input string) string {
	if input == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "script"} {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func extractPathPC(input string) string {
	if input == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filepath", "filename"} {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func displayPathPC(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
