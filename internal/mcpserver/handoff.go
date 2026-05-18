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

// handoffMessagePreview truncates per-message text included verbatim
// in the handoff sections. Long enough to convey intent, short enough
// that one verbose tool-result blob can't dominate.
const handoffMessagePreview = 400

// handoffMaxBlockers caps the rendered blockers list. Three is
// enough signal for the next session to triage; more becomes noise.
const handoffMaxBlockers = 3

// HandoffInput is the input schema for generate_handoff.
type HandoffInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
	// Scope is accepted for back-compat with pre-v2 callers but
	// ignored by the renderer. The v2 skeleton already filters anchor
	// files via the relevance verdict, making the old "current-topic"
	// mode redundant. Field removal scheduled for v3.
	Scope string `json:"scope,omitempty" jsonschema:"DEPRECATED — accepted but ignored as of v2; anchor files always use relevance-verdict filtering"`
}

// HandoffOutput carries the generated handoff plus structured
// fields the slashcommand uses to drive the in-session model's
// narrative authoring (or to suppress it when post-compact).
type HandoffOutput struct {
	SessionID    string `json:"session_id,omitempty" jsonschema:"the session id the handoff was generated from"`
	Path         string `json:"path,omitempty" jsonschema:"absolute path of the source transcript"`
	ProjectPath  string `json:"project_path,omitempty" jsonschema:"absolute project directory the session ran in"`
	Markdown     string `json:"markdown" jsonschema:"deterministic skeleton render — also a valid standalone handoff for programmatic callers and post-compact mode"`
	TokensSource int64  `json:"tokens_source,omitempty" jsonschema:"approximate cache-aware token size of the source session at the latest assistant turn"`

	// Skeleton is the structured form of Markdown — same data, broken
	// into typed fields so the slashcommand prompt and any tool consumer
	// can pull individual sections without re-parsing.
	Skeleton Skeleton `json:"skeleton,omitempty"`

	// PostCompact is true when the most recent compact_boundary in the
	// JSONL is followed by fewer than postCompactTailThreshold turns —
	// in which case the slashcommand suppresses narrative authoring
	// and emits skeleton-only with a banner.
	PostCompact bool `json:"post_compact,omitempty" jsonschema:"true when the model can no longer be trusted to author narrative because a compact event recently ran"`

	// NarrativeSlots lists the section keys the slashcommand should
	// ask the in-session model to author. Empty when PostCompact=true.
	NarrativeSlots []string `json:"narrative_slots,omitempty" jsonschema:"section keys the slashcommand should author: continue_from, decided_vs_open, read_first"`

	Ambiguous  bool           `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates []CandidateRow `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// RenderHandoff renders the deterministic skeleton — the body of
// the handoff prompt without any model-authored narrative. Pure
// function of the snapshot. The proof test at
// docs/proof/02-handoff-equivalence/ asserts this output is
// byte-identical across runs for the same (snapshot, git-dirty)
// input.
func RenderHandoff(snap *SessionSnapshot) string {
	return renderHandoff(snap, time.Now().UnixMilli())
}

// renderHandoff is the implementation; now is injected so tests
// (and the proof fixture) can pin relative-time strings.
func renderHandoff(snap *SessionSnapshot, now int64) string {
	var b strings.Builder

	cwd := firstCwdFromMessages(snap.Messages)
	if cwd == "" {
		cwd = "(unknown — no cwd-bearing message in transcript)"
	}
	branch := branchFromMessages(snap.Messages)

	fmt.Fprintf(&b, "# Handoff from session `%s`\n\n", short(snap.SessionID))
	if branch != "" {
		fmt.Fprintf(&b, "Working in `%s` on branch `%s`.\n\n", cwd, branch)
	} else {
		fmt.Fprintf(&b, "Working in `%s`.\n\n", cwd)
	}

	if plan := extractPlanOfRecord(snap.Messages, now); plan != nil {
		b.WriteString("## Plan of record\n\n")
		ago := ""
		if plan.LastTouchAgo != "" {
			ago = fmt.Sprintf("; last touched %s ago", plan.LastTouchAgo)
		}
		fmt.Fprintf(&b, "- `%s` (read %d×%s)\n\n", plan.Path, plan.ReadCount, ago)
	}

	verdict := contexthealthScoreFiles(snap.Messages)
	anchors, staleCount := buildAnchorFiles(verdict, snap.GitDirtyFiles, snap.GitDirtyKnown, now)
	if len(anchors) > 0 {
		fmt.Fprintf(&b, "## Anchor files (top %d by relevance)\n\n", len(anchors))
		b.WriteString("| File | State | Last touch |\n|------|-------|------------|\n")
		for _, a := range anchors {
			state := "clean"
			switch {
			case a.DirtyUnknown:
				state = "unknown"
			case a.Dirty:
				state = "dirty"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", a.Path, state, a.LastTouchAgo)
		}
		b.WriteString("\n")
		if staleCount > 0 {
			fmt.Fprintf(&b, "<details><summary>%d more files touched (stale / low-relevance)</summary>\n\n", staleCount)
			for _, f := range verdict.Files {
				if f.Stale {
					fmt.Fprintf(&b, "- `%s`\n", f.Path)
				}
			}
			b.WriteString("\n</details>\n\n")
		}
	}

	if tickets := extractTicketHints(snap.Messages); len(tickets) > 0 {
		b.WriteString("## Likely ticket / source-of-truth\n\n")
		for _, t := range tickets {
			if t.FromURL {
				fmt.Fprintf(&b, "- `%s` (appears in pasted URL)\n", t.Key)
			} else {
				fmt.Fprintf(&b, "- `%s` (mentioned %d× in user turns)\n", t.Key, t.Mentions)
			}
		}
		if urls := extractLinkedURLs(snap.Messages); len(urls) > 0 {
			for _, u := range urls {
				fmt.Fprintf(&b, "- %s\n", u)
			}
		}
		b.WriteString("\n")
	}

	inProg, pending := extractTodos(snap.Messages)
	if len(inProg)+len(pending) > 0 {
		b.WriteString("## In-progress todos (last TodoWrite)\n\n")
		for _, t := range inProg {
			fmt.Fprintf(&b, "- [in_progress] %s\n", t.Content)
		}
		for _, t := range pending {
			fmt.Fprintf(&b, "- [pending] %s\n", t.Content)
		}
		b.WriteString("\n")
	}

	if fails := recentFailures(snap.Messages); len(fails) > 0 {
		b.WriteString("## Recent blockers (last 3 errors)\n\n")
		for _, f := range fails {
			fmt.Fprintf(&b, "- %s\n", oneLine(f))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "_Source: `%s`_\n", snap.Path)
	return b.String()
}

// recentFailures pulls the most recent N tool-result rows whose
// IsError flag is set. Truncated per-row so a single huge stack
// trace doesn't dominate the handoff.
func recentFailures(msgs []*connectors.Message) []string {
	const maxFailures = handoffMaxBlockers
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

// handoffMaxTodos caps the rendered count of in_progress + pending
// items each. The TodoWrite tool itself doesn't impose a cap, so
// we apply our own to keep the handoff bounded.
const handoffMaxTodos = 8

// extractTodos returns (in_progress, pending) items from the MOST
// RECENT TodoWrite tool call in the snapshot. Older TodoWrites are
// ignored because the model overwrites the full list on each call.
// Returns (nil, nil) when no TodoWrite was used or the most recent
// one's input fails to parse.
func extractTodos(msgs []*connectors.Message) (inProgress, pending []TodoItem) {
	// Walk backward to find the most recent TodoWrite.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil {
			continue
		}
		for j := len(m.ToolCalls) - 1; j >= 0; j-- {
			tc := m.ToolCalls[j]
			if !strings.EqualFold(tc.Name, "TodoWrite") {
				continue
			}
			var raw struct {
				Todos []struct {
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}
			if err := json.Unmarshal([]byte(tc.Input), &raw); err != nil {
				return nil, nil
			}
			for _, t := range raw.Todos {
				item := TodoItem{Content: strings.TrimSpace(t.Content), Status: t.Status}
				if item.Content == "" {
					continue
				}
				switch t.Status {
				case "in_progress":
					if len(inProgress) < handoffMaxTodos {
						inProgress = append(inProgress, item)
					}
				case "pending":
					if len(pending) < handoffMaxTodos {
						pending = append(pending, item)
					}
				}
			}
			return inProgress, pending
		}
	}
	return nil, nil
}

// handoffMaxAnchorFiles caps the visible anchor-file list. Past this
// point the list becomes noise; the collapsed tail in the rendered
// markdown still acknowledges the rest exist.
const handoffMaxAnchorFiles = 6

// buildAnchorFiles converts a contexthealth RelevanceVerdict + the
// snapshot's captured dirty set into the ordered list rendered in
// the handoff. Returns (anchors, staleCount); staleCount is the
// number of touched files dropped from the visible list because
// they were marked Stale.
//
// dirtyKnown=false signals git state couldn't be captured; per-file
// Dirty defaults to false and DirtyUnknown is set so the renderer
// labels honestly rather than guessing "clean."
func buildAnchorFiles(verdict contexthealth.RelevanceVerdict, dirty map[string]bool, dirtyKnown bool, now int64) ([]AnchorFileRef, int) {
	var fresh []contexthealth.FileRelevance
	staleCount := 0
	for _, f := range verdict.Files {
		if f.Stale {
			staleCount++
			continue
		}
		fresh = append(fresh, f)
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].Score != fresh[j].Score {
			return fresh[i].Score > fresh[j].Score
		}
		if fresh[i].LastTouchTs != fresh[j].LastTouchTs {
			return fresh[i].LastTouchTs > fresh[j].LastTouchTs
		}
		return fresh[i].Path < fresh[j].Path
	})
	if len(fresh) > handoffMaxAnchorFiles {
		fresh = fresh[:handoffMaxAnchorFiles]
	}
	out := make([]AnchorFileRef, 0, len(fresh))
	for _, f := range fresh {
		ref := AnchorFileRef{
			Path:         f.Path,
			LastTouchAgo: formatRelativeAgo(now, f.LastTouchTs),
			Score:        f.Score,
		}
		if dirtyKnown {
			ref.Dirty = dirty[f.Path]
		} else {
			ref.DirtyUnknown = true
		}
		out = append(out, ref)
	}
	return out, staleCount
}

// branchFromMessages returns the first non-empty GitBranch field
// across the snapshot. Empty when no message carried one.
func branchFromMessages(msgs []*connectors.Message) string {
	for _, m := range msgs {
		if m != nil && m.GitBranch != "" {
			return m.GitBranch
		}
	}
	return ""
}

// postCompactTailThreshold is the maximum number of post-boundary
// user+assistant turns allowed before we conclude the model has
// rebuilt enough context to write reliable narrative. Below this
// count we suppress narrative authoring and emit skeleton-only.
const postCompactTailThreshold = 50

// detectPostCompact returns true when the most recent compact
// boundary in the snapshot is followed by fewer than
// postCompactTailThreshold user+assistant turns. False when no
// boundary exists at all.
//
// Compact boundaries are recognised via the canonical
// connectors.CompactBoundary attachment that the Claude parser
// emits for `subtype:"compact_boundary"` JSONL lines. Codex
// transcripts currently never carry this attachment, so this
// helper always returns false for Codex sessions — Codex
// post-compact detection is a separate follow-up that needs the
// Codex parser to surface its `type:"compacted"` events as
// synthetic boundary messages.
func detectPostCompact(msgs []*connectors.Message) bool {
	lastBoundary := -1
	for i, m := range msgs {
		if m != nil && m.CompactBoundary != nil {
			lastBoundary = i
		}
	}
	if lastBoundary < 0 {
		return false
	}
	tail := 0
	for i := lastBoundary + 1; i < len(msgs); i++ {
		m := msgs[i]
		if m == nil {
			continue
		}
		if m.Role == connectors.RoleUser || m.Role == connectors.RoleAssistant {
			tail++
		}
	}
	return tail < postCompactTailThreshold
}

// DetectPostCompactExported is the public entry the determinism
// proof uses to assert PostCompact-flagging behaviour without
// pulling in the full HandleGenerateHandoff plumbing.
func DetectPostCompactExported(msgs []*connectors.Message) bool {
	return detectPostCompact(msgs)
}

// buildSkeleton converts the snapshot into the structured Skeleton
// form returned alongside Markdown. Pulls the same fields the
// renderer prints, so consumers can pick by structure or by
// rendered text without divergence.
func buildSkeleton(snap *SessionSnapshot) Skeleton {
	now := time.Now().UnixMilli()
	verdict := contexthealthScoreFiles(snap.Messages)
	anchors, staleCount := buildAnchorFiles(verdict, snap.GitDirtyFiles, snap.GitDirtyKnown, now)
	inProg, pending := extractTodos(snap.Messages)
	blockers := recentFailures(snap.Messages)
	for i, b := range blockers {
		blockers[i] = oneLine(b)
	}
	return Skeleton{
		Branch:          branchFromMessages(snap.Messages),
		PlanOfRecord:    extractPlanOfRecord(snap.Messages, now),
		AnchorFiles:     anchors,
		StaleFilesCount: staleCount,
		LikelyTickets:   extractTicketHints(snap.Messages),
		LinkedURLs:      extractLinkedURLs(snap.Messages),
		InProgressTodos: inProg,
		PendingTodos:    pending,
		KnownBlockers:   blockers,
	}
}
