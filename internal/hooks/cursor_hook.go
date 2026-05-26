package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// cursor_hook.go — Cursor hook integration.
//
// Cursor CLI's hook surface (cursor.com/docs/hooks) is structurally
// similar to Claude Code's but uses different event names and payload
// shapes. Every event carries a common base with `hook_event_name`,
// `conversation_id`, `workspace_roots`, `transcript_path`, etc.
//
// klyne wires a SINGLE `klyne-hook cursor` command behind every event
// in ~/.cursor/hooks.json. The handler below reads the payload from
// stdin and dispatches internally by `hook_event_name`. Same binary,
// same daemon, same stop_summaries table — Cursor sessions surface in
// the productivity dashboard and reflection pipeline alongside Claude
// and codex turns.
//
// Per-turn semantics:
//   - sessionStart: emit additional_context carrying the canonical
//     KLYNE_SUMMARY-emit instruction so the agent's replies end with
//     a `KLYNE_SUMMARY: ...` line we can capture. This is Cursor's
//     documented injection channel (postToolUse also supports it;
//     beforeSubmitPrompt does NOT — only continue/user_message).
//   - afterAgentResponse: fires with `text` after every agent reply.
//     Extract KLYNE_SUMMARY and write one stop_summaries row per
//     turn — mirrors Claude's per-turn shape exactly.
//   - stop / sessionEnd: no-op, emit {} to satisfy Cursor's
//     fail-open contract.
//
// We intentionally do NOT parse Cursor's `transcript_path` file in
// v1. The afterAgentResponse `text` field gives us the assistant's
// final text directly — no JSONL parsing needed. If we later need
// history-aware features (file lists, tool counts) we can add a
// connectors/cursor parser following the codex template.

// CursorHookInput is the common base every Cursor hook event shares,
// plus the per-event fields we currently consume. Fields we don't
// need are left to the catch-all map for forward-compat.
type CursorHookInput struct {
	// Common base — present on every event except workspaceOpen.
	HookEventName   string   `json:"hook_event_name"`
	ConversationID  string   `json:"conversation_id"`
	GenerationID    string   `json:"generation_id"`
	Model           string   `json:"model"`
	CursorVersion   string   `json:"cursor_version"`
	WorkspaceRoots  []string `json:"workspace_roots"`
	UserEmail       string   `json:"user_email"`
	TranscriptPath  string   `json:"transcript_path"`

	// sessionStart-specific.
	SessionID         string `json:"session_id"`
	IsBackgroundAgent bool   `json:"is_background_agent"`
	ComposerMode      string `json:"composer_mode"`

	// afterAgentResponse / afterAgentThought.
	Text string `json:"text"`

	// beforeSubmitPrompt.
	Prompt string `json:"prompt"`
}

// cursorAdditionalContext is the docstring klyne injects via
// sessionStart.additional_context. Mirrors the per-turn instruction
// the Claude UserPromptSubmit hook returns. Kept verbatim so a Cursor
// session emits the same KLYNE_SUMMARY shape ExtractKlyneSummary
// expects — that lets the same regex/skip handling cover both CLIs.
const cursorAdditionalContext = "At the end of your reply, on its own line, emit exactly one of:\n" +
	"  KLYNE_SUMMARY: <a 1-2 sentence, ≤100-word summary of what was done this turn — files touched, decisions, outcomes>\n" +
	"  KLYNE_SUMMARY: skip\n" +
	"Use `skip` only when the turn was trivial (no edits, no commits, no decisions, no findings). Do not surround the line with code fences or quotes. Do not omit this line."

// CursorHook is the single entry point for every Cursor hook event.
// Reads the payload from stdin, dispatches by hook_event_name,
// returns a Result whose Stdout is the JSON Cursor expects on that
// event (empty `{}` is the documented fail-open default).
//
// db may be nil for the cobra-standalone path; CursorHook opens its
// own connection in that case and closes it on return — mirrors
// ComputeAndPersistSessionEnd's discipline.
func CursorHook(ctx context.Context, stdin io.Reader, db *store.DB) Result {
	ctx, cancel := context.WithTimeout(ctx, sessionEndTimeout)
	defer cancel()

	var errb bytes.Buffer
	out, err := computeCursorHook(ctx, stdin, db, &errb)
	if err != nil {
		fmt.Fprintf(&errb, "klyne cursor hook: %v\n", err)
	}
	if out == nil {
		// Fail-open: empty object so Cursor keeps going.
		out = []byte("{}")
	}
	return Result{Stdout: out, Stderr: errb.Bytes()}
}

// computeCursorHook is the I/O-aware body. Returns the JSON stdout
// payload Cursor should consume (per-event shape) plus a non-fatal
// error stream the caller logs to stderr.
func computeCursorHook(ctx context.Context, stdin io.Reader, db *store.DB, stderr io.Writer) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(stdin, 1<<20)) // 1MB cap
	if err != nil {
		return []byte("{}"), fmt.Errorf("read stdin: %w", err)
	}
	if len(body) == 0 {
		return []byte("{}"), nil
	}

	var in CursorHookInput
	if err := json.Unmarshal(body, &in); err != nil {
		return []byte("{}"), fmt.Errorf("parse payload: %w", err)
	}

	switch in.HookEventName {
	case "sessionStart":
		return handleCursorSessionStart(in)

	case "afterAgentResponse":
		return handleCursorAfterAgentResponse(ctx, in, db, stderr)

	default:
		// Every other event is a no-op for v1 — return the empty
		// object that satisfies Cursor's "fail-open / no override"
		// contract. We accept stdin so the events still fire (so we
		// can add behaviour later without re-registering hooks).
		return []byte("{}"), nil
	}
}

// handleCursorSessionStart returns the additional_context Cursor
// merges into the conversation's initial system context. We inject
// the same KLYNE_SUMMARY-emit instruction Claude's UserPromptSubmit
// hook injects — the model then ends each reply with the line and
// afterAgentResponse captures it.
//
// Cursor docs note `additional_context` is "added to the
// conversation's initial system context" on sessionStart, so this is
// a one-time injection that influences every subsequent turn in the
// session.
func handleCursorSessionStart(_ CursorHookInput) ([]byte, error) {
	out := map[string]any{
		"additional_context": cursorAdditionalContext,
	}
	return json.Marshal(out)
}

// handleCursorAfterAgentResponse extracts KLYNE_SUMMARY from the
// agent's final text and writes a stop_summaries row keyed by
// conversation_id. One row per assistant turn — same granularity as
// Claude's Stop hook.
//
// The row carries cli='cursor', project_path=workspace_roots[0] (or
// the first non-empty), and ai_drafted_summary=<extracted KLYNE_SUMMARY>.
// Skip / missing / empty captures persist an empty AIDraftedSummary
// just like the Claude path.
func handleCursorAfterAgentResponse(ctx context.Context, in CursorHookInput, db *store.DB, stderr io.Writer) ([]byte, error) {
	out := []byte("{}")

	sessionID := strings.TrimSpace(in.ConversationID)
	if sessionID == "" {
		// No conversation_id → nothing useful to key on. Return empty
		// success so the agent loop keeps going.
		return out, nil
	}

	// Pick the first workspace root as the project_path; fall back
	// to the canonical cursor workspace marker so non-workspace
	// turns are at least groupable.
	projectPath := ""
	for _, root := range in.WorkspaceRoots {
		if strings.TrimSpace(root) != "" {
			projectPath = projectpath.Canonical(root)
			break
		}
	}
	if projectPath == "" {
		projectPath = "(cursor-no-workspace)"
	}

	// Extract the trailing KLYNE_SUMMARY line from this turn's text.
	// Reuses the exact same regex / skip / rune-safe cap the Claude
	// path uses — produces strings comparable across CLIs.
	aiDraftedSummary := extractKlyneSummaryFromText(in.Text)

	// Best-effort plain-text summary for the row's primary `summary`
	// column. We take the first non-empty line of the text so
	// LatestStopSummaryForProject has something to render even if
	// KLYNE_SUMMARY was skip / missing.
	summary := firstNonEmptyLine(in.Text)
	if summary == "" {
		summary = "(cursor turn, no text)"
	}

	if db == nil {
		opened, err := resolveDB(ctx)
		if err != nil {
			return out, fmt.Errorf("open db: %w", err)
		}
		defer opened.Close()
		db = opened
	}

	now := time.Now()
	row := &store.StopSummary{
		SessionID:   sessionID,
		Ts:          now.UnixMilli(),
		ProjectPath: projectPath,
		CLI:         "cursor",
		Summary:     summary,
	}
	if err := store.InsertStopSummary(ctx, db, row); err != nil {
		return out, fmt.Errorf("insert stop summary: %w", err)
	}

	entry := worklog.Entry{
		SessionID:        sessionID,
		TS:               now,
		ProjectPath:      projectPath,
		CLI:              "cursor",
		AIDraftedSummary: aiDraftedSummary,
	}
	if _, werr := worklog.WriteEntry(ctx, db, entry, map[string]bool{}, store.UpsertStopSummaryWithWorklog); werr != nil {
		fmt.Fprintf(stderr, "klyne cursor hook: worklog write failed: %v\n", werr)
	}
	return out, nil
}

// extractKlyneSummaryFromText runs the same regex / skip-walking
// logic ExtractKlyneSummary uses, but on a raw string rather than
// against a []*Message slice. Cursor's afterAgentResponse delivers
// the assistant's final text directly via the payload — no need to
// walk a transcript.
//
// Behaviour: returns the LAST non-skip KLYNE_SUMMARY line found in
// the text (intra-message last-wins). Empty / skip / missing line
// returns "". Rune-safe clipped at klyneSummaryMaxLen.
func extractKlyneSummaryFromText(text string) string {
	if text == "" {
		return ""
	}
	matches := klyneSummaryRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return ""
	}
	var raw string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		candidate := strings.TrimSpace(m[1])
		if candidate == "" || strings.EqualFold(candidate, "skip") {
			continue
		}
		raw = candidate
	}
	if raw == "" {
		return ""
	}
	if len(raw) > klyneSummaryMaxLen {
		raw = clipRuneSafe(raw, klyneSummaryMaxLen)
	}
	return raw
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
// Used for the stop_summary's `summary` column when we have no
// better signal — gives the productivity dashboard something to
// render in the "last seen" slot before a reflection lands.
func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(ln)
		if trim != "" {
			if len(trim) > 200 {
				return clipRuneSafe(trim, 200)
			}
			return trim
		}
	}
	return ""
}
