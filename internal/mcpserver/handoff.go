package mcpserver

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// contexthealthScoreFiles is a thin alias so the handoff renderer
// can reuse the relevance scorer without taking a hard dependency
// on its package-level naming. Kept tiny on purpose — if the scorer
// signature ever changes, this is the one place to retarget.
func contexthealthScoreFiles(msgs []*connectors.Message) contexthealth.RelevanceVerdict {
	return contexthealth.ScoreFiles(msgs)
}

// handoffMaxRecentTurns caps how many of the latest user/assistant
// turns we include verbatim in the handoff. Enough for the new
// session to have continuity with the recent conversation, small
// enough to keep the handoff prompt itself well under any context
// window the user is likely to paste it into.
const handoffMaxRecentTurns = 6

// handoffMaxRecentTurnsScoped is the deeper window used when scope
// is "current-topic" — the new session is replacing the old one
// because the topic shifted, so it benefits from extra recent
// context to pick up the new direction without the old framing.
const handoffMaxRecentTurnsScoped = 10

// HandoffScope is the typed wrapper for HandoffInput.Scope.
type HandoffScope string

const (
	HandoffScopeFull         HandoffScope = "full"
	HandoffScopeCurrentTopic HandoffScope = "current-topic"
)

// parseHandoffScope normalises an input string into a HandoffScope.
// Empty strings, "full", and unknown values all fall through to
// HandoffScopeFull so a typo never breaks the existing surface.
func parseHandoffScope(s string) HandoffScope {
	switch s {
	case string(HandoffScopeCurrentTopic):
		return HandoffScopeCurrentTopic
	default:
		return HandoffScopeFull
	}
}

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
	// Scope controls which files and exchanges land in the handoff:
	//   "" or "full"          — every file the session touched plus
	//                            the recent exchanges (default).
	//   "current-topic"       — only files whose anchor matches the
	//                            user's most recent direction (per
	//                            contexthealth.ScoreFiles), and the
	//                            last 10 user/assistant exchanges
	//                            instead of the default 5.
	Scope string `json:"scope,omitempty" jsonschema:"full (default) | current-topic — current-topic carries forward only files relevant to the user's current direction"`
}

// HandoffOutput carries the generated handoff Markdown plus enough
// context for the AI to cite its provenance to the user.
type HandoffOutput struct {
	SessionID    string `json:"session_id,omitempty" jsonschema:"the session id the handoff was generated from"`
	Path         string `json:"path,omitempty" jsonschema:"absolute path of the source transcript"`
	ProjectPath  string `json:"project_path,omitempty" jsonschema:"absolute project directory the session ran in"`
	Markdown     string `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target) — the generated handoff prompt, the ambiguous candidates list, or the no-session message"`
	TokensSource int64  `json:"tokens_source,omitempty" jsonschema:"approximate cache-aware token size of the source session at the latest assistant turn"`
	Ambiguous    bool   `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates   []CandidateRow `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// RenderHandoff is the exported entry point used by the docs/proof
// tests (and any external consumer that wants to render a handoff
// from a snapshot they already loaded). Delegates to renderHandoff
// in HandoffScopeFull mode so existing callers do not need to know
// about the scope flag.
func RenderHandoff(snap *SessionSnapshot) string {
	return renderHandoff(snap, HandoffScopeFull)
}

// RenderScopedHandoff renders the handoff under the given scope.
// HandoffScopeCurrentTopic restricts the "Files touched" section
// to files whose relevance score (per contexthealth.ScoreFiles)
// exceeds the threshold, and bumps the recent-exchanges window
// from handoffMaxRecentTurns to handoffMaxRecentTurnsScoped.
func RenderScopedHandoff(snap *SessionSnapshot, scope HandoffScope) string {
	return renderHandoff(snap, scope)
}

// renderHandoff turns a SessionSnapshot into the deterministic
// Markdown handoff. Pure function — no I/O — so callers (tests,
// future code that wants to re-render from cached snapshots) can
// reuse it without paying the JSONL re-scan cost.
func renderHandoff(snap *SessionSnapshot, scope HandoffScope) string {
	var b strings.Builder

	projectPath := projectPathFromMessages(snap.Messages)
	if projectPath == "" {
		projectPath = "(unknown — no cwd-bearing message in transcript)"
	}

	fmt.Fprintf(&b, "# Handoff from session `%s`\n\n", short(snap.SessionID))
	if scope == HandoffScopeCurrentTopic {
		b.WriteString("_Scope: current-topic — only files relevant to the user's most recent direction are carried forward._\n\n")
	}
	fmt.Fprintf(&b, "We are working in `%s`.\n\n", projectPath)

	if topic := recentTopic(snap.Messages); topic != "" {
		fmt.Fprintf(&b, "## Recent task\n\n%s\n\n", topic)
	}

	relevantPaths := relevantPathSet(snap.Messages, scope)
	if files := filesTouchedFiltered(snap.Messages, relevantPaths); len(files) > 0 {
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

	maxTurns := handoffMaxRecentTurns
	if scope == HandoffScopeCurrentTopic {
		maxTurns = handoffMaxRecentTurnsScoped
	}
	if recent := recentTurnsLimited(snap.Messages, maxTurns); len(recent) > 0 {
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

// recentTurns returns the last handoffMaxRecentTurns user/assistant
// messages, capped per-message at handoffMessagePreview characters.
// Kept for callers that don't want to specify a limit.
func recentTurns(msgs []*connectors.Message) []recentTurn {
	return recentTurnsLimited(msgs, handoffMaxRecentTurns)
}

// recentTurnsLimited is recentTurns with a caller-supplied cap so
// the scoped handoff can pull a deeper window than the default.
// Returns chronological-order (oldest first) entries.
func recentTurnsLimited(msgs []*connectors.Message, maxTurns int) []recentTurn {
	if maxTurns <= 0 {
		return nil
	}
	var picked []*connectors.Message
	for i := len(msgs) - 1; i >= 0 && len(picked) < maxTurns; i-- {
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

// relevantPathSet returns the set of file paths the handoff should
// include under the given scope. nil for HandoffScopeFull (no
// filtering); a populated set of relevant paths for HandoffScopeCurrentTopic.
//
// Empty set on current-topic falls back to "no files" — the caller's
// filesTouchedFiltered will then return an empty slice and the
// "Files touched" section is omitted, signalling clearly that no
// loaded file is currently relevant.
func relevantPathSet(msgs []*connectors.Message, scope HandoffScope) map[string]bool {
	if scope != HandoffScopeCurrentTopic {
		return nil
	}
	verdict := contexthealthScoreFiles(msgs)
	out := map[string]bool{}
	for _, f := range verdict.Files {
		if !f.Stale {
			out[f.Path] = true
		}
	}
	return out
}

// filesTouchedFiltered is filesTouched with an optional inclusion
// set. When include is nil, behaves identically to filesTouched.
// When include is non-nil, only paths present in the set are
// returned.
func filesTouchedFiltered(msgs []*connectors.Message, include map[string]bool) []touchedFile {
	all := filesTouched(msgs)
	if include == nil {
		return all
	}
	out := make([]touchedFile, 0, len(all))
	for _, f := range all {
		if include[f.Path] {
			out = append(out, f)
		}
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

// ticketRegex matches a Linear/Jira/GitHub-style key like CLI-1362.
// At least two uppercase letters keeps single-letter false positives
// (A-1, X-3) out of the results.
var ticketRegex = regexp.MustCompile(`\b[A-Z]{2,}-\d+\b`)

// urlRegex pulls pasted http(s) URLs out of message text. Trailing
// punctuation is trimmed after the match to avoid swallowing
// sentence-end punctuation into the URL.
var urlRegex = regexp.MustCompile(`https?://[^\s<>"']+`)

// ticketMinMentions is the minimum user-turn occurrence count a
// ticket key must clear to land in LikelyTickets via mentions
// alone. Keys appearing inside a pasted URL bypass the threshold
// (FromURL=true qualifies independently).
const ticketMinMentions = 2

// extractTicketHints scans only user-role messages for the ticket
// regex, plus all user-message URLs for embedded keys. Returns
// keys that either meet ticketMinMentions OR appear inside any URL.
// Sort is stable by Key so callers can rely on deterministic order.
func extractTicketHints(msgs []*connectors.Message) []TicketHint {
	inURL := map[string]bool{}
	for _, m := range msgs {
		if m == nil || m.Role != connectors.RoleUser {
			continue
		}
		for _, u := range urlRegex.FindAllString(m.Content, -1) {
			for _, k := range ticketRegex.FindAllString(u, -1) {
				inURL[k] = true
			}
		}
	}
	// Prose mentions exclude substrings that appear inside URLs.
	proseOnly := map[string]int{}
	for _, m := range msgs {
		if m == nil || m.Role != connectors.RoleUser {
			continue
		}
		stripped := urlRegex.ReplaceAllString(m.Content, "")
		for _, k := range ticketRegex.FindAllString(stripped, -1) {
			proseOnly[k]++
		}
	}
	keys := map[string]bool{}
	for k, c := range proseOnly {
		if c >= ticketMinMentions {
			keys[k] = true
		}
	}
	for k := range inURL {
		keys[k] = true
	}
	out := make([]TicketHint, 0, len(keys))
	for k := range keys {
		out = append(out, TicketHint{
			Key:      k,
			Mentions: proseOnly[k],
			FromURL:  inURL[k],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// extractLinkedURLs returns user-pasted URLs, deduplicated and in
// first-seen order. Trailing common punctuation (commas, periods,
// closing brackets) is stripped because the regex is greedy.
func extractLinkedURLs(msgs []*connectors.Message) []string {
	seen := map[string]bool{}
	var out []string
	trim := func(u string) string {
		for len(u) > 0 {
			last := u[len(u)-1]
			if last == '.' || last == ',' || last == ')' || last == ']' || last == ';' || last == ':' {
				u = u[:len(u)-1]
				continue
			}
			break
		}
		return u
	}
	for _, m := range msgs {
		if m == nil || m.Role != connectors.RoleUser {
			continue
		}
		for _, u := range urlRegex.FindAllString(m.Content, -1) {
			u = trim(u)
			if seen[u] {
				continue
			}
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// planOfRecordPatterns lists the file-path substrings that count
// as "planning documents" for the handoff. Path is lowercased
// before the substring check, so matches are case-insensitive.
var planOfRecordPatterns = []string{
	"/plans/",    // matches docs/superpowers/plans/*.md, docs/.../plans/*.md
	"/research/", // matches docs/research/<topic>/*.md
	"/00-plan.md", // matches any */00-plan.md naming
}

// isPlanPath returns true when the given path should be considered
// a plan-of-record candidate. Lower-cased so case quirks in the
// path don't matter.
func isPlanPath(p string) bool {
	pl := strings.ToLower(p)
	if !strings.HasSuffix(pl, ".md") {
		return false
	}
	for _, pat := range planOfRecordPatterns {
		if strings.Contains(pl, pat) {
			return true
		}
	}
	return false
}

// extractPlanOfRecord walks the snapshot's tool calls and returns
// the *PlanOfRecordRef for the plan-shaped file with the most
// recent Read/Edit touch. Read counts include all touches in the
// session. now is the reference time used to compute the
// LastTouchAgo display string; tests pass a fixed value for
// determinism, production callers pass time.Now().UnixMilli().
func extractPlanOfRecord(msgs []*connectors.Message, now int64) *PlanOfRecordRef {
	type rec struct {
		path      string
		count     int
		lastTouch int64
	}
	byPath := map[string]*rec{}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, tc := range m.ToolCalls {
			if !isReadTool(tc.Name) {
				continue
			}
			p := extractPath(tc.Input)
			if p == "" || !isPlanPath(p) {
				continue
			}
			r, ok := byPath[p]
			if !ok {
				r = &rec{path: p}
				byPath[p] = r
			}
			r.count++
			if m.Ts > r.lastTouch {
				r.lastTouch = m.Ts
			}
		}
	}
	if len(byPath) == 0 {
		return nil
	}
	// Pick the most-recently-touched plan; ties broken alphabetically
	// so the result is deterministic.
	var best *rec
	for _, r := range byPath {
		switch {
		case best == nil:
			best = r
		case r.lastTouch > best.lastTouch:
			best = r
		case r.lastTouch == best.lastTouch && r.path < best.path:
			best = r
		}
	}
	return &PlanOfRecordRef{
		Path:         best.path,
		ReadCount:    best.count,
		LastTouchAgo: formatRelativeAgo(now, best.lastTouch),
	}
}

// formatRelativeAgo renders the gap between now and earlier (both
// epoch-ms) as a short human-readable string: "4m", "1h", "2d".
// Returns "" when earlier is zero or in the future.
func formatRelativeAgo(now, earlier int64) string {
	if earlier <= 0 || earlier > now {
		return ""
	}
	delta := time.Duration(now-earlier) * time.Millisecond
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm", int(delta/time.Minute))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh", int(delta/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(delta/(24*time.Hour)))
	}
}
