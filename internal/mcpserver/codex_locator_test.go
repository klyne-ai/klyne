package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// TestListSessionsForCWD_TagsClaudeWithCLI asserts existing Claude-only
// behaviour still works AND each candidate carries CLI=claude. This
// pins the new SessionCandidate.CLI field as part of the contract so
// downstream tools can dispatch to the right parser.
func TestListSessionsForCWD_TagsClaudeWithCLI(t *testing.T) {
	// Cannot t.Parallel: uses t.Setenv via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/cli-tag-claude"
	makeProjectDir(t, home, EncodeCWD(cwd), "only.jsonl")

	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d, want 1", len(cands))
	}
	if cands[0].CLI != connectors.CLIClaude {
		t.Errorf("CLI = %q, want %q", cands[0].CLI, connectors.CLIClaude)
	}
}

// makeCodexSession writes a synthetic Codex rollout-*.jsonl under
// ~/.codex/sessions/YYYY/MM/DD/ with one session_meta line carrying
// the given session id and cwd. Spaces mtime so a sequence of calls
// produces deterministic ordering.
//
// Returns the absolute file path so tests can assert on it.
func makeCodexSession(t *testing.T, home, sessionID, cwd string, mtime time.Time) string {
	t.Helper()
	// Codex layout: ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
	yyyy := mtime.Format("2006")
	mm := mtime.Format("01")
	dd := mtime.Format("02")
	dir := filepath.Join(home, ".codex", "sessions", yyyy, mm, dd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	name := "rollout-" + sessionID + ".jsonl"
	path := filepath.Join(dir, name)
	// Real Codex shape: {timestamp, type:"session_meta", payload:{id, cwd, ...}}
	line := `{"timestamp":"2026-05-07T17:46:56.245Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"` + cwd + `","model_provider":"openai"}}` + "\n"
	// Add one user message line so MsgCount > 1 and the file looks real.
	user := `{"timestamp":"2026-05-07T17:47:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor the auth module"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line+user), 0o644); err != nil {
		t.Fatalf("write codex file: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return path
}

func TestListSessionsForCWD_FindsCodexSessionByExactCWD(t *testing.T) {
	// Cannot t.Parallel: uses t.Setenv.
	home := withFakeHome(t)
	cwd := "/tmp/codex-exact"
	makeCodexSession(t, home, "codex-001", cwd, time.Now())

	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	if cands[0].CLI != connectors.CLICodex {
		t.Errorf("CLI = %q, want %q", cands[0].CLI, connectors.CLICodex)
	}
	if cands[0].SessionID != "codex-001" {
		t.Errorf("SessionID = %q, want codex-001", cands[0].SessionID)
	}
}

func TestListSessionsForCWD_FindsCodexFromDescendantCWD(t *testing.T) {
	// Cannot t.Parallel: uses t.Setenv.
	// Codex session was launched from /tmp/parent. The MCP call
	// arrives with cwd=/tmp/parent/sub/inner. We should still find it
	// because the requested cwd is a descendant of the session's cwd
	// (mirrors Claude's "walk up to nearest project dir" semantics).
	home := withFakeHome(t)
	makeCodexSession(t, home, "codex-parent", "/tmp/parent", time.Now())

	cands, err := ListSessionsForCWD("/tmp/parent/sub/inner")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1 (descendant lookup)", len(cands))
	}
	if cands[0].CLI != connectors.CLICodex {
		t.Errorf("CLI = %q, want codex", cands[0].CLI)
	}
}

func TestListSessionsForCWD_DoesNotMatchSiblingCWD(t *testing.T) {
	// Cannot t.Parallel: uses t.Setenv.
	// Sessions in /tmp/other should NOT match a request for /tmp/proj-x.
	home := withFakeHome(t)
	makeCodexSession(t, home, "codex-other", "/tmp/other", time.Now())

	cands, err := ListSessionsForCWD("/tmp/proj-x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("got %d candidates, want 0 (sibling cwd must not match)", len(cands))
	}
}

func TestListSessionsForCWD_MergesClaudeAndCodexSortedByMtime(t *testing.T) {
	// Cannot t.Parallel: uses t.Setenv.
	home := withFakeHome(t)
	cwd := "/tmp/merged-proj"

	// Claude session, older mtime.
	claudeDir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	claudePath := filepath.Join(claudeDir, "claude-old.jsonl")
	if err := os.WriteFile(claudePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	older := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(claudePath, older, older); err != nil {
		t.Fatalf("chtimes claude: %v", err)
	}

	// Codex session, newer mtime.
	makeCodexSession(t, home, "codex-newer", cwd, time.Now())

	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d, want 2 (claude + codex)", len(cands))
	}
	if cands[0].CLI != connectors.CLICodex {
		t.Errorf("first candidate CLI = %q, want codex (newer mtime)", cands[0].CLI)
	}
	if cands[1].CLI != connectors.CLIClaude {
		t.Errorf("second candidate CLI = %q, want claude", cands[1].CLI)
	}
}

// TestCLIForPath asserts the dispatch helper returns the correct CLI
// for paths under each storage root, and CLI("") for unknown paths.
func TestCLIForPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want connectors.CLI
	}{
		{"claude", "/Users/x/.claude/projects/-tmp-foo/abc.jsonl", connectors.CLIClaude},
		{"codex", "/Users/x/.codex/sessions/2026/05/07/rollout-x.jsonl", connectors.CLICodex},
		{"unknown", "/tmp/random/file.jsonl", connectors.CLI("")},
		{"empty", "", connectors.CLI("")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CLIForPath(tc.path); got != tc.want {
				t.Errorf("CLIForPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
