package productivity

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitCmd runs a git command in dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Deterministic identity + dates so commit timestamps are inside the
	// test window regardless of when the suite runs.
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=dev@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=dev@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFile writes a file, stages it, and commits with the given author
// email at the given commit date.
func commitFile(t *testing.T, dir, name, body, subject, email string, when time.Time) {
	t.Helper()
	if err := writeFile(filepath.Join(dir, name), body); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitCmd(t, dir, "add", name)
	ts := when.Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "-m", subject)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=A",
		"GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME=A",
		"GIT_COMMITTER_EMAIL="+email,
		"GIT_AUTHOR_DATE="+ts,
		"GIT_COMMITTER_DATE="+ts,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func writeFile(path, body string) error {
	return osWriteFile(path, []byte(body), 0o644)
}

func TestScanRepo_LocalCommitsNoRemote(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "feat/CLI-1396-pipeline")

	base := time.Now().Add(-2 * time.Hour)
	commitFile(t, dir, "a.go", "package a\n", "extractReportIdFromLink", "dev@example.com", base)
	commitFile(t, dir, "b.go", "package b\n", "processOneLabStackReport", "ravi@example.com", base.Add(10*time.Minute))

	since := base.Add(-time.Hour)
	until := time.Now()
	userEmails := map[string]bool{"dev@example.com": true}

	sr, err := ScanRepo(dir, since, until, userEmails)
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	if sr.Branch != "feat/CLI-1396-pipeline" {
		t.Errorf("Branch = %q; want feat/CLI-1396-pipeline", sr.Branch)
	}
	if sr.TicketID != "CLI-1396" {
		t.Errorf("TicketID = %q; want CLI-1396", sr.TicketID)
	}
	if sr.Ship != ShipLocal {
		t.Errorf("Ship = %q; want %q (no remote → committed-local-only)", sr.Ship, ShipLocal)
	}
	if len(sr.Commits) != 2 {
		t.Fatalf("len(Commits) = %d; want 2", len(sr.Commits))
	}

	bySubject := map[string]Commit{}
	for _, c := range sr.Commits {
		bySubject[c.Subject] = c
	}
	mine, ok := bySubject["extractReportIdFromLink"]
	if !ok {
		t.Fatalf("missing commit 'extractReportIdFromLink'; got %v", bySubject)
	}
	if !mine.IsUser {
		t.Errorf("own commit IsUser = false; want true")
	}
	if len(mine.SHA) < 7 {
		t.Errorf("SHA = %q; want a non-trivial sha", mine.SHA)
	}
	if mine.Files == 0 {
		t.Errorf("Files = 0; want > 0 (numstat parsed)")
	}
	other, ok := bySubject["processOneLabStackReport"]
	if !ok {
		t.Fatalf("missing co-actor commit")
	}
	if other.IsUser {
		t.Errorf("co-actor commit IsUser = true; want false (identity filter)")
	}
	if sr.Ahead < 1 {
		t.Errorf("Ahead = %d; want >= 1 (no upstream → all commits ahead)", sr.Ahead)
	}
}

// TestScanRepo_PushedWithoutLocalTracking covers the bug where a branch
// pushed to origin without -u (no local @{u} tracking ref) was treated
// as "no remote at all", reporting its entire history as unpushed.
func TestScanRepo_PushedWithoutLocalTracking(t *testing.T) {
	origin := t.TempDir()
	gitCmd(t, origin, "init", "-q", "--bare", "-b", "main")

	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	base := time.Now().Add(-2 * time.Hour)
	commitFile(t, dir, "main.go", "package main\n", "init", "dev@example.com", base)
	gitCmd(t, dir, "remote", "add", "origin", origin)
	gitCmd(t, dir, "push", "-q", "origin", "main")

	gitCmd(t, dir, "checkout", "-q", "-b", "feat/x")
	commitFile(t, dir, "f1.go", "package x\n", "feature commit one", "dev@example.com", base.Add(10*time.Minute))
	gitCmd(t, dir, "push", "-q", "origin", "feat/x") // no -u: origin/feat/x exists, no @{u}
	commitFile(t, dir, "f2.go", "package x\n", "feature commit two (local)", "dev@example.com", base.Add(20*time.Minute))

	sr, err := ScanRepo(dir, base.Add(-time.Hour), time.Now(), map[string]bool{"dev@example.com": true})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	if sr.Ship != ShipPushed {
		t.Errorf("Ship = %q; want %q (origin/feat/x exists despite no local tracking)", sr.Ship, ShipPushed)
	}
	if sr.Ahead != 1 {
		t.Errorf("Ahead = %d; want 1 (one commit beyond origin/feat/x, not whole history)", sr.Ahead)
	}
}

func TestScanRepo_WindowExcludesOldCommits(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")

	old := time.Now().Add(-72 * time.Hour)
	recent := time.Now().Add(-30 * time.Minute)
	commitFile(t, dir, "old.go", "x", "ancient work", "dev@example.com", old)
	commitFile(t, dir, "new.go", "y", "today work", "dev@example.com", recent)

	since := time.Now().Add(-2 * time.Hour)
	sr, err := ScanRepo(dir, since, time.Now(), map[string]bool{"dev@example.com": true})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	if len(sr.Commits) != 1 || sr.Commits[0].Subject != "today work" {
		t.Fatalf("window filter wrong: %+v", sr.Commits)
	}
}
