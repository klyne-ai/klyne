package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// gitInRepo runs a git command in dir and fails the test on error. Uses a
// deterministic identity so the test is hermetic.
func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// countGitSnapshots returns the number of git_session_snapshots rows for
// a session_id by querying the table directly.
func countGitSnapshots(t *testing.T, db *store.DB, sessionID string) int {
	t.Helper()
	var n int
	err := db.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM git_session_snapshots WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

// stageSessionEndTranscript writes a minimal Claude Code JSONL whose cwd
// is projectDir and returns the Stop-event payload bytes.
func stageSessionEndTranscript(t *testing.T, tmp, sessionID, projectDir string) string {
	t.Helper()
	encoded := filepath.Join(tmp, ".claude", "projects", "encoded")
	if err := os.MkdirAll(encoded, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcriptPath := filepath.Join(encoded, sessionID+".jsonl")
	body := strings.Join([]string{
		`{"sessionId":"` + sessionID + `","type":"user","timestamp":"2026-05-15T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"do the work"}]},"cwd":"` + projectDir + `","uuid":"u1"}`,
		`{"sessionId":"` + sessionID + `","type":"assistant","timestamp":"2026-05-15T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go build ./..."}}]},"cwd":"` + projectDir + `","uuid":"u2","parentUuid":"u1"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcriptPath,
		"stop_hook_active": true,
		"cwd":              projectDir,
	})
	return string(payload)
}

// TestSessionEndHook_WritesGitSnapshot asserts that when a session ends
// in a git repo, the hook records a git_session_snapshots row for it
// (spec D6 snapshot capture).
func TestSessionEndHook_WritesGitSnapshot(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".klyne"), 0o755); err != nil {
		t.Fatalf("mkdir .klyne: %v", err)
	}
	prevHome := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = prevHome })

	// A real git repo as the session's project_path, with a dirty file.
	projectDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitInRepo(t, projectDir, "init", "-q", "-b", "feat/CLI-1396-pipeline")
	if err := os.WriteFile(filepath.Join(projectDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	gitInRepo(t, projectDir, "add", "a.go")
	gitInRepo(t, projectDir, "commit", "-qm", "first")
	// Leave an uncommitted file so dirty_file_count is exercised.
	if err := os.WriteFile(filepath.Join(projectDir, "dirty.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write dirty.go: %v", err)
	}

	payload := stageSessionEndTranscript(t, tmp, "sess-snap-1", projectDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := computeAndPersistSessionEnd(ctx, strings.NewReader(payload)); err != nil {
		t.Fatalf("hook body: %v", err)
	}

	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if n := countGitSnapshots(t, db, "sess-snap-1"); n < 1 {
		t.Fatalf("expected >= 1 git_session_snapshots row for sess-snap-1, got %d", n)
	}

	var (
		repoName, branch, dirtyJSON string
		dirtyCount                  int
	)
	err = db.Read().QueryRowContext(ctx, `
SELECT repo_name, branch, dirty_file_count, dirty_files_json
  FROM git_session_snapshots WHERE session_id = ? LIMIT 1`, "sess-snap-1").
		Scan(&repoName, &branch, &dirtyCount, &dirtyJSON)
	if err != nil {
		t.Fatalf("read snapshot row: %v", err)
	}
	if repoName != "repo" {
		t.Errorf("repo_name = %q, want repo", repoName)
	}
	if branch != "feat/CLI-1396-pipeline" {
		t.Errorf("branch = %q, want feat/CLI-1396-pipeline", branch)
	}
	if dirtyCount != 1 {
		t.Errorf("dirty_file_count = %d, want 1 (one uncommitted file)", dirtyCount)
	}
}

// TestSessionEndHook_NonGitProjectNoSnapshotNoError asserts the hook does
// not fail and writes no snapshot rows when the session's project_path is
// not a git repo — capture is best-effort and non-fatal.
func TestSessionEndHook_NonGitProjectNoSnapshotNoError(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".klyne"), 0o755); err != nil {
		t.Fatalf("mkdir .klyne: %v", err)
	}
	prevHome := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = prevHome })

	// A plain (non-git) directory as the project_path — simulates a git
	// failure path for snapshot capture.
	projectDir := filepath.Join(tmp, "plain")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	payload := stageSessionEndTranscript(t, tmp, "sess-snap-2", projectDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The hook must NOT fail even though snapshot capture finds no repo.
	if err := computeAndPersistSessionEnd(ctx, strings.NewReader(payload)); err != nil {
		t.Fatalf("hook body must not fail on a non-git project: %v", err)
	}

	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Session-end's core work (the stop_summary) still ran.
	got, err := store.LatestStopSummaryForProject(ctx, db, projectDir)
	if err != nil {
		t.Fatalf("latest stop summary: %v", err)
	}
	if got == nil {
		t.Fatal("expected the stop_summary row to still be written")
	}
	// But no git snapshot rows, since the project is not a git repo.
	if n := countGitSnapshots(t, db, "sess-snap-2"); n != 0 {
		t.Errorf("expected 0 git snapshot rows for a non-git project, got %d", n)
	}
}
