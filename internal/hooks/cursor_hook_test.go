package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

// cursorHookForTest mirrors CursorHook but takes an explicit *store.DB
// so tests can use openHookTestDB instead of going through resolveDB.
// Production callers go through CursorHook (db=nil → resolveDB).
func cursorHookForTest(t *testing.T, payload string, db *store.DB) Result {
	t.Helper()
	var errb bytes.Buffer
	out, err := computeCursorHook(context.Background(), strings.NewReader(payload), db, &errb)
	if err != nil {
		errb.WriteString("klyne cursor hook: " + err.Error() + "\n")
	}
	if out == nil {
		out = []byte("{}")
	}
	return Result{Stdout: out, Stderr: errb.Bytes()}
}

// TestCursorHook_SessionStartInjectsAdditionalContext pins the
// contract that sessionStart returns the KLYNE_SUMMARY-emit
// instruction in the `additional_context` field. This is the only
// injection channel Cursor exposes for sessionStart, and it's what
// drives the model to emit `KLYNE_SUMMARY: ...` at the end of every
// reply — exactly like the Claude path.
func TestCursorHook_SessionStartInjectsAdditionalContext(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "sessionStart",
		"conversation_id": "conv-abc",
		"session_id":      "sess-xyz",
		"workspace_roots": []string{"/tmp/whatever"},
	})

	res := CursorHook(context.Background(), strings.NewReader(string(payload)), nil)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d; want 0 (cursor hooks must never block); stderr=%s", res.ExitCode, res.Stderr)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		t.Fatalf("decode stdout: %v\nraw=%s", err, res.Stdout)
	}
	ctx, _ := out["additional_context"].(string)
	if !strings.Contains(ctx, "KLYNE_SUMMARY:") {
		t.Errorf("additional_context should contain KLYNE_SUMMARY: instruction, got %q", ctx)
	}
}

// TestCursorHook_AfterAgentResponsePopulatesAIDraftedSummary is the
// end-to-end invariant: an afterAgentResponse event whose `text`
// ends with `KLYNE_SUMMARY: <line>` produces a stop_summaries row
// with cli='cursor' and ai_drafted_summary = <line>.
func TestCursorHook_AfterAgentResponsePopulatesAIDraftedSummary(t *testing.T) {
	db := openHookTestDB(t)
	const conv = "conv-cursor-1"
	const want = "Implemented X and ran tests; all green."

	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "afterAgentResponse",
		"conversation_id": conv,
		"workspace_roots": []string{"/tmp/cursor_e2e"},
		"text":            "Done.\nKLYNE_SUMMARY: " + want,
	})

	res := cursorHookForTest(t, string(payload), db)
	if string(res.Stdout) != "{}" {
		t.Fatalf("stdout = %q; want \"{}\"", res.Stdout)
	}

	var (
		cli string
		got string
	)
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT cli, COALESCE(ai_drafted_summary,'') FROM stop_summaries WHERE session_id = ?`,
		conv).Scan(&cli, &got); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if cli != "cursor" {
		t.Errorf("cli = %q; want cursor", cli)
	}
	if got != want {
		t.Fatalf("ai_drafted_summary = %q; want %q", got, want)
	}
}

// TestCursorHook_AfterAgentResponseSkipLeavesEmpty pins the inverse:
// a `KLYNE_SUMMARY: skip` line writes the row but leaves
// ai_drafted_summary empty — same semantics as the Claude path.
func TestCursorHook_AfterAgentResponseSkipLeavesEmpty(t *testing.T) {
	db := openHookTestDB(t)
	const conv = "conv-cursor-skip"

	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": "afterAgentResponse",
		"conversation_id": conv,
		"workspace_roots": []string{"/tmp/cursor_skip"},
		"text":            "Nothing interesting.\nKLYNE_SUMMARY: skip",
	})

	cursorHookForTest(t, string(payload), db)

	var got string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT COALESCE(ai_drafted_summary,'') FROM stop_summaries WHERE session_id = ?`,
		conv).Scan(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "" {
		t.Fatalf("ai_drafted_summary should be empty for skip sentinel, got %q", got)
	}
}

// TestCursorHook_UnknownEventNoOp ensures every unhandled event
// returns the empty-object fail-open response Cursor expects.
func TestCursorHook_UnknownEventNoOp(t *testing.T) {
	for _, event := range []string{"beforeSubmitPrompt", "stop", "sessionEnd", "afterFileEdit", "preToolUse"} {
		payload, _ := json.Marshal(map[string]any{
			"hook_event_name": event,
			"conversation_id": "conv-noop",
			"workspace_roots": []string{"/tmp"},
		})
		res := CursorHook(context.Background(), strings.NewReader(string(payload)), nil)
		if res.ExitCode != 0 {
			t.Errorf("event %s exit = %d; want 0", event, res.ExitCode)
		}
		if string(res.Stdout) != "{}" {
			t.Errorf("event %s stdout = %q; want \"{}\"", event, res.Stdout)
		}
	}
}

// TestExtractKlyneSummaryFromText covers the helper directly: pick
// last real KLYNE_SUMMARY line, ignore skip, return empty when none
// found.
func TestExtractKlyneSummaryFromText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no marker", "just some prose", ""},
		{"single real", "blah\nKLYNE_SUMMARY: did X", "did X"},
		{"skip only", "blah\nKLYNE_SUMMARY: skip", ""},
		{"real then skip — real wins", "KLYNE_SUMMARY: real\nKLYNE_SUMMARY: skip", "real"},
		{"two reals — last wins", "KLYNE_SUMMARY: first\nKLYNE_SUMMARY: second", "second"},
		{"leading whitespace ok", "   KLYNE_SUMMARY:   trimmed   ", "trimmed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKlyneSummaryFromText(tc.in)
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}
