package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// codexCompactedJSONLFixture builds a single-line Codex rollout
// transcript ending with a `type:"compacted"` event whose
// payload.replacement_history embeds the supplied user/assistant
// turns. Used by every Codex pre-compact test below to keep them
// readable.
func codexCompactedJSONLFixture(t *testing.T, home, cwd, sessionID string, replacement string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "05", "08")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "rollout-"+sessionID+".jsonl")
	body := `{"timestamp":"2026-05-08T10:00:00.000Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"` + cwd + `","model_provider":"openai"}}` + "\n" +
		`{"timestamp":"2026-05-08T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"pre-compact original turn"}]}}` + "\n" +
		`{"timestamp":"2026-05-08T10:05:00.000Z","type":"compacted","payload":{"message":"summary of work","replacement_history":[` + replacement + `]}}` + "\n" +
		`{"timestamp":"2026-05-08T10:05:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"post-compact follow-up"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestHandleGetPreCompactContext_CodexRecoversReplacementHistory(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/codex-pc-recover"
	replacement := `{"type":"message","role":"user","content":[{"type":"input_text","text":"first user thing"}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"replied to first"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"second user thing"}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"replied to second"}]}`
	codexCompactedJSONLFixture(t, home, cwd, "cx-pc-001", replacement)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd})
	if !out.FoundCompact {
		t.Fatalf("FoundCompact = false, want true (Codex compacted event present)")
	}
	if len(out.Messages) != 4 {
		t.Fatalf("Messages len = %d, want 4 (4 items in replacement_history)", len(out.Messages))
	}
	wantTexts := []string{"first user thing", "replied to first", "second user thing", "replied to second"}
	for i, want := range wantTexts {
		if out.Messages[i].Content != want {
			t.Errorf("Messages[%d].Content = %q, want %q", i, out.Messages[i].Content, want)
		}
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	for i, want := range wantRoles {
		if out.Messages[i].Role != want {
			t.Errorf("Messages[%d].Role = %q, want %q", i, out.Messages[i].Role, want)
		}
	}
}

func TestHandleGetPreCompactContext_CodexOmitsPreTokensAndTrigger(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	// Codex compacted events do NOT carry preTokens / trigger metadata.
	// The output must reflect that honestly (zero / empty), not pretend.
	home := withFakeHome(t)
	cwd := "/tmp/codex-pc-meta"
	replacement := `{"type":"message","role":"user","content":[{"type":"input_text","text":"only thing"}]}`
	codexCompactedJSONLFixture(t, home, cwd, "cx-pc-meta", replacement)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd})
	if !out.FoundCompact {
		t.Fatalf("FoundCompact = false, want true")
	}
	if out.PreTokens != 0 {
		t.Errorf("PreTokens = %d, want 0 (Codex does not expose)", out.PreTokens)
	}
	if out.Trigger != "" {
		t.Errorf("Trigger = %q, want \"\" (Codex does not distinguish manual/auto)", out.Trigger)
	}
}

func TestHandleGetPreCompactContext_CodexNoCompactYet(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	// Codex session with messages but no compacted event should report
	// FoundCompact=false, same as the Claude path's equivalent test.
	home := withFakeHome(t)
	cwd := "/tmp/codex-pc-fresh"
	dir := filepath.Join(home, ".codex", "sessions", "2026", "05", "08")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "rollout-cx-fresh.jsonl")
	body := `{"timestamp":"2026-05-08T10:00:00.000Z","type":"session_meta","payload":{"id":"cx-fresh","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-05-08T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := mustPreCompact(t, PreCompactInput{CWD: cwd})
	if out.FoundCompact {
		t.Errorf("FoundCompact = true, want false (no compacted event)")
	}
	if len(out.Messages) != 0 {
		t.Errorf("Messages len = %d, want 0", len(out.Messages))
	}
}

func TestHandleGetPreCompactContext_CodexRespectsLimit(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	// Codex limit semantics mirror Claude: return the most recent N.
	home := withFakeHome(t)
	cwd := "/tmp/codex-pc-limit"
	// 4 items in replacement_history, ask for limit=2 → expect last 2.
	replacement := `{"type":"message","role":"user","content":[{"type":"input_text","text":"m1"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"m2"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"m3"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"m4"}]}`
	codexCompactedJSONLFixture(t, home, cwd, "cx-pc-limit", replacement)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd, Limit: 2})
	if len(out.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Content != "m3" || out.Messages[1].Content != "m4" {
		t.Errorf("Messages = %+v, want m3, m4 (most recent two)", out.Messages)
	}
}
