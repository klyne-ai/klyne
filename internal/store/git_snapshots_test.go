package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func openGitSnapshotsDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "snap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertGitSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openGitSnapshotsDB(t)

	captured := time.Date(2026, 5, 19, 11, 30, 0, 0, time.UTC)
	s := &store.GitSnapshot{
		SessionID:      "sess-snap",
		ProjectPath:    "/repos/consultation-service",
		RepoName:       "consultation-service",
		WorktreePath:   "/repos/consultation-service",
		Branch:         "feat/CLI-1396-pipeline",
		HeadSHA:        "abc123def456",
		AheadCount:     9,
		BehindCount:    0,
		DirtyFileCount: 2,
		DirtyFiles:     []string{"a.go", "b.go"},
		CapturedAt:     captured,
	}
	if err := store.InsertGitSnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	// Read the row straight back via SQL to verify every column.
	var (
		gotSession, gotProject, gotRepo, gotWorktree, gotBranch, gotHead string
		gotAhead, gotBehind, gotDirty                                    int
		gotDirtyJSON                                                     string
		gotCapturedAt                                                    time.Time
	)
	row := db.Read().QueryRowContext(ctx, `
SELECT session_id, project_path, repo_name, worktree_path, branch, head_sha,
       ahead_count, behind_count, dirty_file_count, dirty_files_json, captured_at
  FROM git_session_snapshots WHERE id = ?`, s.ID)
	if err := row.Scan(&gotSession, &gotProject, &gotRepo, &gotWorktree, &gotBranch,
		&gotHead, &gotAhead, &gotBehind, &gotDirty, &gotDirtyJSON, &gotCapturedAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotSession != "sess-snap" {
		t.Errorf("session_id = %q, want sess-snap", gotSession)
	}
	if gotProject != "/repos/consultation-service" {
		t.Errorf("project_path = %q", gotProject)
	}
	if gotRepo != "consultation-service" {
		t.Errorf("repo_name = %q", gotRepo)
	}
	if gotBranch != "feat/CLI-1396-pipeline" {
		t.Errorf("branch = %q", gotBranch)
	}
	if gotHead != "abc123def456" {
		t.Errorf("head_sha = %q", gotHead)
	}
	if gotAhead != 9 || gotBehind != 0 {
		t.Errorf("ahead/behind = %d/%d, want 9/0", gotAhead, gotBehind)
	}
	if gotDirty != 2 {
		t.Errorf("dirty_file_count = %d, want 2", gotDirty)
	}
	var files []string
	if err := json.Unmarshal([]byte(gotDirtyJSON), &files); err != nil {
		t.Fatalf("dirty_files_json not valid JSON: %v (%q)", err, gotDirtyJSON)
	}
	if len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
		t.Errorf("dirty_files_json = %v, want [a.go b.go]", files)
	}
	if !gotCapturedAt.Equal(captured) {
		t.Errorf("captured_at = %v, want %v", gotCapturedAt, captured)
	}
}

func TestInsertGitSnapshot_RequiresProjectPath(t *testing.T) {
	ctx := context.Background()
	db := openGitSnapshotsDB(t)

	err := store.InsertGitSnapshot(ctx, db, &store.GitSnapshot{RepoName: "x", CapturedAt: time.Now()})
	if err == nil {
		t.Fatal("expected error for missing project_path")
	}
}

func TestInsertGitSnapshot_NilDirtyFilesBecomesEmptyJSON(t *testing.T) {
	ctx := context.Background()
	db := openGitSnapshotsDB(t)

	s := &store.GitSnapshot{
		ProjectPath: "/repos/oms-service",
		RepoName:    "oms-service",
		DirtyFiles:  nil,
	}
	if err := store.InsertGitSnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var dirtyJSON string
	if err := db.Read().QueryRowContext(ctx,
		`SELECT dirty_files_json FROM git_session_snapshots WHERE id = ?`, s.ID).
		Scan(&dirtyJSON); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if dirtyJSON != "[]" {
		t.Errorf("dirty_files_json = %q, want []", dirtyJSON)
	}
}

func TestInsertGitSnapshot_DefaultsCapturedAt(t *testing.T) {
	ctx := context.Background()
	db := openGitSnapshotsDB(t)

	before := time.Now().Add(-time.Second)
	s := &store.GitSnapshot{ProjectPath: "/repos/p", RepoName: "p"}
	if err := store.InsertGitSnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if s.CapturedAt.Before(before) {
		t.Errorf("captured_at not defaulted to now: %v", s.CapturedAt)
	}
}
