package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
)

// session_end.go — `klyne session-end` Stop-hook entry point.
//
// This subcommand is registered by `klyne mcp install` as a Stop
// hook in ~/.claude/settings.json. When Claude Code ends a session,
// it spawns this subprocess and pipes the Stop-event JSON to stdin:
//
//	{
//	  "session_id": "...",
//	  "transcript_path": "/abs/path/to/.jsonl",
//	  "stop_hook_active": true,
//	  "cwd": "/abs/project/path"
//	}
//
// The hook:
//
//  1. Reads stdin (Stop-event JSON), resolves the transcript path
//     and cwd.
//  2. Loads the snapshot directly from JSONL.
//  3. Computes a deterministic summary (last user prompt, last
//     bash command, files touched in final tool-call window).
//  4. Persists one row to the stop_summaries table so a future
//     SessionStart in the same project can recall what just
//     happened.
//
// Hard rules:
//
//   - The hook must NEVER block session-end. Any error returns
//     exit 0 with empty stdout.
//   - Stdout is the hook channel; nothing is written there in v1
//     because we don't want to inject context at session-end.
//   - stderr carries debugging lines only.

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

// newSessionEndCmd registers `klyne session-end`. Hidden from the
// help menu — it's an entry point for the hook, not for humans.
func newSessionEndCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "session-end",
		Short:  "Stop-hook entry point: write a deterministic session summary",
		Hidden: true,
		Long: `Read a Claude Code Stop-event JSON payload from stdin and
write a deterministic summary of the just-ended session to klyne's
local store. Designed as a Stop hook entry point — installed
automatically by 'klyne mcp install'.

Stdin: Stop-event JSON (session_id, transcript_path, cwd, ...).
Stdout: empty (no context injected back into Claude Code).
Stderr: debugging lines only.
Exit code: always 0 unless invoked with bad flags. Hooks must
never block session-end on a klyne error.`,
		RunE:          runSessionEnd,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return c
}

func runSessionEnd(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), sessionEndTimeout)
	defer cancel()

	if err := computeAndPersistSessionEnd(ctx, cmd.InOrStdin()); err != nil {
		// Log to stderr but never block Claude Code's exit.
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne session-end: %v\n", err)
	}
	return nil
}

// computeAndPersistSessionEnd is the I/O-aware body, separated so
// tests can drive it without a cobra command.
func computeAndPersistSessionEnd(ctx context.Context, stdin io.Reader) error {
	in := readSessionEndInput(stdin)

	transcript := strings.TrimSpace(in.TranscriptPath)
	if transcript == "" {
		// Fall back to cwd-based resolution; useful when a future
		// Claude Code build adjusts the event shape.
		cwd := in.CWD
		if cwd == "" {
			w, _ := os.Getwd()
			cwd = w
		}
		if cwd == "" {
			return nil // nothing to do silently
		}
		path, err := resolvePath(in.SessionID, cwd)
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
		return nil // no data → no summary
	}

	cwd := strings.TrimSpace(in.CWD)
	if cwd == "" {
		// Use the cwd reported on the latest message as a fallback.
		cwd = derivedCWD(snap.Messages)
	}

	summary := buildSessionEndSummary(snap, cwd)
	if summary.Summary == "" {
		return nil // nothing useful happened in the session
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	row := &store.StopSummary{
		SessionID:   snap.SessionID,
		Ts:          time.Now().UnixMilli(),
		ProjectPath: cwd,
		CLI:         string(detectCLI(snap)),
		Summary:     summary.Summary,
		LastUser:    summary.LastUser,
		LastBash:    summary.LastBash,
		Files:       summary.Files,
	}
	if err := store.InsertStopSummary(ctx, db, row); err != nil {
		return fmt.Errorf("insert stop summary: %w", err)
	}
	return nil
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
// Some Claude Code versions omit cwd in the Stop event but emit it on
// every message; this keeps the summary's project_path accurate.
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

// buildSessionEndSummary walks msgs in reverse to find:
//   - the most recent user prompt (capped at ~200 chars)
//   - the most recent Bash command run
//   - up to sessionEndMaxFiles distinct file paths touched by the
//     final tool-call window (Read / Edit / Write inputs)
//
// Then renders a Markdown body. The body is intended for a future
// SessionStart's recall surface — short, scannable, fact-based.
func buildSessionEndSummary(snap *mcpserver.SessionSnapshot, projectPath string) sessionEndSummary {
	var (
		last       sessionEndSummary
		files      []string
		seen       = map[string]struct{}{}
		bashFound  bool
		userFound  bool
	)

	for i := len(snap.Messages) - 1; i >= 0; i-- {
		m := snap.Messages[i]
		if m == nil {
			continue
		}
		if !userFound && m.Role == connectors.RoleUser && strings.TrimSpace(m.Content) != "" {
			last.LastUser = truncate(strings.TrimSpace(m.Content), 200)
			userFound = true
		}
		if m.Role == connectors.RoleAssistant {
			for _, tc := range m.ToolCalls {
				switch strings.ToLower(tc.Name) {
				case "bash", "shell":
					if !bashFound {
						if cmd := extractBashCmdFromInput(tc.Input); cmd != "" {
							last.LastBash = truncate(cmd, 200)
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

// renderSessionEndBody produces the Markdown body persisted on the
// stop_summary row. Kept short; the contract is "useful for a future
// session to recall what just happened" — not a transcript.
func renderSessionEndBody(sessionID, projectPath string, s sessionEndSummary) string {
	if s.LastUser == "" && s.LastBash == "" && len(s.Files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# klyne session-end summary\n\n")
	fmt.Fprintf(&b, "Session: `%s`\n", shortID(sessionID))
	if projectPath != "" {
		fmt.Fprintf(&b, "Project: `%s`\n", projectPath)
	}
	fmt.Fprintf(&b, "Ended: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	if s.LastUser != "" {
		fmt.Fprintf(&b, "## Last user prompt\n\n> %s\n\n", oneLineSafe(s.LastUser))
	}
	if s.LastBash != "" {
		fmt.Fprintf(&b, "## Last shell command\n\n`%s`\n\n", oneLineSafe(s.LastBash))
	}
	if len(s.Files) > 0 {
		b.WriteString("## Files touched (most recent first)\n\n")
		for _, f := range s.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}
	return b.String()
}

// extractBashCmdFromInput is the same parser logic the runbook
// detector uses — co-located here so the session-end hook doesn't
// require the insights package as a dep of the cmd directory tree.
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

// extractFilePath pulls the "file_path" / "path" field from a Read
// or Edit tool call's JSON input payload. Returns empty when the
// payload is unparseable or carries neither field.
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

// truncate is a UTF-8-safe string truncator with an ellipsis.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// oneLineSafe collapses internal newlines so a single message body
// renders cleanly inside a Markdown blockquote / code span.
func oneLineSafe(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
