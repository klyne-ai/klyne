package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodeCWD(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"absolute simple", "/Users/x/proj", "-Users-x-proj"},
		{"absolute deep", "/Users/x/proj/subdir/inner", "-Users-x-proj-subdir-inner"},
		{"empty", "", ""},
		{"relative", "proj/subdir", ""},
		{"root", "/", "-"},
		// Regression: Claude Code replaces both '/' AND '.' with '-',
		// producing '--' wherever a path contained '/.'. The git
		// worktree case (.claude/worktrees/...) is the load-bearing
		// example — without folding '.' the resolver looks for a
		// directory that doesn't exist and silently walks up to the
		// parent project, returning unrelated sessions.
		{"worktree dotfile", "/Users/x/proj/.claude/worktrees/foo", "-Users-x-proj--claude-worktrees-foo"},
		{"trailing dotted file", "/Users/x/proj/foo.tsx", "-Users-x-proj-foo-tsx"},
		{"multiple dots", "/Users/x/proj/.a/.b/c", "-Users-x-proj--a--b-c"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := EncodeCWD(tc.in); got != tc.want {
				t.Errorf("EncodeCWD(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// withFakeHome redirects HOME for the duration of one test so the
// resolver looks under a temp directory's projects/ subtree instead
// of the real ~/.claude/projects.
func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// makeProjectDir creates a fake encoded-CWD project directory plus N
// .jsonl files spaced one second apart in mtime so tests can assert
// "latest by mtime" deterministically.
func makeProjectDir(t *testing.T, home, encodedName string, fileNames ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", encodedName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i, name := range fileNames {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		// Stagger mtimes so the LAST entry in fileNames is the newest.
		mtime := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
}

func TestLatestSessionForCWD_ExactMatch(t *testing.T) {
	// Cannot t.Parallel: withFakeHome uses t.Setenv.
	home := withFakeHome(t)
	cwd := "/tmp/proj-a"
	makeProjectDir(t, home, EncodeCWD(cwd), "older.jsonl", "newer.jsonl")

	got, err := LatestSessionForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if filepath.Base(got) != "newer.jsonl" {
		t.Errorf("got = %q, want newer.jsonl (latest by mtime)", got)
	}
}

func TestLatestSessionForCWD_FallsBackToAncestor(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	// Project dir exists for /tmp/proj-a, AI subprocess is launched
	// from /tmp/proj-a/subdir/inner. The resolver must walk up.
	home := withFakeHome(t)
	makeProjectDir(t, home, EncodeCWD("/tmp/proj-a"), "primary.jsonl")

	got, err := LatestSessionForCWD("/tmp/proj-a/subdir/inner")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if filepath.Base(got) != "primary.jsonl" {
		t.Errorf("got = %q, want primary.jsonl from ancestor project dir", got)
	}
}

func TestLatestSessionForCWD_NoMatch(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	withFakeHome(t)
	got, err := LatestSessionForCWD("/tmp/never-existed")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "" {
		t.Errorf("got = %q, want empty (no project dir)", got)
	}
}

func TestLatestSessionForCWD_EmptyDirReturnsEmpty(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	home := withFakeHome(t)
	// Project dir exists but contains no .jsonl files.
	makeProjectDir(t, home, EncodeCWD("/tmp/proj-empty"))
	got, err := LatestSessionForCWD("/tmp/proj-empty")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "" {
		t.Errorf("got = %q, want empty (no jsonl files in dir)", got)
	}
}

func TestLatestSessionForCWD_PrefersNearestAncestor(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	// Both /tmp/parent and /tmp/parent/child have project dirs.
	// CWD inside /tmp/parent/child must resolve to the child's dir,
	// not the parent's.
	home := withFakeHome(t)
	makeProjectDir(t, home, EncodeCWD("/tmp/parent"), "parent-session.jsonl")
	makeProjectDir(t, home, EncodeCWD("/tmp/parent/child"), "child-session.jsonl")

	got, err := LatestSessionForCWD("/tmp/parent/child/deep")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if filepath.Base(got) != "child-session.jsonl" {
		t.Errorf("got = %q, want child-session.jsonl (nearest ancestor wins)", got)
	}
}

func TestListSessionsForCWD_MultipleSessionsSortedByRecency(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	home := withFakeHome(t)
	cwd := "/tmp/multi-proj"
	makeProjectDir(t, home, EncodeCWD(cwd), "older.jsonl", "middle.jsonl", "newest.jsonl")

	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("got %d candidates, want 3", len(cands))
	}
	if filepath.Base(cands[0].Path) != "newest.jsonl" {
		t.Errorf("cands[0] = %s, want newest.jsonl first", filepath.Base(cands[0].Path))
	}
	if filepath.Base(cands[2].Path) != "older.jsonl" {
		t.Errorf("cands[2] = %s, want older.jsonl last", filepath.Base(cands[2].Path))
	}
}

func TestListSessionsForCWD_PreviewFromUserMessage(t *testing.T) {
// Cannot t.Parallel: uses t.Setenv.
	home := withFakeHome(t)
	cwd := "/tmp/preview-proj"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Real user-message shape verified against ~/.claude/projects:
	// `message` is a JSON-encoded string of {role, content}.
	body := `{"type":"user","sessionId":"sess-preview","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the auth middleware bug"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "preview.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d, want 1", len(cands))
	}
	if cands[0].SessionID != "sess-preview" {
		t.Errorf("SessionID = %q, want sess-preview", cands[0].SessionID)
	}
	if cands[0].Preview != "fix the auth middleware bug" {
		t.Errorf("Preview = %q, want %q", cands[0].Preview, "fix the auth middleware bug")
	}
	if cands[0].MsgCount != 1 {
		t.Errorf("MsgCount = %d, want 1", cands[0].MsgCount)
	}
}

func TestPickActiveSession_SingleCandidate(t *testing.T) {
	t.Parallel()
	cands := []SessionCandidate{{SessionID: "only-one", IsActive: false}}
	got, ok := PickActiveSession(cands)
	if !ok {
		t.Errorf("ok = false, want true (one candidate is unambiguous)")
	}
	if got.SessionID != "only-one" {
		t.Errorf("got = %q, want only-one", got.SessionID)
	}
}

func TestPickActiveSession_MultipleWithOneActive(t *testing.T) {
	t.Parallel()
	cands := []SessionCandidate{
		{SessionID: "active", IsActive: true},
		{SessionID: "idle-1", IsActive: false},
		{SessionID: "idle-2", IsActive: false},
	}
	got, ok := PickActiveSession(cands)
	if !ok {
		t.Errorf("ok = false, want true (single active wins)")
	}
	if got.SessionID != "active" {
		t.Errorf("got = %q, want active", got.SessionID)
	}
}

func TestPickActiveSession_AmbiguousMultipleActive(t *testing.T) {
	t.Parallel()
	cands := []SessionCandidate{
		{SessionID: "a", IsActive: true},
		{SessionID: "b", IsActive: true},
		{SessionID: "c", IsActive: false},
	}
	_, ok := PickActiveSession(cands)
	if ok {
		t.Errorf("ok = true, want false (two active candidates is ambiguous)")
	}
}

func TestPickActiveSession_AmbiguousAllIdle(t *testing.T) {
	t.Parallel()
	cands := []SessionCandidate{
		{SessionID: "a", IsActive: false},
		{SessionID: "b", IsActive: false},
	}
	_, ok := PickActiveSession(cands)
	if ok {
		t.Errorf("ok = true, want false (no active candidates is ambiguous)")
	}
}

func TestPickActiveSession_Empty(t *testing.T) {
	t.Parallel()
	_, ok := PickActiveSession(nil)
	if ok {
		t.Errorf("ok = true, want false (empty list)")
	}
}
