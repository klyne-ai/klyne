package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleGenerateHandoff_AmbiguousReturnsCandidates(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/handoff-ambig"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Two parallel sessions, both fresh → both active → ambiguous.
	writeJSONL(t, dir, "a.jsonl",
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task one"}]}}`,
	)
	writeJSONL(t, dir, "b.jsonl",
		`{"type":"user","sessionId":"sess-b","timestamp":"2026-04-08T11:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task two"}]}}`,
	)

	out := mustHandoff(t, HandoffInput{CWD: cwd})
	if !out.Ambiguous {
		t.Errorf("Ambiguous = false, want true (two active sessions)")
	}
	if len(out.Candidates) != 2 {
		t.Errorf("Candidates len = %d, want 2", len(out.Candidates))
	}
	if out.Markdown != "" {
		t.Errorf("Markdown should be empty when ambiguous; got %d chars", len(out.Markdown))
	}
}

func TestHandleGenerateHandoff_HappyPathReturnsMarkdown(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/handoff-ok"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Realistic conversation: one user prompt, one assistant turn that
	// invoked Read + Bash, plus a tool error to surface in failures.
	writeJSONL(t, dir, "single.jsonl",
		`{"type":"user","sessionId":"sess-handoff","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/handoff-ok","message":{"role":"user","content":[{"type":"text","text":"please refactor the auth middleware"}]}}`,
		`{"type":"assistant","sessionId":"sess-handoff","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","cwd":"/tmp/handoff-ok","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":4000},"content":[{"type":"text","text":"On it."},{"type":"tool_use","id":"tc1","name":"Read","input":{"file_path":"/tmp/handoff-ok/auth.go"}},{"type":"tool_use","id":"tc2","name":"Bash","input":{"command":"go test ./auth/..."}}]}}`,
		`{"type":"user","sessionId":"sess-handoff","timestamp":"2026-04-08T10:00:02.000Z","cwd":"/tmp/handoff-ok","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tc2","content":"FAIL TestLogin","is_error":true}]}}`,
	)

	out := mustHandoff(t, HandoffInput{CWD: cwd})
	if out.Ambiguous {
		t.Errorf("Ambiguous = true, want false")
	}
	if out.SessionID != "sess-handoff" {
		t.Errorf("SessionID = %q, want sess-handoff", out.SessionID)
	}
	if out.ProjectPath != "/tmp/handoff-ok" {
		t.Errorf("ProjectPath = %q, want /tmp/handoff-ok", out.ProjectPath)
	}
	md := out.Markdown
	mustContain := []string{
		"# Handoff from session `sess-han`",
		"/tmp/handoff-ok",
		"## Recent task",
		"refactor the auth middleware",
		"## Files touched",
		"auth.go",
		"## Commands run",
		"go test",
		"## Known failures",
		"FAIL TestLogin",
	}
	for _, s := range mustContain {
		if !strings.Contains(md, s) {
			t.Errorf("handoff markdown missing %q:\n---\n%s\n---", s, md)
		}
	}
}

func mustHandoff(t *testing.T, in HandoffInput) HandoffOutput {
	t.Helper()
	_, out, err := HandleGenerateHandoff(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleGenerateHandoff: %v", err)
	}
	return out
}
