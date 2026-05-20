package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GitSnapshot is one row of the git_session_snapshots table (migration
// 017). It is a point-in-time record of a repo working tree at the moment
// a Claude/Codex session ended — branch, HEAD, ahead/behind vs upstream,
// and the dirty (uncommitted+untracked) file set.
//
// This row is essential and unreconstructable: it is the only way to ever
// know "AI task done but uncommitted at time T" historically (spec D4/D6).
// Live git scans capture the *current* tree state but cannot reproduce a
// past one. The session-end hook is the authoritative writer.
type GitSnapshot struct {
	ID             int64     `json:"id"`
	SessionID      string    `json:"session_id,omitempty"`
	ProjectPath    string    `json:"project_path"`
	RepoName       string    `json:"repo_name"`
	WorktreePath   string    `json:"worktree_path,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	HeadSHA        string    `json:"head_sha,omitempty"`
	AheadCount     int       `json:"ahead_count"`
	BehindCount    int       `json:"behind_count"`
	DirtyFileCount int       `json:"dirty_file_count"`
	DirtyFiles     []string  `json:"dirty_files"`
	CapturedAt     time.Time `json:"captured_at"`
}

// InsertGitSnapshot writes a new git_session_snapshots row and sets s.ID
// to the auto-assigned row id. CapturedAt defaults to time.Now() when
// zero; DirtyFiles nil is stored as an empty JSON array (the column is
// NOT NULL DEFAULT '[]').
func InsertGitSnapshot(ctx context.Context, db *DB, s *GitSnapshot) error {
	if strings.TrimSpace(s.ProjectPath) == "" {
		return fmt.Errorf("store: git snapshot project_path required")
	}
	if strings.TrimSpace(s.RepoName) == "" {
		return fmt.Errorf("store: git snapshot repo_name required")
	}
	if s.CapturedAt.IsZero() {
		s.CapturedAt = time.Now()
	}
	files := s.DirtyFiles
	if files == nil {
		files = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("store: marshal dirty files: %w", err)
	}
	const q = `
INSERT INTO git_session_snapshots
    (session_id, project_path, repo_name, worktree_path, branch, head_sha,
     ahead_count, behind_count, dirty_file_count, dirty_files_json, captured_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := db.Write().ExecContext(ctx, q,
		s.SessionID, s.ProjectPath, s.RepoName, s.WorktreePath, s.Branch, s.HeadSHA,
		s.AheadCount, s.BehindCount, s.DirtyFileCount, string(filesJSON), s.CapturedAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert git_session_snapshot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: git_session_snapshot last insert id: %w", err)
	}
	s.ID = id
	return nil
}
