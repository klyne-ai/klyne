package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// loadXraySystemMessages reads the Claude Code JSONL file directly and
// synthesizes connectors.Message entries with Role=System for each
// `type=attachment` record in the session.
//
// Claude Code's protocol logs the user's first prompt at submit time, then
// appends the SessionStart context (MCP instructions, skill listings, tool
// catalog, permissions) as `type=attachment` records that arrive AFTER the
// first user message. So scanning only the head of the file misses everything
// — we have to read all attachments end-to-end.
//
// We process every attachment encountered. addedNames/addedBlocks are summed
// into source buckets; removedNames are intentionally ignored — for "what's
// eating my context across this session" the cumulative loaded cost is the
// honest answer (the tokens were paid even if the MCP was later removed).
//
// The synthesized Content is prefixed so AttributeSources's existing prefix
// heuristics ("mcp:", "skill:") classify each row to the right SourceKind.
// Length of Content approximates the real injected payload size so the per-
// source token estimate is meaningful.
func loadXraySystemMessages(path string) []*connectors.Message {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var out []*connectors.Message
	// Track the most recent pending Skill invocation: when an assistant emits
	// `Skill(skill="superpowers:brainstorming")`, the actual skill body lands
	// in the very next user message with isMeta=true (~10KB of markdown).
	// We attribute that body to the skill rather than letting it count as
	// regular conversation context — that's the real per-skill token cost.
	var pendingSkill string

	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var head struct {
			Type       string          `json:"type"`
			Attachment json.RawMessage `json:"attachment,omitempty"`
			IsMeta     bool            `json:"isMeta,omitempty"`
			Message    json.RawMessage `json:"message,omitempty"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}

		switch head.Type {
		case "attachment":
			if len(head.Attachment) == 0 {
				continue
			}
			var att map[string]any
			if err := json.Unmarshal(head.Attachment, &att); err != nil {
				continue
			}
			attType, _ := att["type"].(string)
			out = append(out, synthFromAttachment(attType, att)...)

		case "assistant":
			// Look for assistant Skill tool_use → arms pendingSkill so the
			// next isMeta user message gets attributed to the skill.
			if name := skillFromAssistantLine(head.Message); name != "" {
				pendingSkill = name
			}

		case "user":
			// isMeta user messages right after a Skill tool_use carry the
			// skill body. Attribute their bytes to the pending skill name.
			if !head.IsMeta || pendingSkill == "" {
				continue
			}
			body := userMetaText(head.Message)
			if body == "" {
				pendingSkill = ""
				continue
			}
			out = append(out, &connectors.Message{
				Role:    connectors.RoleSystem,
				Content: "skill: " + sanitizeSourceName(pendingSkill) + "\n" + body,
			})
			pendingSkill = ""
		}
	}
	return out
}

// skillFromAssistantLine returns the skill name when an assistant message
// invokes the Skill tool, or "" otherwise.
func skillFromAssistantLine(messageRaw json.RawMessage) string {
	if len(messageRaw) == 0 {
		return ""
	}
	var m struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(messageRaw, &m); err != nil {
		return ""
	}
	for _, b := range m.Content {
		if t, _ := b["type"].(string); t != "tool_use" {
			continue
		}
		if name, _ := b["name"].(string); name != "Skill" {
			continue
		}
		input, _ := b["input"].(map[string]any)
		if input == nil {
			continue
		}
		if s, ok := input["skill"].(string); ok {
			return s
		}
	}
	return ""
}

// userMetaText extracts the first text block's content from a meta-user message
// payload. Used to fish the skill body bytes out so we can attribute them.
func userMetaText(messageRaw json.RawMessage) string {
	if len(messageRaw) == 0 {
		return ""
	}
	var m struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(messageRaw, &m); err != nil {
		return ""
	}
	for _, b := range m.Content {
		if t, _ := b["type"].(string); t != "text" {
			continue
		}
		if s, _ := b["text"].(string); s != "" {
			return s
		}
	}
	return ""
}

func synthFromAttachment(attType string, att map[string]any) []*connectors.Message {
	switch attType {
	case "mcp_instructions_delta":
		return synthMCPInstructions(att)
	case "skill_listing":
		return synthSkillListing(att)
	case "deferred_tools_delta":
		return synthDeferredTools(att)
	case "command_permissions":
		return synthCommandPermissions(att)
	case "hook_success":
		return synthHookSuccess(att)
	}
	return nil
}

// synthHookSuccess attributes the stdout of a SessionStart / UserPromptSubmit
// hook to its source. The "You have superpowers" injection lands here as a
// hook_success attachment with ~6KB of stdout containing the using-superpowers
// skill body — that's a real context cost the user pays.
//
// We classify by hookName + stdout content: the using-superpowers injection
// goes to "superpowers"; klyne advisor / Stop hook output goes to "klyne hooks";
// anything else falls through to a generic "<hookName>" bucket.
func synthHookSuccess(att map[string]any) []*connectors.Message {
	stdout, _ := att["stdout"].(string)
	if stdout == "" {
		return nil
	}
	hookName, _ := att["hookName"].(string)

	var prefix string
	switch {
	case strings.Contains(stdout, "You have superpowers") || strings.Contains(stdout, "using-superpowers"):
		prefix = "skill: superpowers"
	case strings.Contains(strings.ToLower(hookName), "klyne") ||
		strings.Contains(stdout, "klyne advise") ||
		strings.Contains(stdout, "klyne:"):
		prefix = "klyne: hook-injection"
	default:
		// Use the hook name itself as the source identifier.
		clean := sanitizeSourceName(hookName)
		if clean == "" {
			clean = "anonymous-hook"
		}
		prefix = "hook: " + clean
	}

	// Pad to stdout length so charsToTokens reports the real cost without
	// embedding the raw stdout (which could itself contain rule prefixes
	// that confuse downstream consumers if they ever loosen the classifier).
	header := prefix + "\n"
	padLen := len(stdout) - len(header)
	if padLen < 0 {
		padLen = 0
	}
	return []*connectors.Message{{
		Role:    connectors.RoleSystem,
		Content: header + strings.Repeat(" ", padLen),
	}}
}

func synthMCPInstructions(att map[string]any) []*connectors.Message {
	names := jsonStringList(att["addedNames"])
	blocks := jsonStringList(att["addedBlocks"])
	if len(names) == 0 {
		return nil
	}
	out := make([]*connectors.Message, 0, len(names))
	for i, name := range names {
		var block string
		if i < len(blocks) {
			block = blocks[i]
		}
		content := "mcp: " + sanitizeSourceName(name) + "\n" + block
		out = append(out, &connectors.Message{
			Role:    connectors.RoleSystem,
			Content: content,
		})
	}
	return out
}

func synthSkillListing(att map[string]any) []*connectors.Message {
	content, _ := att["content"].(string)
	if content == "" {
		return nil
	}
	count := jsonInt(att["skillCount"])
	header := fmt.Sprintf("skill: superpowers (%d skills loaded)\n", count)
	return []*connectors.Message{{
		Role:    connectors.RoleSystem,
		Content: header + content,
	}}
}

// synthDeferredTools groups the tool catalog by MCP origin and emits one
// synthetic message per group. Token cost is approximated from the per-tool
// definition lines (addedLines), padded to length so AttributeSources's
// charsToTokens() returns a realistic share.
func synthDeferredTools(att map[string]any) []*connectors.Message {
	names := jsonStringList(att["addedNames"])
	lines := jsonStringList(att["addedLines"])
	if len(names) == 0 {
		return nil
	}
	type group struct {
		bytes int
		count int
	}
	groups := map[string]*group{}
	order := []string{}
	for i, n := range names {
		mcp := mcpFromToolName(n)
		var sz int
		if i < len(lines) {
			sz = len(lines[i])
		} else {
			sz = len(n) + 60
		}
		g, ok := groups[mcp]
		if !ok {
			g = &group{}
			groups[mcp] = g
			order = append(order, mcp)
		}
		g.bytes += sz
		g.count++
	}

	out := make([]*connectors.Message, 0, len(groups))
	for _, mcp := range order {
		g := groups[mcp]
		// Header uses sanitized name so extractSourceName (which cuts at the
		// first whitespace) returns the full source name as a single token.
		// Tool count goes after a newline so it doesn't pollute the name.
		header := fmt.Sprintf("mcp: %s\n%d tools loaded\n", sanitizeSourceName(mcp), g.count)
		padLen := g.bytes - len(header)
		if padLen < 0 {
			padLen = 0
		}
		out = append(out, &connectors.Message{
			Role:    connectors.RoleSystem,
			Content: header + strings.Repeat(" ", padLen),
		})
	}
	return out
}

// sanitizeSourceName replaces whitespace with hyphens so that source names
// containing spaces ("claude.ai Linear", "Anthropic built-ins") survive as a
// single token through AttributeSources.extractSourceName, which truncates at
// the first whitespace character. The display layer renders the resulting
// hyphenated name as-is.
func sanitizeSourceName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "\t", "-")
	return s
}

func synthCommandPermissions(att map[string]any) []*connectors.Message {
	body := fmt.Sprintf("klyne: permissions config\n%v", att["allowedTools"])
	return []*connectors.Message{{
		Role:    connectors.RoleSystem,
		Content: body,
	}}
}

// mcpFromToolName returns a human-readable MCP origin from a Claude Code tool
// name. Examples:
//
//	mcp__klyne__recall                  → "klyne"
//	mcp__plugin_serena_serena__find     → "serena"
//	mcp__claude_ai_Linear__list_issues  → "claude.ai-Linear"
//	Read                                → "anthropic-builtins"
//
// Output is whitespace-free so the synthesized "mcp: <name>" line survives
// AttributeSources's extractSourceName intact.
func mcpFromToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return "anthropic-builtins"
	}
	rest := strings.TrimPrefix(name, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "anthropic-builtins"
	}
	src := parts[0]
	src = strings.TrimPrefix(src, "plugin_")
	src = strings.TrimPrefix(src, "claude_ai_")
	// "serena_serena" → "serena"
	if seg := strings.SplitN(src, "_", 2); len(seg) == 2 && seg[0] == seg[1] {
		src = seg[0]
	}
	// Replace internal _ with - so the name reads naturally without spaces.
	src = strings.ReplaceAll(src, "_", "-")
	src = strings.TrimSpace(src)
	if src == "" {
		return "anthropic-builtins"
	}
	return src
}

func jsonStringList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
