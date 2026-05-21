package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// klyneSummaryRe captures the `KLYNE_SUMMARY: <text>` line the
// UserPromptSubmit hook asks the assistant to emit at the end of each
// reply. Anchored to start-of-line so a passing mention of the literal
// string inside markdown / quotes / code blocks doesn't false-match.
//
// The text portion is captured up to end-of-line. A leading "skip"
// (case-insensitive, exact word) is treated as the assistant
// declaring the turn unworthy of a summary — we discard such lines.
var klyneSummaryRe = regexp.MustCompile(`(?m)^\s*KLYNE_SUMMARY:\s*(.+?)\s*$`)

// klyneSummaryMaxLen caps the captured summary length so a runaway
// reply can't bloat ai_drafted_summary. The instruction asks for
// ≤100 words; 1000 characters is a generous ceiling.
const klyneSummaryMaxLen = 1000

// loadSnapshotWaitForFinalText loads path via mcpserver.LoadSnapshot
// and retries until the most recent assistant message carries
// non-empty text content, or budget elapses. This sidesteps the race
// where Claude Code's Stop hook fires before the model's final text
// block is flushed to JSONL — a naive single LoadSnapshot loses the
// KLYNE_SUMMARY line we just instructed the model to emit.
//
// "Final text content present" is the proxy for "transcript is
// complete" — much more reliable than mtime polling on macOS where
// mtime granularity and write coalescing can lie. We give up on
// budget and return whatever's loaded; the caller proceeds with that
// (the deterministic columns still write, ai_drafted_summary just
// stays empty for that row).
func loadSnapshotWaitForFinalText(ctx context.Context, path string, budget time.Duration) (*mcpserver.SessionSnapshot, error) {
	const pollEvery = 100 * time.Millisecond
	deadline := time.Now().Add(budget)
	var lastSnap *mcpserver.SessionSnapshot
	for {
		snap, err := mcpserver.LoadSnapshot(ctx, path)
		if err != nil {
			return nil, err
		}
		lastSnap = snap
		if hasFinalAssistantText(snap) {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return lastSnap, nil
		}
		select {
		case <-ctx.Done():
			return lastSnap, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// hasFinalAssistantText reports whether snap's most recent assistant
// message has non-empty text content. Tool-call-only assistant turns
// don't count — they're a sign the model hasn't emitted its final
// reply yet (the typical sequence is: tool_use, tool_result,
// tool_use, ..., final text). The Stop hook fires once the LAST
// assistant turn includes plain text — which is also where the
// KLYNE_SUMMARY line will live.
func hasFinalAssistantText(snap *mcpserver.SessionSnapshot) bool {
	if snap == nil {
		return false
	}
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		m := snap.Messages[i]
		if m == nil || m.Role != connectors.RoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) != "" {
			return true
		}
	}
	return false
}


// extractKlyneSummary scans msgs (in reverse) for the most recent
// assistant message and returns its trailing `KLYNE_SUMMARY: ...`
// payload. Returns "" when the line is missing, says "skip", or the
// capture is empty. The match is taken from the LAST occurrence so
// an interrupted earlier draft doesn't shadow the final answer.
func extractKlyneSummary(msgs []*connectors.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Role != connectors.RoleAssistant {
			continue
		}
		body := m.Content
		if body == "" {
			// First assistant message with non-empty text wins;
			// pure tool-call turns have empty Content and are skipped.
			continue
		}
		matches := klyneSummaryRe.FindAllStringSubmatch(body, -1)
		if len(matches) == 0 {
			return ""
		}
		raw := strings.TrimSpace(matches[len(matches)-1][1])
		if raw == "" {
			return ""
		}
		if strings.EqualFold(raw, "skip") {
			return ""
		}
		if len(raw) > klyneSummaryMaxLen {
			raw = raw[:klyneSummaryMaxLen]
		}
		return raw
	}
	return ""
}

// sessionEndTimeout caps wall-clock time for the entire hook. Larger
// than the advise hook's 2s because we may walk a longer transcript;
// still bounded so a corrupt JSONL never stalls Claude Code's exit.
const sessionEndTimeout = 5 * time.Second

// sessionEndMaxFiles caps how many distinct file paths the summary
// stores. The last 10 files touched are far more useful than a long
// list spanning the whole session.
const sessionEndMaxFiles = 10

// sessionEndInput is the JSON the Stop hook receives on stdin. We
// only care about the four fields Claude Code documents — other
// keys may appear and are ignored.
type sessionEndInput struct {
	SessionID      string `json:"session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	StopHookActive bool   `json:"stop_hook_active,omitempty"`
	CWD            string `json:"cwd,omitempty"`
}

// SessionEnd is the daemon-side implementation of `klyne session-end`.
// The Claude Code Stop hook calls this when a session ends; the
// handler loads the transcript, builds a deterministic summary, and
// persists one row to the stop_summaries table plus a worklog entry.
//
// db is the daemon's already-open store handle (reused across hook
// dispatches). Passing nil falls back to opening a fresh DB on the
// fly — same degradation pattern as the cobra path.
//
// Always returns Result with ExitCode 0: the Stop hook must never
// block Claude Code's exit. Errors are surfaced via Stderr only.
func SessionEnd(ctx context.Context, stdin io.Reader, db *store.DB) Result {
	ctx, cancel := context.WithTimeout(ctx, sessionEndTimeout)
	defer cancel()

	var errb bytes.Buffer
	if err := computeAndPersistSessionEnd(ctx, stdin, db, &errb); err != nil {
		fmt.Fprintf(&errb, "klyne session-end: %v\n", err)
	}
	return Result{Stderr: errb.Bytes()}
}

// computeAndPersistSessionEnd is the I/O-aware body — mirrors
// cmd/klyne/session_end.go::computeAndPersistSessionEnd. Duplicated
// (rather than shared) per the convention documented in hooks.go:
// the cobra path lives in `package main` and can't be imported.
//
// stderr is used for worklog write failures (which must not block
// the Stop event) — every other failure path returns via the error
// channel so the caller can prefix with the hook name.
func computeAndPersistSessionEnd(ctx context.Context, stdin io.Reader, db *store.DB, stderr io.Writer) error {
	in := readSessionEndInput(stdin)

	transcript := strings.TrimSpace(in.TranscriptPath)
	if transcript == "" {
		// Fall back to cwd-based resolution; useful when a future
		// Claude Code build adjusts the event shape.
		cwd := in.CWD
		if cwd == "" {
			return nil
		}
		path, err := resolveSessionPath(in.SessionID, cwd)
		if err != nil || path == "" {
			return err
		}
		transcript = path
	}

	// Load the transcript, retrying briefly if the final assistant
	// turn hasn't been flushed to disk yet. Claude Code's Stop hook
	// fires the moment the model stops generating — a few hundred
	// milliseconds BEFORE the final text block lands in JSONL. The
	// retry loop polls until the most recent assistant message
	// carries non-empty text content (i.e., not just tool calls), or
	// the budget elapses. Bounded ≤ 3s, well within the 5s sessionEndTimeout.
	snap, err := loadSnapshotWaitForFinalText(ctx, transcript, 3*time.Second)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}
	if snap == nil || len(snap.Messages) == 0 {
		return nil
	}

	cwd := strings.TrimSpace(in.CWD)
	if cwd == "" {
		cwd = derivedCWD(snap.Messages)
	}
	// Canonicalize to the main repo path so worklog entries from a
	// worktree session land under the same project_path as the main
	// checkout. Non-git dirs and git failures pass through unchanged.
	projectPath := projectpath.Canonical(cwd)

	summary := buildSessionEndSummary(snap, projectPath)
	if summary.Summary == "" {
		return nil
	}

	// Resolve the DB: prefer the daemon's open handle when supplied,
	// otherwise open a fresh one. The daemon supplies one in production
	// via SetDB; tests can pass nil to exercise the open-fresh path.
	if db == nil {
		opened, err := resolveDB(ctx)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		// Only close DBs we opened locally — the daemon's handle is
		// long-lived and must not be closed.
		defer opened.Close()
		db = opened
	}

	row := &store.StopSummary{
		SessionID:   snap.SessionID,
		Ts:          time.Now().UnixMilli(),
		ProjectPath: projectPath,
		CLI:         string(detectCLI(snap)),
		Summary:     summary.Summary,
		LastUser:    summary.LastUser,
		LastBash:    summary.LastBash,
		Files:       summary.Files,
	}
	if err := store.InsertStopSummary(ctx, db, row); err != nil {
		return fmt.Errorf("insert stop summary: %w", err)
	}

	// Derive worklog metrics from the snapshot. The Stop hook is the
	// authoritative writer for Claude-side worklog entries; failures
	// log to stderr but never block session-end.
	var (
		toolCount      int
		editWriteCount int
		firstTs        int64
		lastTs         int64
	)
	for _, m := range snap.Messages {
		if m == nil {
			continue
		}
		if m.Role == connectors.RoleAssistant {
			toolCount += len(m.ToolCalls)
			for _, tc := range m.ToolCalls {
				switch strings.ToLower(tc.Name) {
				case "edit", "write", "multiedit":
					editWriteCount++
				}
			}
		}
		if firstTs == 0 || (m.Ts > 0 && m.Ts < firstTs) {
			firstTs = m.Ts
		}
		if m.Ts > lastTs {
			lastTs = m.Ts
		}
	}
	wallTime := time.Duration(0)
	if firstTs > 0 && lastTs > firstTs {
		wallTime = time.Duration(lastTs-firstTs) * time.Millisecond
	}

	tags := deriveEventTags(sessionFixture{
		LastBash:       summary.LastBash,
		EditWriteCount: editWriteCount,
		Files:          summary.Files,
	})

	// Pull the per-turn KLYNE_SUMMARY line out of the last assistant
	// message. The UserPromptSubmit hook injects an instruction asking
	// the assistant to emit one such line at the end of each reply;
	// when present we store it verbatim in ai_drafted_summary so
	// reflection has a real prose record without any daemon-side LM call.
	// Empty / "skip" / missing → empty AIDraftedSummary; reflection still
	// works using the deterministic columns.
	aiDraftedSummary := extractKlyneSummary(snap.Messages)

	entry := worklog.Entry{
		SessionID:        row.SessionID,
		TS:               time.UnixMilli(row.Ts),
		ProjectPath:      row.ProjectPath,
		CLI:              row.CLI,
		LastUser:         row.LastUser,
		LastBash:         row.LastBash,
		Files:            row.Files,
		CommitSHA:        "",
		WallTime:         wallTime,
		ToolCallCount:    toolCount,
		EditWriteCount:   editWriteCount,
		EventTags:        tags,
		AIDraftedSummary: aiDraftedSummary,
	}
	if _, werr := worklog.WriteEntry(ctx, db, entry, map[string]bool{}, store.UpsertStopSummaryWithWorklog); werr != nil {
		fmt.Fprintf(stderr, "klyne session-end: worklog write failed: %v\n", werr)
	}

	// Capture a point-in-time git snapshot of the session's repo and its
	// sibling worktrees (spec D6 — the only way to reconstruct
	// "AI task done but uncommitted at session end" historically).
	// BEST-EFFORT and NON-FATAL: any git or DB failure is logged to
	// stderr and swallowed, exactly like the worklog write above —
	// session-end must never block Claude Code's exit on a klyne error.
	captureGitSnapshots(ctx, db, row.SessionID, cwd, stderr)
	return nil
}

// captureGitSnapshots writes one git_session_snapshots row per worktree
// of the session's repo. It is invoked at the end of session-end and is
// strictly best-effort: every failure path (no git, capture error, DB
// insert error) is logged to stderr and swallowed so the Stop hook never
// blocks Claude Code's exit. dir is the session's cwd (a non-git dir
// simply yields no snapshots). Mirrors the cobra path's helper of the
// same name — see hooks.go's duplication note.
func captureGitSnapshots(ctx context.Context, db *store.DB, sessionID, dir string, stderr io.Writer) {
	if db == nil {
		return
	}
	snaps := productivity.CaptureSessionSnapshots(dir)
	now := time.Now()
	for _, s := range snaps {
		row := &store.GitSnapshot{
			SessionID:      sessionID,
			ProjectPath:    s.ProjectPath,
			RepoName:       s.RepoName,
			WorktreePath:   s.WorktreePath,
			Branch:         s.Branch,
			HeadSHA:        s.HeadSHA,
			AheadCount:     s.AheadCount,
			BehindCount:    s.BehindCount,
			DirtyFileCount: s.DirtyFileCount,
			DirtyFiles:     s.DirtyFiles,
			CapturedAt:     now,
		}
		if err := store.InsertGitSnapshot(ctx, db, row); err != nil {
			fmt.Fprintf(stderr, "klyne session-end: git snapshot write failed: %v\n", err)
		}
	}
}

// sessionFixture is the minimal input shape consumed by
// deriveEventTags. Mirrors the cobra path's type of the same name.
type sessionFixture struct {
	LastBash       string
	EditWriteCount int
	Files          []string
}

// deriveEventTags is the deterministic classifier that maps a
// session-end summary onto worklog event tags. Mirrors the cobra
// path; keyword/prefix matches only, no model calls.
func deriveEventTags(s sessionFixture) []worklog.EventTag {
	var tags []worklog.EventTag
	cmd := strings.TrimSpace(s.LastBash)
	// Detect commits and PR-opens in compound commands too — e.g.
	// `git add README.md && git commit -m "..."`. Strict-prefix match
	// missed these and undercounted the events.
	if containsCommandToken(cmd, "git commit") {
		tags = append(tags, worklog.TagCommitLanded)
	}
	if containsCommandToken(cmd, "gh pr create") {
		tags = append(tags, worklog.TagPROpened)
	}
	if s.EditWriteCount >= 2 {
		tags = append(tags, worklog.TagFileSignificantlyEdited)
	}
	for _, f := range s.Files {
		if strings.Contains(f, "/migrations/") || strings.HasSuffix(f, ".sql") || strings.HasSuffix(f, ".proto") {
			tags = append(tags, worklog.TagMigrationOrSchemaChange)
			break
		}
	}
	for _, f := range s.Files {
		base := filepath.Base(f)
		if base == "go.mod" || base == "package.json" || base == "Cargo.toml" || base == "pyproject.toml" {
			tags = append(tags, worklog.TagDependencyChange)
			break
		}
	}
	for _, f := range s.Files {
		lower := strings.ToLower(f)
		for _, kw := range []string{"auth", "crypto", "password", "token", "secret", "oauth"} {
			if strings.Contains(lower, kw) {
				tags = append(tags, worklog.TagSecurityRelevantChange)
				goto done
			}
		}
	}
done:
	return tags
}

func readSessionEndInput(r io.Reader) sessionEndInput {
	if r == nil {
		return sessionEndInput{}
	}
	body, err := io.ReadAll(io.LimitReader(r, 64*1024))
	if err != nil || len(body) == 0 {
		return sessionEndInput{}
	}
	var in sessionEndInput
	if err := json.Unmarshal(body, &in); err != nil {
		return sessionEndInput{}
	}
	return in
}

// containsCommandToken reports whether cmd contains token as a
// standalone command — guarding against the false positive where a
// substring like `git committed` would match `git commit`. We split
// cmd on shell separators (&&, ;, |, newlines) and prefix-match each
// segment, which catches both compound commands and chained pipes.
func containsCommandToken(cmd, token string) bool {
	if cmd == "" || token == "" {
		return false
	}
	// Replace separators with a single sentinel so a single Split
	// pass covers all three.
	normalized := cmd
	for _, sep := range []string{"&&", "||", "|", ";", "\n"} {
		normalized = strings.ReplaceAll(normalized, sep, "\x00")
	}
	for _, seg := range strings.Split(normalized, "\x00") {
		seg = strings.TrimSpace(seg)
		if strings.HasPrefix(seg, token) {
			// Require either end-of-segment or a space/flag character
			// after the token so `git committed` doesn't pass.
			tail := seg[len(token):]
			if tail == "" || tail[0] == ' ' || tail[0] == '\t' {
				return true
			}
		}
	}
	return false
}

// derivedCWD picks the cwd off the latest message that has one set.
// Mirrors the cobra path so summary project_path matches across both
// invocation modes.
func derivedCWD(msgs []*connectors.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] == nil {
			continue
		}
		if msgs[i].Cwd != "" {
			return msgs[i].Cwd
		}
		if msgs[i].ProjectPath != "" {
			return msgs[i].ProjectPath
		}
	}
	return ""
}

// detectCLI returns "claude" / "codex" based on the snapshot's
// message stream. Defaults to "claude" when ambiguous.
func detectCLI(snap *mcpserver.SessionSnapshot) connectors.CLI {
	if snap == nil {
		return connectors.CLIClaude
	}
	for _, m := range snap.Messages {
		if m != nil && m.CLI != "" {
			return m.CLI
		}
	}
	return connectors.CLIClaude
}

// sessionEndSummary is the deterministic synthesis from the JSONL.
type sessionEndSummary struct {
	Summary  string
	LastUser string
	LastBash string
	Files    []string
}

// buildSessionEndSummary walks msgs in reverse to find the most
// recent user prompt, the most recent Bash command, and up to
// sessionEndMaxFiles distinct file paths touched in the final
// tool-call window. Mirrors the cobra path.
func buildSessionEndSummary(snap *mcpserver.SessionSnapshot, projectPath string) sessionEndSummary {
	var (
		last      sessionEndSummary
		files     []string
		seen      = map[string]struct{}{}
		bashFound bool
		userFound bool
	)

	for i := len(snap.Messages) - 1; i >= 0; i-- {
		m := snap.Messages[i]
		if m == nil {
			continue
		}
		if !userFound && m.Role == connectors.RoleUser && strings.TrimSpace(m.Content) != "" {
			last.LastUser = sessionEndTruncate(strings.TrimSpace(m.Content), 200)
			userFound = true
		}
		if m.Role == connectors.RoleAssistant {
			for _, tc := range m.ToolCalls {
				switch strings.ToLower(tc.Name) {
				case "bash", "shell":
					if !bashFound {
						if cmd := extractBashCmdFromInput(tc.Input); cmd != "" {
							last.LastBash = sessionEndTruncate(cmd, 200)
							bashFound = true
						}
					}
				case "read", "edit", "write", "multiedit":
					if path := extractFilePath(tc.Input); path != "" {
						if _, dup := seen[path]; !dup {
							seen[path] = struct{}{}
							files = append(files, path)
						}
					}
				}
				if len(files) >= sessionEndMaxFiles && bashFound && userFound {
					break
				}
			}
		}
		if len(files) >= sessionEndMaxFiles && bashFound && userFound {
			break
		}
	}

	last.Files = files
	last.Summary = renderSessionEndBody(snap.SessionID, projectPath, last)
	return last
}

func renderSessionEndBody(sessionID, projectPath string, s sessionEndSummary) string {
	if s.LastUser == "" && s.LastBash == "" && len(s.Files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# klyne session-end summary\n\n")
	fmt.Fprintf(&b, "Session: `%s`\n", sessionShortID(sessionID))
	if projectPath != "" {
		fmt.Fprintf(&b, "Project: `%s`\n", projectPath)
	}
	fmt.Fprintf(&b, "Ended: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	if s.LastUser != "" {
		fmt.Fprintf(&b, "## Last user prompt\n\n> %s\n\n", sessionOneLine(s.LastUser))
	}
	if s.LastBash != "" {
		fmt.Fprintf(&b, "## Last shell command\n\n`%s`\n\n", sessionOneLine(s.LastBash))
	}
	if len(s.Files) > 0 {
		b.WriteString("## Files touched (most recent first)\n\n")
		for _, f := range s.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}
	return b.String()
}

func extractBashCmdFromInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		return input
	}
	if v, ok := obj["command"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := obj["cmd"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

func extractFilePath(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "filePath"} {
		if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sessionEndTruncate is a UTF-8-safe string truncator. Renamed from
// the cobra path's `truncate` to avoid collision if a future shared
// helper lands.
func sessionEndTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// sessionOneLine collapses newlines so a single message body renders
// cleanly inside a Markdown blockquote / code span.
func sessionOneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// sessionShortID returns the first 8 characters of an id. Local copy
// of cmd/klyne's shortID — see hooks.go's duplication note.
func sessionShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
