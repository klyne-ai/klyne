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
	"unicode/utf8"

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
//
// Exported (capitalised) so the cobra `klyne session-end` subcommand
// (cmd/klyne/session_end.go — the path Claude Code actually invokes
// via the klyne-hook fallback) can reuse the same retry semantics
// instead of doing a single blind LoadSnapshot. Keeping ONE wait
// implementation prevents the two session-end code paths from
// drifting apart again — that drift is exactly what caused
// `ai_drafted_summary` to stay empty system-wide.
func LoadSnapshotWaitForFinalText(ctx context.Context, path string, budget time.Duration) (*mcpserver.SessionSnapshot, error) {
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

// hasFinalAssistantText reports whether the JSONL has fully landed
// the current turn's assistant text. The check is deliberately
// strict: the VERY LAST message in the snapshot must be an assistant
// turn with non-empty text content. Anything else means the file
// flush is still in flight and we should keep polling.
//
// Why so strict — production race observed 2026-05-26: Claude Code
// fires the Stop hook the moment the model stops generating, but
// the file write of the final assistant message hasn't been
// committed to disk yet (file mtime trailed the hook by ~1s in the
// captured repro). At that instant the parsed snapshot's most
// recent assistant message is the PRIOR turn (one full
// user/assistant pair earlier). An earlier looser implementation
// scanned backward for "any assistant with text" and latched onto
// that prior turn, so the wait short-circuited and the row landed
// with empty AIDraftedSummary even though the actual KLYNE_SUMMARY
// arrived in the file shortly after.
//
// The strict last-message-is-assistant-with-text check makes those
// three states distinguishable:
//   - last is USER / tool result          → new turn not yet in file → wait
//   - last is ASSISTANT, no text content  → still emitting tool_use   → wait
//   - last is ASSISTANT with text         → final text landed         → ready
//
// On budget elapse the loop in LoadSnapshotWaitForFinalText returns
// whatever's loaded; the caller proceeds with ExtractKlyneSummary
// which will yield "" on an incomplete tail and the row writes with
// empty AIDraftedSummary — the documented degraded path.
func hasFinalAssistantText(snap *mcpserver.SessionSnapshot) bool {
	if snap == nil || len(snap.Messages) == 0 {
		return false
	}
	last := snap.Messages[len(snap.Messages)-1]
	if last == nil {
		return false
	}
	if last.Role != connectors.RoleAssistant {
		return false
	}
	return strings.TrimSpace(last.Content) != ""
}

// extractKlyneSummary scans msgs (in reverse) for the assistant's
// trailing `KLYNE_SUMMARY: ...` payload and returns it.
//
// Recap-leakage guard: Claude Code's per-turn recap generator runs as a
// separate model call that picks up the same UserPromptSubmit hook
// injection klyne emits, and dutifully responds with `KLYNE_SUMMARY:
// skip` because the recap turn is trivial. That message lands AFTER
// the user's real assistant reply in the transcript — same turn, no
// user message between — and the naive last-wins parser used to prefer
// the recap's skip over the user's authoritative summary, silently
// losing it from stop_summaries.ai_drafted_summary.
//
// The fix: scan back through CONSECUTIVE assistant messages and prefer
// any real (non-skip) summary over an intervening skip / missing line.
// A user message resets the scope — a skip emitted AFTER a new user
// prompt is an intentional skip for that new turn, not a recap
// artifact.
//
// Returns "" when no real summary is found within the current turn's
// assistant span, or when the captured text is empty.
//
// Exported (capitalised) so the cobra `klyne session-end` subcommand
// can populate stop_summaries.ai_drafted_summary identically to the
// daemon-side path. Both Stop-event entry points MUST funnel through
// this single function — they previously diverged and the cobra path
// silently dropped every KLYNE_SUMMARY line.
func ExtractKlyneSummary(msgs []*connectors.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil {
			continue
		}
		// Crossing a user message ends the current turn's assistant
		// span — anything before this is a prior turn.
		if m.Role == connectors.RoleUser {
			return ""
		}
		if m.Role != connectors.RoleAssistant {
			continue
		}
		body := m.Content
		if body == "" {
			// Tool-call-only assistant turns have empty Content;
			// keep scanning. Same as the original behaviour.
			continue
		}
		matches := klyneSummaryRe.FindAllStringSubmatch(body, -1)
		if len(matches) == 0 {
			// Message has prose but no KLYNE_SUMMARY line. Keep
			// scanning back — a later recap-style message that
			// dropped the summary entirely shouldn't shadow a
			// real summary further back in the same turn.
			continue
		}
		raw := strings.TrimSpace(matches[len(matches)-1][1])
		if raw == "" || strings.EqualFold(raw, "skip") {
			// Skip or empty-capture in this message; treat it as
			// possible recap leakage and keep scanning. If we hit
			// a user message (turn boundary) or run out of
			// assistant messages, the loop's exit paths return "".
			continue
		}
		if len(raw) > klyneSummaryMaxLen {
			raw = clipRuneSafe(raw, klyneSummaryMaxLen)
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
	if err := ComputeAndPersistSessionEnd(ctx, stdin, db, &errb); err != nil {
		fmt.Fprintf(&errb, "klyne session-end: %v\n", err)
	}
	return Result{Stderr: errb.Bytes()}
}

// ComputeAndPersistSessionEnd is the canonical Stop-hook body. Both
// entry points — the daemon's hookserver (via SessionEnd) and the
// cobra `klyne session-end` subprocess — funnel through this single
// function. They previously diverged (the cobra path silently dropped
// the KLYNE_SUMMARY extraction for over a week before this fix), and
// keeping ONE implementation is the only structural guarantee against
// that drift re-emerging.
//
// db: pass the daemon's open handle when called from within the
// daemon process; pass nil from the standalone cobra entry — the
// function opens its own DB and closes it on return.
//
// stderr is used for worklog / git-snapshot write failures (which
// must not block the Stop event). Every other failure path returns
// via the error channel so the caller can prefix with the hook name.
func ComputeAndPersistSessionEnd(ctx context.Context, stdin io.Reader, db *store.DB, stderr io.Writer) error {
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
	snap, err := LoadSnapshotWaitForFinalText(ctx, transcript, 3*time.Second)
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
				if isEditWriteTool(tc.Name) {
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

	tags := DeriveEventTags(SessionFixture{
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
	aiDraftedSummary := ExtractKlyneSummary(snap.Messages)

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

// SessionFixture is the minimal input shape consumed by
// DeriveEventTags. Exported so the (now-canonical) tag-derivation
// unit tests live alongside the implementation in this package.
type SessionFixture struct {
	LastBash       string
	EditWriteCount int
	Files          []string
}

// DeriveEventTags is the deterministic classifier that maps a
// session-end summary onto worklog event tags. Keyword/prefix
// matches only — no model calls — so the memory layer's signal is
// reproducible and auditable.
func DeriveEventTags(s SessionFixture) []worklog.EventTag {
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
				switch {
				case isShellTool(tc.Name):
					if !bashFound {
						if cmd := extractBashCmdFromInput(tc.Input); cmd != "" {
							last.LastBash = sessionEndTruncate(cmd, 200)
							bashFound = true
						}
					}
				case isFileTool(tc.Name):
					for _, path := range extractToolFilePaths(tc.Name, tc.Input) {
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

// Tool names differ between clients even when the operation is the same.
// Keep the normalization here so Codex apply_patch/exec_command turns feed
// the same worklog evidence and productivity scoring as Claude Edit/Bash.
func isShellTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "shell", "exec_command":
		return true
	default:
		return false
	}
}

func isEditWriteTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit", "write", "multiedit", "apply_patch":
		return true
	default:
		return false
	}
}

func isFileTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "edit", "write", "multiedit", "apply_patch":
		return true
	default:
		return false
	}
}

func extractToolFilePaths(name, input string) []string {
	if strings.EqualFold(strings.TrimSpace(name), "apply_patch") {
		return extractPatchFilePaths(input)
	}
	if path := extractFilePath(input); path != "" {
		return []string{path}
	}
	return nil
}

var patchFileHeaderRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File:\s*(.+?)\s*$`)

// extractPatchFilePaths reads the native apply_patch format used by Codex.
// Some clients wrap the patch in {"patch":"..."}; accept that shape too.
func extractPatchFilePaths(input string) []string {
	patch := strings.TrimSpace(input)
	if patch == "" {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(patch), &obj) == nil {
		if wrapped, ok := obj["patch"].(string); ok {
			patch = wrapped
		}
	}
	seen := map[string]bool{}
	var paths []string
	for _, match := range patchFileHeaderRe.FindAllStringSubmatch(patch, -1) {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
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

// sessionEndTruncate clips s to at most max bytes and appends an
// ellipsis when truncation occurs. The byte cut is rune-safe — it
// trims back to the last valid UTF-8 boundary so a multi-byte
// codepoint at the edge is dropped whole instead of being split.
// The "…" suffix is 3 bytes; the returned string is at most
// max-1+3 = max+2 bytes (the function trades a tight upper bound
// for the human-friendly ellipsis).
func sessionEndTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return clipRuneSafe(s, max-1) + "…"
}

// clipRuneSafe returns s clipped to at most maxBytes bytes, never
// splitting a UTF-8 codepoint. The cut lands on the highest rune
// boundary at or before maxBytes. Caller decides whether to append
// an ellipsis (and accounts for its byte cost).
//
// Used by both KLYNE_SUMMARY extraction (which persists into a
// SQLite TEXT column where downstream readers assume valid UTF-8)
// and by sessionEndTruncate's human-friendly path.
func clipRuneSafe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Range over string yields the byte offset of each rune's start;
	// the largest such offset that is ≤ maxBytes is where we cut.
	end := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		end = i
	}
	// Belt-and-braces: confirm the slice is valid UTF-8 (it should
	// always be when s was valid, since we cut on a rune boundary).
	out := s[:end]
	if !utf8.ValidString(out) {
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
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
