package contexthealth

import (
	"encoding/json"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// source_attribution.go — attribute SessionStart-region system messages to
// their originating source (MCP server, skill, hook, or unknown).
//
// The Claude Code JSONL format embeds system-role messages at the beginning of
// a session (before the first user turn). Each one carries a "source" field or
// a recognisable prefix pattern that identifies what injected the content.
// This file parses those messages with a best-effort heuristic and groups the
// results by source name so the audit CLI can render the "Pre-prompt context"
// section of the scorecard.

// SourceKind categorises what injected a pre-prompt system block.
type SourceKind string

const (
	// SourceKindMCP is content injected by an MCP server (e.g. serena, github).
	SourceKindMCP SourceKind = "mcp_server"
	// SourceKindSkill is content injected by a Superpower / skill.
	SourceKindSkill SourceKind = "skill"
	// SourceKindHook is content injected by a UserPromptSubmit / hook.
	SourceKindHook SourceKind = "hook"
	// SourceKindUnknown is content whose source could not be attributed.
	SourceKindUnknown SourceKind = "unknown"
)

// SourceRow is one entry in the pre-prompt attribution table.
type SourceRow struct {
	// Name is the human-readable source name (e.g. "serena MCP", "superpowers skill").
	Name string
	// Kind is the category of the source.
	Kind SourceKind
	// Tokens is the approximate token count for this source's injected content.
	// We use len(content)/4 as a rough chars-to-tokens estimate; the Claude
	// tokeniser is not available in the pure-function package.
	Tokens int
	// LastUsefulCallAgo is an optional human-readable string like "3d ago" or
	// "never called this session". Empty when not determinable from the messages.
	LastUsefulCallAgo string
}

// AttributeSources walks the SessionStart-region system messages (role=system,
// before the first user turn) from msgs, attributes their content to a source,
// and returns one SourceRow per distinct source, sorted by Tokens desc.
//
// Heuristics used (in priority order):
//  1. JSON field "source" or "injected_by" in the message's raw content.
//  2. Prefix match: "MCP:" / "mcp_server:" → SourceKindMCP.
//  3. Prefix match: "skill:" / "superpower:" → SourceKindSkill.
//  4. Prefix match: "hook:" / "klyne:" → SourceKindHook.
//  5. Fallback: content goes to the "unknown" bucket.
//
// Only messages before the first role=user turn are examined. This is the
// "SessionStart region" where injected system prompts live.
func AttributeSources(msgs []*connectors.Message) []SourceRow {
	// Collect the SessionStart-region system messages.
	var systemMsgs []*connectors.Message
	for _, m := range msgs {
		if m.Role == connectors.RoleUser {
			break
		}
		if m.Role == connectors.RoleSystem {
			systemMsgs = append(systemMsgs, m)
		}
	}
	if len(systemMsgs) == 0 {
		return nil
	}

	// Accumulate bytes per source name.
	type bucket struct {
		kind  SourceKind
		bytes int
	}
	buckets := map[string]*bucket{}
	order := []string{} // insertion order for determinism

	upsert := func(name string, kind SourceKind, bytes int) {
		if b, ok := buckets[name]; ok {
			b.bytes += bytes
		} else {
			buckets[name] = &bucket{kind: kind, bytes: bytes}
			order = append(order, name)
		}
	}

	for _, m := range systemMsgs {
		name, kind := classifySystemMessage(m.Content)
		upsert(name, kind, len(m.Content))
	}

	// Build the output slice (preserves insertion order — largest-first sort
	// is done after).
	rows := make([]SourceRow, 0, len(buckets))
	for _, name := range order {
		b := buckets[name]
		rows = append(rows, SourceRow{
			Name:   name,
			Kind:   b.kind,
			Tokens: charsToTokens(b.bytes),
		})
	}
	sortSourceRows(rows)
	return rows
}

// classifySystemMessage inspects the content of a single system message and
// returns (sourceName, SourceKind). Falls back to ("unknown", SourceKindUnknown)
// when no heuristic matches.
func classifySystemMessage(content string) (string, SourceKind) {
	// Try JSON first — some connectors emit {"injected_by":"serena","type":"mcp",...}.
	if name, kind, ok := parseJSONSource(content); ok {
		return name, kind
	}

	lower := strings.ToLower(strings.TrimSpace(content))

	// Prefix heuristics.
	type rule struct {
		prefix string
		kind   SourceKind
		label  string // optional fixed label; empty → extract from prefix
	}
	rules := []rule{
		{"mcp:", SourceKindMCP, ""},
		{"mcp_server:", SourceKindMCP, ""},
		{"[mcp]", SourceKindMCP, ""},
		{"skill:", SourceKindSkill, ""},
		{"superpower:", SourceKindSkill, ""},
		{"[skill]", SourceKindSkill, ""},
		{"hook:", SourceKindHook, ""},
		{"klyne:", SourceKindHook, "klyne hooks"},
		{"[hook]", SourceKindHook, ""},
		{"anthropic hooks", SourceKindHook, "anthropic hooks"},
	}
	for _, r := range rules {
		if strings.HasPrefix(lower, r.prefix) || strings.Contains(lower, r.prefix) {
			if r.label != "" {
				return r.label, r.kind
			}
			name := extractSourceName(content, r.prefix)
			if name == "" {
				name = kindDefaultName(r.kind)
			}
			return name, r.kind
		}
	}

	// Well-known MCP server names embedded anywhere in the first line.
	firstLine := firstNonEmptyLine(content)
	lowerFirst := strings.ToLower(firstLine)
	mcpNames := []string{"serena", "github", "linear", "slack", "playwright", "context7"}
	for _, n := range mcpNames {
		if strings.Contains(lowerFirst, n) {
			return n + " MCP", SourceKindMCP
		}
	}
	skillNames := []string{"superpowers", "klyne-health", "klyne:health"}
	for _, n := range skillNames {
		if strings.Contains(lowerFirst, n) {
			return n + " skill", SourceKindSkill
		}
	}

	return "unknown", SourceKindUnknown
}

// parseJSONSource attempts to read a JSON object from content and look for
// "injected_by", "source", or "mcp_server" fields that identify the origin.
func parseJSONSource(content string) (name string, kind SourceKind, ok bool) {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", "", false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return "", "", false
	}
	// Try fields in priority order.
	for _, key := range []string{"injected_by", "source", "mcp_server", "skill"} {
		v, exists := raw[key]
		if !exists {
			continue
		}
		s, isStr := v.(string)
		if !isStr || s == "" {
			continue
		}
		// Determine kind from a sibling "type" field if present.
		kind = kindFromTypeField(raw)
		if kind == "" {
			kind = kindFromName(s)
		}
		return s, kind, true
	}
	return "", "", false
}

// kindFromTypeField reads a "type" or "kind" field from the JSON object and
// maps it to a SourceKind.
func kindFromTypeField(raw map[string]any) SourceKind {
	for _, key := range []string{"type", "kind"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		switch strings.ToLower(s) {
		case "mcp", "mcp_server":
			return SourceKindMCP
		case "skill", "superpower":
			return SourceKindSkill
		case "hook":
			return SourceKindHook
		}
	}
	return ""
}

// kindFromName infers a SourceKind from the source name string using
// recognisable suffixes / substrings.
func kindFromName(name string) SourceKind {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "mcp") || strings.Contains(lower, "mcp_server"):
		return SourceKindMCP
	case strings.HasSuffix(lower, "skill") || strings.HasSuffix(lower, "superpower"):
		return SourceKindSkill
	case strings.HasSuffix(lower, "hook"):
		return SourceKindHook
	default:
		return SourceKindUnknown
	}
}

// kindDefaultName returns a generic label for each SourceKind.
func kindDefaultName(k SourceKind) string {
	switch k {
	case SourceKindMCP:
		return "unknown MCP"
	case SourceKindSkill:
		return "unknown skill"
	case SourceKindHook:
		return "unknown hook"
	default:
		return "unknown"
	}
}

// extractSourceName strips the leading prefix from content and returns the
// next word/identifier as the source name. Falls back to "" when the
// content after the prefix is empty.
func extractSourceName(content, prefix string) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, prefix)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(content[idx+len(prefix):])
	// Take up to the first whitespace or punctuation.
	end := strings.IndexAny(rest, " \t\n\r:,")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// firstNonEmptyLine returns the first non-blank line from content, trimmed.
func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// charsToTokens converts a byte count to an approximate token count using the
// widely-cited 4 chars ≈ 1 token heuristic. Rounds up to avoid reporting 0
// tokens for small snippets.
func charsToTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

// sortSourceRows sorts rows by Tokens descending, Name ascending on ties.
// Small slice — insertion sort is fine.
func sortSourceRows(rows []SourceRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if a.Tokens > b.Tokens || (a.Tokens == b.Tokens && a.Name <= b.Name) {
				break
			}
			rows[j-1], rows[j] = b, a
		}
	}
}
