package worklog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func newWriterTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "writer.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestWriteEntrySuppresses(t *testing.T) {
	db := newWriterTestDB(t)
	res, err := WriteEntry(context.Background(), db, Entry{
		SessionID: "s1", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
		WallTime: 30 * time.Second, ToolCallCount: 1,
	}, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatal(err)
	}
	if res.RecapVisible != 0 {
		t.Errorf("trivial must be invisible, got visible=%d", res.RecapVisible)
	}
	if res.SuppressedBy == "" {
		t.Errorf("must record reason")
	}
}

func TestWriteEntryCanonicalizesWorktreePath(t *testing.T) {
	// Worktrees of the same repo must share a project identity so the
	// worklog rollup shows one card per repo, not one per worktree.
	main, wt := newRepoWithWorktree(t)
	db := newWriterTestDB(t)
	_, err := WriteEntry(context.Background(), db, Entry{
		SessionID: "s-wt", TS: time.Now(), ProjectPath: wt, CLI: "claude",
		CommitSHA: "abc", WallTime: 5 * time.Minute, ToolCallCount: 30,
		EditWriteCount: 4, Files: []string{"src/auth.go"},
		EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
	}, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	var got string
	if err := db.Read().QueryRow(
		`SELECT project_path FROM stop_summaries WHERE session_id = ?`,
		"s-wt",
	).Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	wantAbs, _ := filepath.EvalSymlinks(main)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != wantAbs {
		t.Errorf("persisted project_path = %q, want canonical %q (worktree input %q must roll up to main repo)", gotAbs, wantAbs, wt)
	}
}

// newRepoWithWorktree creates a main git repo with one commit and a
// linked worktree, returning both absolute paths. Mirrors the helper
// used by internal/projectpath tests so worklog writers and recorders
// can be exercised with realistic worktree inputs.
func newRepoWithWorktree(t *testing.T) (mainRepo, worktree string) {
	t.Helper()
	main := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	wt := t.TempDir() + "/wt"
	cmd := exec.Command("git", "-C", main, "worktree", "add", wt)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	return main, wt
}

func TestWriteEntryPromotes(t *testing.T) {
	db := newWriterTestDB(t)
	res, err := WriteEntry(context.Background(), db, Entry{
		SessionID: "s2", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
		CommitSHA: "abc", WallTime: 5 * time.Minute, ToolCallCount: 30,
		EditWriteCount: 4, Files: []string{"src/auth.go"},
		EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
	}, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatal(err)
	}
	if res.RecapVisible != 1 {
		t.Errorf("substantive must be visible")
	}
	if res.Importance < 7 {
		t.Errorf("commit must score >= 7, got %d", res.Importance)
	}
	if res.Signature == "" {
		t.Errorf("signature must be set")
	}
}
