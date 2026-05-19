package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

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

	snap, err := mcpserver.LoadSnapshot(ctx, transcript)
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

	entry := worklog.Entry{
		SessionID:      row.SessionID,
		TS:             time.UnixMilli(row.Ts),
		ProjectPath:    row.ProjectPath,
		CLI:            row.CLI,
		LastUser:       row.LastUser,
		LastBash:       row.LastBash,
		Files:          row.Files,
		CommitSHA:      "",
		WallTime:       wallTime,
		ToolCallCount:  toolCount,
		EditWriteCount: editWriteCount,
		EventTags:      tags,
	}
	if _, werr := worklog.WriteEntry(ctx, db, entry, map[string]bool{}, store.UpsertStopSummaryWithWorklog); werr != nil {
		fmt.Fprintf(stderr, "klyne session-end: worklog write failed: %v\n", werr)
	}
	return nil
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
	if strings.HasPrefix(cmd, "git commit") {
		tags = append(tags, worklog.TagCommitLanded)
	}
	if strings.HasPrefix(cmd, "gh pr create") {
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
