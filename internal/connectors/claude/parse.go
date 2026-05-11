// Package claude implements the Claude Code connector for klyne.
//
// # Parse
//
// Parse converts a single raw JSONL line from a Claude Code session file into
// the canonical *connectors.Message. It is the canonical reader for Claude
// JSONL; W12 and the store layer build on top of it.
//
// # Tool-use representation decision
//
// Claude Code lines with type="assistant" and stop_reason="tool_use" carry a
// message.content array whose elements are {"type":"tool_use",...} objects.
// W4 emits ONE Message per JSONL line (not one per tool call), so tool_use
// entries are collected into Message.ToolCalls on the assistant Message.  The
// role is kept as RoleAssistant — the caller can inspect ToolCalls to detect
// tool-use.  A separate tool_result user-line is emitted as a Message with
// role=RoleTool and its results collected in Message.ToolResults.
//
// Rationale: The store schema has a single-row-per-message design (messages
// table with tool_name scalar + tool_calls/tool_results as JSON). Emitting one
// Message per line matches 1-to-1 and avoids the need to re-aggregate on read.
//
// # Forward compatibility
//
// All unmarshalling uses json.RawMessage for fields whose shape may evolve
// (e.g. message.content). Unknown top-level fields are silently dropped via
// the permissive struct tags. Malformed lines cause Parse to return a non-nil
// error; the caller (Watch loop) logs a warning and skips the line — it never
// panics.
package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// ── Raw wire types ────────────────────────────────────────────────────────────
// These structs mirror the on-disk JSON exactly. They are unexported because
// only parse.go needs them; callers receive *connectors.Message.

// rawLine is the top-level object in a Claude Code JSONL file.
type rawLine struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  string          `json:"timestamp"`
	CWD        string          `json:"cwd"`
	GitBranch  string          `json:"gitBranch"`
	Version    string          `json:"version"`
	Subtype    string          `json:"subtype"`
	// Message is present on type=user and type=assistant lines.
	Message json.RawMessage `json:"message"`
	// Summary fields (type="summary")
	Summary  string `json:"summary"`
	LeafUUID string `json:"leafUuid"`
	// Attachment is present on type="attachment" lines. The shape
	// klyne consumes today is hook_additional_context (UserPromptSubmit
	// hook injections); other attachment.type values are silently
	// skipped so we stay tolerant of future shapes.
	Attachment *rawAttachment `json:"attachment,omitempty"`
}

// rawAttachment captures the inline payload Claude Code stores for
// type="attachment" lines. The interesting case for klyne is
// `type == "hook_additional_context"` which is what the
// UserPromptSubmit hook produces.
type rawAttachment struct {
	Type      string   `json:"type"`
	Content   []string `json:"content"`
	HookName  string   `json:"hookName"`
	HookEvent string   `json:"hookEvent"`
}

// rawMessage is the nested "message" object on user/assistant lines.
type rawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      *rawUsage       `json:"usage"`
}

// rawUsage holds token counts reported by the assistant.
type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// rawContentItem covers both text and tool_use / tool_result entries in
// message.content arrays.
type rawContentItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	// tool_use fields
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result fields
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // may be string or array
	IsError   bool            `json:"is_error"`
}

// ── Parse ─────────────────────────────────────────────────────────────────────

// Parse converts one raw JSONL line into a canonical Message.
//
// path is the absolute path of the source file; it is used to derive
// SessionID when the payload's sessionId field is absent (unlikely but
// tolerated) and to set CLI + ProjectPath.
//
// Returns (nil, err) for malformed JSON or lines that should be skipped
// (e.g. unknown types with no useful payload). The caller MUST log and skip
// on error rather than halt.
func Parse(line []byte, path string) (*connectors.Message, error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("claude parse: invalid JSON: %w", err)
	}

	ts, err := parseTimestamp(raw.Timestamp)
	if err != nil {
		// Fallback to now if timestamp is missing/unparseable
		ts = time.Now().UnixMilli()
	}

	// ProjectPath comes from the "cwd" field when present.
	projectPath := raw.CWD

	msg := &connectors.Message{
		ID:          raw.UUID,
		SessionID:   raw.SessionID,
		CLI:         connectors.CLIClaude,
		ProjectPath: projectPath,
		Ts:          ts,
		ParentUUID:  normalizeParentUUID(raw.ParentUUID),
		// gitBranch + cwd let the cockpit page tell parallel
		// `claude --resume <id>` invocations apart when they share
		// a sessionId but run from different worktrees / dirs.
		GitBranch: raw.GitBranch,
		Cwd:       raw.CWD,
	}

	switch raw.Type {
	case "system":
		return parseSystem(msg, raw), nil

	case "user":
		return parseUser(msg, raw)

	case "assistant":
		return parseAssistant(msg, raw)

	case "summary":
		return parseSummary(msg, raw), nil

	case "attachment":
		// Hook-injected context (e.g. UserPromptSubmit additions
		// from klyne advise) lands here with attachment.type =
		// "hook_additional_context". The advisor text is the
		// payload that the user typically wants to surface later
		// via search — without parsing these rows, klyne's index
		// never sees its own advisories. Returns nil for
		// non-hook attachments (images, etc.) so the existing
		// "skip silently" behaviour stays unchanged for shapes
		// we do not consume yet.
		return parseAttachment(msg, raw)

	default:
		return nil, fmt.Errorf("claude parse: unknown type %q", raw.Type)
	}
}

// ── type-specific parsers ─────────────────────────────────────────────────────

func parseSystem(msg *connectors.Message, raw rawLine) *connectors.Message {
	msg.Role = connectors.RoleSystem
	// message field on system lines is a plain string (not an object).
	// Extract it as a raw string if possible.
	var txt string
	_ = json.Unmarshal(raw.Message, &txt)
	msg.Content = txt
	return msg
}

func parseUser(msg *connectors.Message, raw rawLine) (*connectors.Message, error) {
	var m rawMessage
	if err := json.Unmarshal(raw.Message, &m); err != nil {
		return nil, fmt.Errorf("claude parse: user message decode: %w", err)
	}

	// Determine if this is a tool_result user line or a plain user message.
	items, err := decodeContent(m.Content)
	if err != nil {
		return nil, err
	}

	var toolResults []connectors.ToolResult
	var textParts []string

	for _, item := range items {
		switch item.Type {
		case "tool_result":
			tr := connectors.ToolResult{
				ID:      item.ToolUseID,
				IsError: item.IsError,
				Output:  extractToolResultContent(item.Content),
			}
			toolResults = append(toolResults, tr)
		case "text":
			textParts = append(textParts, item.Text)
		default:
			// plain string content decoded as text
			textParts = append(textParts, item.Text)
		}
	}

	if len(toolResults) > 0 {
		msg.Role = connectors.RoleTool
		msg.ToolResults = toolResults
		if len(textParts) > 0 {
			msg.Content = strings.Join(textParts, "\n")
		}
	} else {
		msg.Role = connectors.RoleUser
		if len(textParts) > 0 {
			msg.Content = strings.Join(textParts, "\n")
		} else {
			// plain string content (not an array)
			var s string
			if err2 := json.Unmarshal(m.Content, &s); err2 == nil {
				msg.Content = s
			}
		}
	}
	return msg, nil
}

func parseAssistant(msg *connectors.Message, raw rawLine) (*connectors.Message, error) {
	var m rawMessage
	if err := json.Unmarshal(raw.Message, &m); err != nil {
		return nil, fmt.Errorf("claude parse: assistant message decode: %w", err)
	}

	msg.Role = connectors.RoleAssistant
	msg.Model = m.Model
	if m.ID != "" {
		msg.ID = m.ID
	}
	// Keep raw.UUID as fallback ID set above; override if message has its own.
	if raw.UUID != "" {
		msg.ID = raw.UUID
	}

	if m.Usage != nil {
		// Claude reports the prompt token count split into three buckets:
		// input_tokens (the fresh portion this turn), cache_read_input_tokens
		// (served from prompt cache), and cache_creation_input_tokens (newly
		// written cache entries). Real Claude Code sessions lean heavily on
		// the prompt cache, so dropping the cached buckets undercounts
		// prompt tokens by 99%+. TokensIn is the canonical TOTAL across all
		// three buckets; the cached subsets are reported separately so the
		// cost engine can apply differentiated rates.
		msg.TokensIn = m.Usage.InputTokens +
			m.Usage.CacheReadInputTokens +
			m.Usage.CacheCreationInputTokens
		msg.TokensOut = m.Usage.OutputTokens
		msg.CachedReadTokens = m.Usage.CacheReadInputTokens
		msg.CachedWriteTokens = m.Usage.CacheCreationInputTokens
	}

	items, err := decodeContent(m.Content)
	if err != nil {
		return nil, err
	}

	var textParts []string
	var toolCalls []connectors.ToolCall

	for _, item := range items {
		switch item.Type {
		case "text":
			textParts = append(textParts, item.Text)
		case "tool_use":
			inputStr := ""
			if len(item.Input) > 0 {
				inputStr = string(item.Input)
			}
			toolCalls = append(toolCalls, connectors.ToolCall{
				ID:    item.ID,
				Name:  item.Name,
				Input: inputStr,
			})
		}
	}

	msg.Content = strings.Join(textParts, "\n")
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return msg, nil
}

func parseSummary(msg *connectors.Message, raw rawLine) *connectors.Message {
	msg.Role = connectors.RoleSystem
	msg.Content = raw.Summary
	// LeafUUID is the last assistant uuid before compaction; store as ParentUUID
	// so the store layer can link the summary to the compacted conversation end.
	if raw.LeafUUID != "" {
		msg.ParentUUID = raw.LeafUUID
	}
	return msg
}

// parseAttachment turns Claude Code's type="attachment" JSONL line
// into a canonical Message when the attachment carries searchable
// text content. The only shape currently consumed is
// hook_additional_context — what the UserPromptSubmit hook
// produces (klyne advise injects its inline advisories through
// this surface). Other attachment shapes (image blobs, large
// pasted artifacts, etc.) return (nil, nil) so the per-line
// caller silently skips them without flagging an error.
//
// The recovered text is stored under role=system so it lands in
// the SQLite index and shows up in cross-session search, but
// is not double-counted as a user/assistant turn for tokens.
// Hook attribution lives in Content's leading "klyne: " prefix
// (the only producer today) and in the original JSONL row's
// attachment.hookName which is preserved on the structured side
// for any future surface that wants to filter by hook.
func parseAttachment(msg *connectors.Message, raw rawLine) (*connectors.Message, error) {
	if raw.Attachment == nil {
		return nil, nil
	}
	switch raw.Attachment.Type {
	case "hook_additional_context":
		if len(raw.Attachment.Content) == 0 {
			return nil, nil
		}
		msg.Role = connectors.RoleSystem
		msg.Content = strings.Join(raw.Attachment.Content, "\n")
		return msg, nil
	default:
		// Unknown attachment.type (image, binary, future shape).
		// Silently skip — same tolerance the parser applies to any
		// future field it does not consume yet.
		return nil, nil
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// decodeContent parses message.content, which may be:
//  - a JSON string (plain user text, old format)
//  - a JSON array of content items
//  - null / absent
func decodeContent(raw json.RawMessage) ([]rawContentItem, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Try array first
	if len(raw) > 0 && raw[0] == '[' {
		var items []rawContentItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("claude parse: content array decode: %w", err)
		}
		return items, nil
	}

	// Try plain string
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("claude parse: content string decode: %w", err)
		}
		return []rawContentItem{{Type: "text", Text: s}}, nil
	}

	return nil, nil
}

// extractToolResultContent gets the string output from a tool_result's Content
// field, which may be a plain string or an array of content items.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// plain string
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// array of items — concatenate text fields
	var items []rawContentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		var parts []string
		for _, it := range items {
			if it.Text != "" {
				parts = append(parts, it.Text)
			} else if it.Type == "text" {
				parts = append(parts, it.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// parseTimestamp parses ISO 8601 with milliseconds (e.g. 2026-05-06T10:00:00.000Z)
// and returns epoch-milliseconds.
func parseTimestamp(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Try without sub-second
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return 0, fmt.Errorf("cannot parse timestamp %q: %w", s, err)
		}
	}
	return t.UnixMilli(), nil
}

// normalizeParentUUID converts the literal string "null" (as written by Claude
// Code for root messages) to an empty string.
func normalizeParentUUID(s string) string {
	if s == "null" {
		return ""
	}
	return s
}
