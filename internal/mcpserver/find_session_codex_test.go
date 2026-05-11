package mcpserver

import (
	"path/filepath"
	"testing"
	"time"
)

// TestFindSessionByID_FindsCodexSession asserts the session-id
// resolver looks under ~/.codex/sessions in addition to
// ~/.claude/projects, so the AI can pass an explicit Codex session id
// and have any tool resolve to its rollout file.
func TestFindSessionByID_FindsCodexSession(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	want := makeCodexSession(t, home, "019e038a-2082-7640-b175-6e6d350d5f98", "/tmp/cx-byid", time.Now())

	got, err := FindSessionByID("019e038a-2082-7640-b175-6e6d350d5f98")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != want {
		t.Errorf("FindSessionByID = %q, want %q", got, want)
	}
}

func TestFindSessionByID_StillFindsClaudeSession(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	makeProjectDir(t, home, EncodeCWD("/tmp/cl-byid"), "abc-claude.jsonl")
	got, err := FindSessionByID("abc-claude")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if filepath.Base(got) != "abc-claude.jsonl" {
		t.Errorf("FindSessionByID base = %q, want abc-claude.jsonl", filepath.Base(got))
	}
}

func TestFindSessionByID_NoMatchReturnsEmpty(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	withFakeHome(t)
	got, err := FindSessionByID("does-not-exist-anywhere")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "" {
		t.Errorf("FindSessionByID = %q, want empty", got)
	}
}
