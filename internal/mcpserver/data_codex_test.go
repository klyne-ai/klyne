package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// TestLoadSnapshot_DispatchesToCodexParser asserts LoadSnapshot reads
// a Codex rollout file using the Codex parser. Verified by checking
// that user messages come through in the canonical Message shape with
// the Codex content extracted (Codex content is `[{type:"input_text",
// text:...}]`, NOT Claude's `[{type:"text",text:...}]` — using the
// wrong parser would yield empty content).
func TestLoadSnapshot_DispatchesToCodexParser(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	// Build a rollout file directly under ~/.codex/sessions/...
	dir := filepath.Join(home, ".codex", "sessions", "2026", "05", "07")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "rollout-test-codex.jsonl")
	body := `{"timestamp":"2026-05-07T10:00:00.000Z","type":"session_meta","payload":{"id":"codex-snap","cwd":"/tmp/codex-snap","model_provider":"openai"}}` + "\n" +
		`{"timestamp":"2026-05-07T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5"}}` + "\n" +
		`{"timestamp":"2026-05-07T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor the cost engine"}]}}` + "\n" +
		`{"timestamp":"2026-05-07T10:00:03.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"on it"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot err = %v", err)
	}
	if snap.SessionID != "codex-snap" {
		t.Errorf("SessionID = %q, want codex-snap", snap.SessionID)
	}
	// Expect at least the user + assistant messages parsed.
	var sawUser, sawAssistant bool
	for _, m := range snap.Messages {
		if m.CLI != connectors.CLICodex {
			t.Errorf("message CLI = %q, want codex (parser dispatched wrong)", m.CLI)
		}
		if m.Role == connectors.RoleUser && m.Content == "refactor the cost engine" {
			sawUser = true
		}
		if m.Role == connectors.RoleAssistant && m.Content == "on it" {
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Errorf("did not see parsed user message — codex parser likely not used")
	}
	if !sawAssistant {
		t.Errorf("did not see parsed assistant message — codex parser likely not used")
	}
}

// TestLoadSnapshot_ClaudePathStillWorks pins the existing Claude
// behaviour after the dispatch refactor.
func TestLoadSnapshot_ClaudePathStillWorks(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD("/tmp/claude-snap"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "claude.jsonl")
	body := `{"type":"user","sessionId":"claude-snap","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/claude-snap","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if snap.SessionID != "claude-snap" {
		t.Errorf("SessionID = %q, want claude-snap", snap.SessionID)
	}
	if len(snap.Messages) == 0 || snap.Messages[0].Content != "hello" {
		t.Errorf("expected parsed claude user message; got %d messages", len(snap.Messages))
	}
}
