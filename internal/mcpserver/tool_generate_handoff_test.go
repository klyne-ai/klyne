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

func TestHandleGenerateHandoff_ScopeCurrentTopicFiltersStaleFiles(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/handoff-scope"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	// Build a synthetic transcript: opens with auth, pivots to
	// billing. Auth files should drop from a current-topic handoff;
	// billing files survive. The same input under scope=full keeps
	// all three.
	bigText := strings.Repeat("xxxx", 3000) // 12K bytes per result
	writeJSONL(t, dir, "scope.jsonl",
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"fix the authentication middleware login bug"}]}}`,
		`{"type":"assistant","sessionId":"sess-scope","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","cwd":"/tmp/handoff-scope","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100},"content":[{"type":"tool_use","id":"tc1","name":"Read","input":{"file_path":"/tmp/handoff-scope/auth/login.go"}}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:02.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tc1","content":"`+bigText+`"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:03.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"the session cookie issuer is wrong"}]}}`,
		`{"type":"assistant","sessionId":"sess-scope","uuid":"a2","timestamp":"2026-04-08T10:00:04.000Z","cwd":"/tmp/handoff-scope","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100},"content":[{"type":"tool_use","id":"tc2","name":"Read","input":{"file_path":"/tmp/handoff-scope/auth/session.go"}}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:05.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tc2","content":"`+bigText+`"}]}}`,
		// Topic pivot — billing only in last 5 user messages.
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:06.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"now switch to billing refund logic"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:07.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"the subscription charge isn't applying"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:08.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"billing invoice for the refund flow"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:09.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"credit the customer for the failed charge"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"make sure the refund subscription path is right"}]}}`,
		// Extra billing prompts so the auth files at idx 1 and 4
		// fall outside the recency horizon (recentTouchHorizonUserMsgs
		// user messages).
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.100Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"double check the refund authorization code"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.200Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"the refund retry policy needs adjusting"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.300Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"verify the subscription invoice format"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.400Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"ensure refund credits log correctly"}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:10.500Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"text","text":"and the customer notification email"}]}}`,
		`{"type":"assistant","sessionId":"sess-scope","uuid":"a3","timestamp":"2026-04-08T10:00:11.000Z","cwd":"/tmp/handoff-scope","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100},"content":[{"type":"tool_use","id":"tc3","name":"Read","input":{"file_path":"/tmp/handoff-scope/billing/refund.go"}}]}}`,
		`{"type":"user","sessionId":"sess-scope","timestamp":"2026-04-08T10:00:12.000Z","cwd":"/tmp/handoff-scope","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tc3","content":"`+bigText+`"}]}}`,
	)

	full := mustHandoff(t, HandoffInput{CWD: cwd})
	if full.Ambiguous {
		t.Fatalf("full handoff unexpectedly ambiguous")
	}
	if !strings.Contains(full.Markdown, "auth/login.go") {
		t.Fatalf("full handoff should keep login.go: %s", full.Markdown)
	}
	if !strings.Contains(full.Markdown, "billing/refund.go") {
		t.Fatalf("full handoff should keep refund.go: %s", full.Markdown)
	}

	scoped := mustHandoff(t, HandoffInput{CWD: cwd, Scope: "current-topic"})
	if scoped.Ambiguous {
		t.Fatalf("scoped handoff unexpectedly ambiguous")
	}
	if strings.Contains(scoped.Markdown, "auth/login.go") {
		t.Fatalf("scoped handoff should drop login.go (stale): %s", scoped.Markdown)
	}
	if strings.Contains(scoped.Markdown, "auth/session.go") {
		t.Fatalf("scoped handoff should drop session.go (stale): %s", scoped.Markdown)
	}
	if !strings.Contains(scoped.Markdown, "billing/refund.go") {
		t.Fatalf("scoped handoff should keep refund.go (relevant): %s", scoped.Markdown)
	}
	if !strings.Contains(scoped.Markdown, "Scope: current-topic") {
		t.Fatalf("scoped handoff should declare its scope: %s", scoped.Markdown)
	}
}
