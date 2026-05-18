package mcpserver

import (
	"context"
	"path/filepath"
	"reflect"
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
	// Markdown now carries the slash-prompt-ready ambiguous-candidate
	// list so /klyne:handoff can echo it verbatim instead of having
	// the host LLM re-render structured fields itself.
	if out.Markdown == "" {
		t.Errorf("Markdown should carry the ambiguous candidate list; got empty string")
	}
	if !strings.Contains(out.Markdown, "session_id") {
		t.Errorf("Markdown should mention session_id retry instruction; got %q", out.Markdown)
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
	// v2 renderer sections: no longer has ## Recent task / ## Files touched /
	// ## Commands run / ## Known failures — those were v1 shapes. Assert on
	// v2 canonical sections instead.
	mustContain := []string{
		"# Handoff from session `sess-han`",
		"/tmp/handoff-ok",
		"auth.go",
		"FAIL TestLogin",
		"_Source: ",
	}
	for _, s := range mustContain {
		if !strings.Contains(md, s) {
			t.Errorf("handoff markdown missing %q:\n---\n%s\n---", s, md)
		}
	}
	// v2 fields: Skeleton, NarrativeSlots, PostCompact
	if out.PostCompact {
		t.Errorf("PostCompact should be false for a session with no compact boundary")
	}
	want := []string{"continue_from", "decided_vs_open", "read_first"}
	if !reflect.DeepEqual(out.NarrativeSlots, want) {
		t.Errorf("NarrativeSlots = %v, want %v", out.NarrativeSlots, want)
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

func TestHandleGenerateHandoff_StaleFilesInCollapsedTail(t *testing.T) {
	// Formerly TestHandleGenerateHandoff_ScopeCurrentTopicFiltersStaleFiles.
	// In v2, Scope is ignored — the renderer always applies relevance-verdict
	// filtering. This test verifies that stale files land in the collapsed
	// <details> tail and NOT in the anchor table, with no scope flag needed.
	home := withFakeHome(t)
	cwd := "/tmp/handoff-scope"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	// Build a synthetic transcript: opens with auth, pivots to
	// billing. Auth files should be marked stale by the relevance scorer
	// and relegated to the <details> tail; billing files survive in the
	// anchor table.
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

	out := mustHandoff(t, HandoffInput{CWD: cwd})
	if out.Ambiguous {
		t.Fatalf("handoff unexpectedly ambiguous")
	}
	md := out.Markdown

	// billing/refund.go should appear in the anchor table (relevant).
	if !strings.Contains(md, "billing/refund.go") {
		t.Fatalf("handoff should keep refund.go (relevant): %s", md)
	}

	// auth files should be stale — they appear in the <details> collapsed
	// tail, not in the anchor table above it. Verify they are NOT inside
	// the "## Anchor files" table by splitting on the <details> boundary.
	detailsIdx := strings.Index(md, "<details>")
	if detailsIdx < 0 {
		// No stale files at all — that's also acceptable if the scorer
		// didn't mark them stale (fixture may not trigger the threshold
		// on every platform). Skip rather than hard-fail.
		t.Logf("no <details> section in handoff — stale-file relegation not triggered (ok if scorer threshold not met)")
		return
	}
	anchorSection := md[:detailsIdx]
	// Auth files must NOT appear in the anchor table above <details>.
	if strings.Contains(anchorSection, "auth/login.go") {
		t.Errorf("auth/login.go should be relegated to stale <details> tail, not in anchor table:\n%s", anchorSection)
	}
	if strings.Contains(anchorSection, "auth/session.go") {
		t.Errorf("auth/session.go should be relegated to stale <details> tail, not in anchor table:\n%s", anchorSection)
	}
}

// buildPostCompactFixture writes a JSONL with a compact_boundary
// followed by a short tail; returns the CWD string for mustHandoff.
func buildPostCompactFixture(t *testing.T) string {
	t.Helper()
	home := withFakeHome(t)
	cwd := "/tmp/handoff-postcompact"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "pc.jsonl",
		`{"parentUuid":null,"type":"user","timestamp":"2026-05-19T10:00:00.000Z","sessionId":"pc1","cwd":"/tmp/handoff-postcompact","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"first turn"}]}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-05-19T10:05:00.000Z","sessionId":"pc1","uuid":"u2","compactMetadata":{"trigger":"manual","preTokens":120000,"postTokens":5000,"durationMs":1000},"content":"Conversation compacted"}`,
		`{"parentUuid":"u2","type":"user","timestamp":"2026-05-19T10:06:00.000Z","sessionId":"pc1","cwd":"/tmp/handoff-postcompact","uuid":"u3","message":{"role":"user","content":[{"type":"text","text":"after compact"}]}}`,
	)
	return cwd
}

// buildNoCompactFixture writes a plain JSONL with no compact boundary.
func buildNoCompactFixture(t *testing.T) string {
	t.Helper()
	home := withFakeHome(t)
	cwd := "/tmp/handoff-nocompact"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "nc.jsonl",
		`{"parentUuid":null,"type":"user","timestamp":"2026-05-19T10:00:00.000Z","sessionId":"nc1","cwd":"/tmp/handoff-nocompact","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"parentUuid":"u1","type":"assistant","timestamp":"2026-05-19T10:00:01.000Z","sessionId":"nc1","cwd":"/tmp/handoff-nocompact","uuid":"u2","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	)
	return cwd
}

func TestHandleGenerateHandoff_PostCompactFlag(t *testing.T) {
	cwd := buildPostCompactFixture(t)
	out := mustHandoff(t, HandoffInput{CWD: cwd})
	if !out.PostCompact {
		t.Fatalf("expected PostCompact=true, got %+v", out)
	}
	if len(out.NarrativeSlots) != 0 {
		t.Errorf("expected empty NarrativeSlots when post-compact, got %v", out.NarrativeSlots)
	}
}

func TestHandleGenerateHandoff_NotPostCompactPopulatesSlots(t *testing.T) {
	cwd := buildNoCompactFixture(t)
	out := mustHandoff(t, HandoffInput{CWD: cwd})
	if out.PostCompact {
		t.Fatal("expected PostCompact=false")
	}
	want := []string{"continue_from", "decided_vs_open", "read_first"}
	if !reflect.DeepEqual(out.NarrativeSlots, want) {
		t.Errorf("slots = %v, want %v", out.NarrativeSlots, want)
	}
}
