package mcpserver

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHandleListSessions_NoSessions(t *testing.T) {
	withFakeHome(t)
	out := mustListSessions(t, ListSessionsInput{CWD: "/tmp/proj-empty-list"})
	if len(out.Candidates) != 0 {
		t.Errorf("Candidates len = %d, want 0", len(out.Candidates))
	}
	if out.CWD != "/tmp/proj-empty-list" {
		t.Errorf("CWD = %q, want /tmp/proj-empty-list", out.CWD)
	}
}

func TestHandleListSessions_MultipleSessionsActiveFlagged(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-list"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "session-a.jsonl",
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task A"}]}}`,
	)
	writeJSONL(t, dir, "session-b.jsonl",
		`{"type":"user","sessionId":"sess-b","timestamp":"2026-04-08T11:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task B different"}]}}`,
	)

	out := mustListSessions(t, ListSessionsInput{CWD: cwd})
	if len(out.Candidates) != 2 {
		t.Fatalf("Candidates len = %d, want 2", len(out.Candidates))
	}
	// Both freshly written → both IsActive (within the 30s window).
	for _, c := range out.Candidates {
		if !c.IsActive {
			t.Errorf("expected IsActive=true for fresh session %s", c.SessionID)
		}
		if c.Preview == "" {
			t.Errorf("expected non-empty preview for %s", c.SessionID)
		}
	}
}

func mustListSessions(t *testing.T, in ListSessionsInput) ListSessionsOutput {
	t.Helper()
	_, out, err := HandleListSessions(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleListSessions: %v", err)
	}
	return out
}
