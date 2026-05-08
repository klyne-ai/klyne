package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJSONL drops a fixture transcript on disk for a single test and
// returns its absolute path. Lines are written verbatim, one per arg,
// followed by a newline.
func writeJSONL(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Set mtime to "now" so latest-by-mtime resolution finds it.
	now := time.Now()
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

func TestHandleGetContextHealth_NoSessionFound(t *testing.T) {
	withFakeHome(t)
	out := mustCall(t, GetContextHealthInput{CWD: "/tmp/no-such-project"})
	if out.SessionID != "" {
		t.Errorf("SessionID = %q, want empty when no session found", out.SessionID)
	}
}

func TestHandleGetContextHealth_HighFillReportsRescue(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-rescue"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Single assistant message at 850K cache-aware tokens against an
	// Opus 4.7 1M window → 85% fill → rescue_now per the rubric.
	writeJSONL(t, dir, "rescue.jsonl",
		`{"type":"user","sessionId":"sess-rescue","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/proj-rescue","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"assistant","sessionId":"sess-rescue","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","cwd":"/tmp/proj-rescue","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":850000}}}`,
	)
	out := mustCall(t, GetContextHealthInput{CWD: cwd})
	if out.State != "rescue_now" {
		t.Errorf("State = %q, want rescue_now (85%% fill)", out.State)
	}
	if out.Action != "start_fresh" {
		t.Errorf("Action = %q, want start_fresh", out.Action)
	}
	if out.ContextFillPct < 84 || out.ContextFillPct > 86 {
		t.Errorf("ContextFillPct = %.2f, want ~85", out.ContextFillPct)
	}
	if out.SessionID != "sess-rescue" {
		t.Errorf("SessionID = %q, want sess-rescue", out.SessionID)
	}
}

func TestHandleGetContextHealth_LowFillReportsHealthy(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-healthy"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// 5K tokens against 1M window → 0.5% → Healthy.
	writeJSONL(t, dir, "healthy.jsonl",
		`{"type":"assistant","sessionId":"sess-healthy","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/proj-healthy","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":5000}}}`,
	)
	out := mustCall(t, GetContextHealthInput{CWD: cwd})
	if out.State != "healthy" {
		t.Errorf("State = %q, want healthy", out.State)
	}
	if out.Action != "continue" {
		t.Errorf("Action = %q, want continue", out.Action)
	}
}

func TestHandleGetContextHealth_RespectsExplicitSessionID(t *testing.T) {
	home := withFakeHome(t)
	dir := filepath.Join(home, ".claude", "projects", "-tmp-some-other-cwd")
	writeJSONL(t, dir, "explicit-target.jsonl",
		`{"type":"assistant","sessionId":"explicit-target","uuid":"a","timestamp":"2026-04-08T10:00:00.000Z","cwd":"/tmp/some-other-cwd","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":5000}}}`,
	)
	// Pass session_id explicitly; CWD does not matter.
	out := mustCall(t, GetContextHealthInput{SessionID: "explicit-target", CWD: "/tmp/never-existed"})
	if out.SessionID != "explicit-target" {
		t.Errorf("SessionID = %q, want explicit-target (explicit id wins)", out.SessionID)
	}
}

// mustCall is a thin wrapper around HandleGetContextHealth for tests
// that always expect a non-error return — it asserts cleanly on the
// happy path and fails fast otherwise.
func mustCall(t *testing.T, in GetContextHealthInput) GetContextHealthOutput {
	t.Helper()
	_, out, err := HandleGetContextHealth(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleGetContextHealth: %v", err)
	}
	return out
}
