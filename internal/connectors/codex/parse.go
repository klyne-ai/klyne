// Package codex implements the Codex CLI connector.
//
// Codex CLI writes session transcripts to:
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
//
// The format is experimental (spec §13 risk #2). This parser is designed to be
// forward-compatible: unknown fields are silently ignored and unknown type values
// are treated as system-equivalent messages rather than causing panics.
//
// Field-name decisions:
//   - session_id  (snake_case — Codex convention, differs from Claude's sessionId)
//   - usage.prompt_tokens    → Message.TokensIn
//   - usage.completion_tokens → Message.TokensOut
//   - compact_event lines are treated as system messages (W15 will add proper
//     detection once the real Codex compact signal is confirmed).
package codex

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// rawLine is the union of all fields that may appear in a Codex JSONL line.
// Unknown fields are absorbed by the embedded json.RawMessage map (via
// json.Decoder DisallowUnknownFields is intentionally NOT set).
type rawLine struct {
	// Common fields
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"timestamp"`
	Model     string `json:"model"`

	// session_meta
	CWD string `json:"cwd"`

	// input / output
	Role    string `json:"role"`
	Content string `json:"content"`

	// output only
	Usage *rawUsage `json:"usage,omitempty"`

	// function_call
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// function_call_output
	Output string `json:"output"`

	// compact_event (synthetic — verify against real sessions in Wave 1)
	BeforeTokens int64  `json:"before_tokens"`
	AfterTokens  int64  `json:"after_tokens"`
	Summary      string `json:"summary"`
}

// rawUsage represents the token accounting block in Codex output lines.
type rawUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// sessionIDFromPath derives the session ID from the JSONL file path when the
// line itself does not carry one. The file name is used as a fallback.
// Codex path: ~/.codex/sessions/YYYY/MM/DD/rollout-<id>.jsonl
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	// Strip extension
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

// parseTimestamp converts an ISO 8601 timestamp string to epoch milliseconds.
// Returns 0 on parse failure (caller falls back to wall-clock time).
func parseTimestamp(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try without nanoseconds
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

// normalizeRole maps Codex role strings onto canonical connectors.Role values.
func normalizeRole(codexRole, codexType string) connectors.Role {
	switch codexType {
	case "input":
		return connectors.RoleUser
	case "output":
		return connectors.RoleAssistant
	case "function_call":
		return connectors.RoleTool
	case "function_call_output":
		return connectors.RoleTool
	case "session_meta":
		return connectors.RoleSystem
	case "compact_event":
		// Treat as system message; W15 will handle proper compact detection.
		return connectors.RoleSystem
	}
	// Fall back to the role field if present.
	switch codexRole {
	case "user":
		return connectors.RoleUser
	case "assistant":
		return connectors.RoleAssistant
	case "system":
		return connectors.RoleSystem
	}
	// Unknown type — emit as system rather than panicking (forward-compat).
	return connectors.RoleSystem
}

// ParseLine converts a single raw JSONL line from a Codex session file into a
// canonical connectors.Message. The path is used to derive the session_id and
// project_path when the line does not carry them.
//
// Malformed lines (non-JSON, missing required fields) are skipped with a
// warning log; the caller receives (nil, error) and should continue to the
// next line rather than aborting.
func ParseLine(line []byte, path string) (*connectors.Message, error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		log.Printf("codex: skipping malformed line in %s: %v", path, err)
		return nil, fmt.Errorf("codex: malformed json: %w", err)
	}

	// Derive session ID — prefer line value, fall back to filename.
	sessionID := raw.SessionID
	if sessionID == "" {
		sessionID = sessionIDFromPath(path)
	}

	// Derive timestamp.
	ts := parseTimestamp(raw.Timestamp)
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}

	role := normalizeRole(raw.Role, raw.Type)

	msg := &connectors.Message{
		SessionID: sessionID,
		CLI:       connectors.CLICodex,
		Role:      role,
		Ts:        ts,
		Model:     raw.Model,
	}

	// Extract project path from session_meta lines or derive from path.
	if raw.CWD != "" {
		msg.ProjectPath = raw.CWD
	} else {
		// Best-effort: walk up from .../YYYY/MM/DD/rollout-*.jsonl to find cwd.
		// Not available in all lines; store empty and let the store backfill.
		msg.ProjectPath = ""
	}

	switch raw.Type {
	case "session_meta":
		// Session metadata — synthesize a system message.
		msg.Content = fmt.Sprintf("[session_meta] model=%s cwd=%s", raw.Model, raw.CWD)

	case "input":
		msg.Content = raw.Content

	case "output":
		msg.Content = raw.Content
		if raw.Usage != nil {
			msg.TokensIn = raw.Usage.PromptTokens
			msg.TokensOut = raw.Usage.CompletionTokens
		}

	case "function_call":
		// Represent the tool call in ToolCalls; Content carries a summary.
		inputStr := ""
		if len(raw.Arguments) > 0 {
			inputStr = string(raw.Arguments)
		}
		msg.ToolCalls = []connectors.ToolCall{
			{
				ID:    raw.CallID,
				Name:  raw.Name,
				Input: inputStr,
			},
		}
		msg.Content = fmt.Sprintf("[function_call] %s(%s)", raw.Name, inputStr)

	case "function_call_output":
		msg.ToolResults = []connectors.ToolResult{
			{
				ID:     raw.CallID,
				Output: raw.Output,
			},
		}
		msg.Content = raw.Output

	case "compact_event":
		// Synthetic marker — treated as system. W15 will refine.
		// Log a notice so operators can see compact events in the daemon logs.
		log.Printf("codex: compact_event detected in session %s (before=%d after=%d)",
			sessionID, raw.BeforeTokens, raw.AfterTokens)
		msg.Content = fmt.Sprintf("[compact_event] before_tokens=%d after_tokens=%d summary=%s",
			raw.BeforeTokens, raw.AfterTokens, raw.Summary)

	default:
		// Unknown type — forward-compat: emit as system with raw type in content.
		log.Printf("codex: unknown line type %q in %s — treating as system message", raw.Type, path)
		msg.Content = fmt.Sprintf("[%s] %s", raw.Type, raw.Content)
	}

	return msg, nil
}
