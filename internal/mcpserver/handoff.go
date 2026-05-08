package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// handoffMaxRecentTurns caps how many of the latest user/assistant
// turns we include verbatim in the handoff. Enough for the new
// session to have continuity with the recent conversation, small
// enough to keep the handoff prompt itself well under any context
// window the user is likely to paste it into.
const handoffMaxRecentTurns = 6

// handoffMaxFiles caps the "files touched" list. Past this point the
// list becomes noise; the user can always inspect the source JSONL.
const handoffMaxFiles = 20

// handoffMaxCommands caps the "commands run" list with the same
// reasoning.
const handoffMaxCommands = 12

// handoffMessagePreview truncates per-message text included verbatim
// in the recent-exchanges section. Long enough to convey intent,
// short enough that one verbose tool-result blob can't dominate.
const handoffMessagePreview = 400

// HandoffInput is the input schema for generate_handoff.
type HandoffInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
}

// HandoffOutput carries the generated handoff Markdown plus enough
// context for the AI to cite its provenance to the user.
type HandoffOutput struct {
	SessionID    string `json:"session_id,omitempty" jsonschema:"the session id the handoff was generated from"`
	Path         string `json:"path,omitempty" jsonschema:"absolute path of the source transcript"`
	ProjectPath  string `json:"project_path,omitempty" jsonschema:"absolute project directory the session ran in"`
	Markdown     string `json:"markdown,omitempty" jsonschema:"the generated handoff prompt — paste into a fresh Claude Code session"`
	TokensSource int64  `json:"tokens_source,omitempty" jsonschema:"approximate cache-aware token size of the source session at the latest assistant turn"`
	Ambiguous    bool   `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates   []CandidateRow `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// RenderHandoff is the exported entry point used by the docs/proof
// tests (and any external consumer that wants to render a handoff
// from a snapshot they already loaded). Delegates to renderHandoff
// so the internal call sites stay unchanged.
func RenderHandoff(snap *SessionSnapshot) string {
	return renderHandoff(snap)
}

// renderHandoff turns a SessionSnapshot into the deterministic
// Markdown handoff. Pure function — no I/O — so callers (tests,
// future code that wants to re-render from cached snapshots) can
// reuse it without paying the JSONL re-scan cost.
func renderHandoff(snap *SessionSnapshot) string {
	var b strings.Builder

	projectPath := projectPathFromMessages(snap.Messages)
	if projectPath == "" {
		projectPath = "(unknown — no cwd-bearing message in transcript)"
	}

	fmt.Fprintf(&b, "# Handoff from session `%s`\n\n", short(snap.SessionID))
	fmt.Fprintf(&b, "We are working in `%s`.\n\n", projectPath)

	if topic := recentTopic(snap.Messages); topic != "" {
		fmt.Fprintf(&b, "## Recent task\n\n%s\n\n", topic)
	}

	if files := filesTouched(snap.Messages); len(files) > 0 {
		b.WriteString("## Files touched\n\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- `%s`%s\n", f.Path, occurrenceSuffix(f.Count))
		}
		b.WriteString("\n")
	}

	if cmds := commandsRun(snap.Messages); len(cmds) > 0 {
		b.WriteString("## Commands run\n\n")
		for _, c := range cmds {
			fmt.Fprintf(&b, "- `%s`%s\n", c.Stem, occurrenceSuffix(c.Count))
		}
		b.WriteString("\n")
	}

	if fails := recentFailures(snap.Messages); len(fails) > 0 {
		b.WriteString("## Known failures\n\n")
		for _, f := range fails {
			fmt.Fprintf(&b, "- %s\n", oneLine(f))
		}
		b.WriteString("\n")
	}

	if recent := recentTurns(snap.Messages); len(recent) > 0 {
		b.WriteString("## Last few exchanges\n\n")
		for _, t := range recent {
			fmt.Fprintf(&b, "**%s** — %s\n\n", t.Role, oneLine(t.Body))
		}
	}

	fmt.Fprintf(&b, "---\n_Source: `%s`_\n", snap.Path)
	return b.String()
}

// projectPathFromMessages returns the first non-empty Cwd or
// ProjectPath value across the snapshot's messages. Empty when none
// of the rows carries one (rare — most Claude Code messages do).
func projectPathFromMessages(msgs []*connectors.Message) string {
	for _, m := range msgs {
		if m.Cwd != "" {
			return m.Cwd
		}
		if m.ProjectPath != "" {
			return m.ProjectPath
		}
	}
	return ""
}

// recentTopic extracts the most-recent user message's content as a
// proxy for "what is the user trying to do." Truncated to fit the
// handoff prompt without dominating it.
func recentTopic(msgs []*connectors.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == connectors.RoleUser && strings.TrimSpace(msgs[i].Content) != "" {
			return truncatePreview(strings.TrimSpace(msgs[i].Content))
		}
	}
	return ""
}

// touchedFile is one file-read attribution row with a reused-count.
type touchedFile struct {
	Path  string
	Count int
}

// filesTouched aggregates Read/Edit/View tool calls across the
// snapshot, sorts by reuse count desc (the most-frequent reads are
// likely the ones the next session also wants context on), and
// caps at handoffMaxFiles.
func filesTouched(msgs []*connectors.Message) []touchedFile {
	counts := map[string]int{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if !isReadTool(tc.Name) {
				continue
			}
			path := extractPath(tc.Input)
			if path == "" {
				continue
			}
			counts[path]++
		}
	}
	rows := make([]touchedFile, 0, len(counts))
	for p, c := range counts {
		rows = append(rows, touchedFile{Path: p, Count: c})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Path < rows[j].Path
	})
	if len(rows) > handoffMaxFiles {
		rows = rows[:handoffMaxFiles]
	}
	return rows
}

// commandRow is one shell-command attribution.
type commandRow struct {
	Stem  string
	Count int
}

// commandsRun aggregates Bash tool calls by command stem (e.g.
// "go test"), sorts by count desc, caps at handoffMaxCommands.
func commandsRun(msgs []*connectors.Message) []commandRow {
	counts := map[string]int{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if !isShellTool(tc.Name) {
				continue
			}
			stem := commandStem(extractCommand(tc.Input))
			if stem == "" {
				continue
			}
			counts[stem]++
		}
	}
	rows := make([]commandRow, 0, len(counts))
	for s, c := range counts {
		rows = append(rows, commandRow{Stem: s, Count: c})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Stem < rows[j].Stem
	})
	if len(rows) > handoffMaxCommands {
		rows = rows[:handoffMaxCommands]
	}
	return rows
}

// recentFailures pulls the most recent N tool-result rows whose
// IsError flag is set. Truncated per-row so a single huge stack
// trace doesn't dominate the handoff.
func recentFailures(msgs []*connectors.Message) []string {
	const maxFailures = 5
	var out []string
	// Walk backward so we collect newest-first.
	for i := len(msgs) - 1; i >= 0 && len(out) < maxFailures; i-- {
		for _, tr := range msgs[i].ToolResults {
			if !tr.IsError {
				continue
			}
			out = append(out, truncatePreview(tr.Output))
			if len(out) >= maxFailures {
				break
			}
		}
	}
	return out
}

// recentTurn is one user/assistant exchange row for the handoff.
type recentTurn struct {
	Role string
	Body string
}

// recentTurns returns the last N user/assistant messages in
// chronological order (oldest first), capped per-message at
// handoffMessagePreview characters.
func recentTurns(msgs []*connectors.Message) []recentTurn {
	var picked []*connectors.Message
	for i := len(msgs) - 1; i >= 0 && len(picked) < handoffMaxRecentTurns; i-- {
		m := msgs[i]
		if m.Role != connectors.RoleUser && m.Role != connectors.RoleAssistant {
			continue
		}
		body := strings.TrimSpace(m.Content)
		if body == "" {
			continue
		}
		picked = append(picked, m)
	}
	// picked is newest-first; reverse for chronological output.
	out := make([]recentTurn, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- {
		body := strings.TrimSpace(picked[i].Content)
		if len(body) > handoffMessagePreview {
			body = body[:handoffMessagePreview] + "…"
		}
		out = append(out, recentTurn{
			Role: string(picked[i].Role),
			Body: body,
		})
	}
	return out
}

// short trims a long uuid down to its first 8 hex chars for human
// readability. "" → "".
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// oneLine collapses internal newlines/tabs to single spaces so each
// item in a Markdown list stays on one rendered line.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > handoffMessagePreview {
		s = s[:handoffMessagePreview] + "…"
	}
	return s
}

// occurrenceSuffix produces "" for n==1 and " (×N)" otherwise.
func occurrenceSuffix(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf(" (×%d)", n)
}

// --- shared helpers ported from contexthealth so the handoff renderer
//     stays self-contained. Duplicating these tiny functions is
//     cheaper than introducing a circular package dependency or a new
//     shared utility module just for two helpers.

// isReadTool returns true for Claude Code tools that load file
// content into the conversation context.
func isReadTool(name string) bool {
	switch strings.ToLower(name) {
	case "read", "view", "cat", "open", "edit", "multiedit":
		return true
	}
	return false
}

// isShellTool returns true for Claude Code tools that execute shell
// commands.
func isShellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "sh", "exec", "run":
		return true
	}
	return false
}

// extractPath pulls the file path out of a Read/Edit tool call's JSON
// input. Mirrors contexthealth.extractPath; kept local to avoid
// importing the classifier just for one helper.
func extractPath(input string) string {
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

// extractCommand pulls the shell command out of a Bash tool call's
// JSON input.
func extractCommand(input string) string {
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

// commandStem returns a normalised shorthand for a shell command,
// matching the contexthealth classifier so bloat groupings and
// handoff "commands run" sections agree on labels.
func commandStem(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	parts := strings.Fields(cmd)
	switch {
	case len(parts) == 1:
		return parts[0]
	case strings.HasPrefix(parts[1], "-"):
		return parts[0]
	default:
		return parts[0] + " " + parts[1]
	}
}
