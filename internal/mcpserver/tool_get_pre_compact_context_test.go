package mcpserver

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHandleGetPreCompactContext_NoCompactYet(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/precompact-empty"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "fresh.jsonl",
		`{"type":"user","sessionId":"sess-fresh","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"assistant","sessionId":"sess-fresh","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":1000}}}`,
	)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd})
	if out.FoundCompact {
		t.Errorf("FoundCompact = true, want false (no /compact in transcript)")
	}
	if len(out.Messages) != 0 {
		t.Errorf("Messages len = %d, want 0", len(out.Messages))
	}
}

func TestHandleGetPreCompactContext_RecoversMessagesBeforeBoundary(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/precompact-with"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Layout: 3 user/asst pairs, then a compact_boundary, then 1 more
	// post-compact pair. The tool must return only the pre-compact 6
	// messages (or fewer with `limit`).
	writeJSONL(t, dir, "with-compact.jsonl",
		`{"type":"user","sessionId":"sess-pc","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"first thing"}]}}`,
		`{"type":"assistant","sessionId":"sess-pc","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"replied to first"}],"usage":{"input_tokens":1,"cache_read_input_tokens":2000}}}`,
		`{"type":"user","sessionId":"sess-pc","timestamp":"2026-04-08T10:00:02.000Z","message":{"role":"user","content":[{"type":"text","text":"second thing"}]}}`,
		`{"type":"assistant","sessionId":"sess-pc","uuid":"a2","timestamp":"2026-04-08T10:00:03.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"replied to second"}],"usage":{"input_tokens":1,"cache_read_input_tokens":4000}}}`,
		`{"type":"user","sessionId":"sess-pc","timestamp":"2026-04-08T10:00:04.000Z","message":{"role":"user","content":[{"type":"text","text":"third thing"}]}}`,
		`{"type":"assistant","sessionId":"sess-pc","uuid":"a3","timestamp":"2026-04-08T10:00:05.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"replied to third"}],"usage":{"input_tokens":1,"cache_read_input_tokens":6000}}}`,
		`{"type":"system","subtype":"compact_boundary","sessionId":"sess-pc","uuid":"c1","timestamp":"2026-04-08T10:00:06.000Z","compactMetadata":{"trigger":"manual","preTokens":250000}}`,
		`{"type":"user","sessionId":"sess-pc","timestamp":"2026-04-08T10:00:07.000Z","message":{"role":"user","content":[{"type":"text","text":"post-compact follow-up"}]}}`,
		`{"type":"assistant","sessionId":"sess-pc","uuid":"a4","timestamp":"2026-04-08T10:00:08.000Z","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"after compact"}],"usage":{"input_tokens":1,"cache_read_input_tokens":1500}}}`,
	)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd})
	if !out.FoundCompact {
		t.Fatalf("FoundCompact = false, want true")
	}
	if out.Trigger != "manual" {
		t.Errorf("Trigger = %q, want manual", out.Trigger)
	}
	if out.PreTokens != 250000 {
		t.Errorf("PreTokens = %d, want 250000", out.PreTokens)
	}
	// 6 pre-compact messages total; default limit is 30 so all 6 returned.
	if len(out.Messages) != 6 {
		t.Errorf("Messages len = %d, want 6", len(out.Messages))
	}
	// First message content should be "first thing", last "replied to third".
	if out.Messages[0].Content != "first thing" {
		t.Errorf("Messages[0] = %q, want %q", out.Messages[0].Content, "first thing")
	}
	if out.Messages[5].Content != "replied to third" {
		t.Errorf("Messages[5] = %q, want %q", out.Messages[5].Content, "replied to third")
	}
	// The post-compact follow-up must NOT appear.
	for i, m := range out.Messages {
		if m.Content == "post-compact follow-up" || m.Content == "after compact" {
			t.Errorf("post-compact message leaked into output at index %d: %q", i, m.Content)
		}
	}
}

func TestHandleGetPreCompactContext_RespectsLimit(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/precompact-limit"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "limited.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"m1"}]}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"m2"}]}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:02.000Z","message":{"role":"user","content":[{"type":"text","text":"m3"}]}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":"m4"}]}}`,
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c","timestamp":"2026-04-08T10:00:04.000Z","compactMetadata":{"trigger":"auto","preTokens":900000}}`,
	)

	out := mustPreCompact(t, PreCompactInput{CWD: cwd, Limit: 2})
	if !out.FoundCompact {
		t.Fatal("FoundCompact = false, want true")
	}
	if len(out.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2 (limit was 2)", len(out.Messages))
	}
	// Limit pulls the most recent N, so we should see m3 and m4.
	if out.Messages[0].Content != "m3" || out.Messages[1].Content != "m4" {
		t.Errorf("Messages = %+v, want m3, m4", out.Messages)
	}
	if out.Trigger != "auto" {
		t.Errorf("Trigger = %q, want auto", out.Trigger)
	}
}

func mustPreCompact(t *testing.T, in PreCompactInput) PreCompactOutput {
	t.Helper()
	_, out, err := HandleGetPreCompactContext(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleGetPreCompactContext: %v", err)
	}
	return out
}
