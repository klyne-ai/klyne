package worklog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// gitRun runs a git command in dir with a deterministic identity/date so
// commit timestamps land inside the test window.
func gitRun(t *testing.T, dir string, when time.Time, email string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	ts := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL="+email,
		"GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSubstrateRepo builds a git repo with two user commits and one
// bot/co-actor commit on a ticket branch, all dated `when`.
func newSubstrateRepo(t *testing.T, when time.Time) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, when, "u@u", "init", "-q", "-b", "feat/CLI-1396-pipeline")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userEmail := "dev@example.com"
	// Pin the §6.3 identity to the fixture author so the test is hermetic
	// and does not depend on the machine's global `git config user.email`.
	productivity.SetUserEmailAliases([]string{userEmail})
	t.Cleanup(func() { productivity.SetUserEmailAliases(nil) })
	write("a.go", "package a\n")
	gitRun(t, dir, when, userEmail, "add", "a.go")
	gitRun(t, dir, when, userEmail, "commit", "-m", "wire pipeline entrypoint")

	write("b.go", "package b\n")
	gitRun(t, dir, when.Add(10*time.Minute), userEmail, "add", "b.go")
	gitRun(t, dir, when.Add(10*time.Minute), userEmail, "commit", "-m", "extractReportIdFromLink")

	// Co-actor / bot commit — must be excluded by the identity filter.
	write("c.go", "package c\n")
	gitRun(t, dir, when.Add(20*time.Minute), "jenkins@ci", "add", "c.go")
	gitRun(t, dir, when.Add(20*time.Minute), "jenkins@ci", "commit", "-m", "Jenkins: bump build")
	return dir
}

// BuildProjectSubstrate must scan a real repo and produce a Report whose
// single Service carries only the user's commits (improvement 6) with
// the ticket id parsed from the branch name.
func TestBuildProjectSubstrate_ScansRepoIdentityFiltered(t *testing.T) {
	when := time.Now().Add(-2 * time.Hour)
	dir := newSubstrateRepo(t, when)

	rep, err := BuildProjectSubstrate(dir, when.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("BuildProjectSubstrate: %v", err)
	}
	if len(rep.Services) != 1 {
		t.Fatalf("expected 1 Service, got %d", len(rep.Services))
	}
	svc := rep.Services[0]
	if len(svc.Branches) != 1 {
		t.Fatalf("expected 1 Branch, got %d", len(svc.Branches))
	}
	b := svc.Branches[0]
	// Identity filter (improvement 6): the Jenkins commit must be gone.
	if len(b.Commits) != 2 {
		t.Errorf("expected 2 user commits (Jenkins excluded), got %d: %+v", len(b.Commits), b.Commits)
	}
	for _, c := range b.Commits {
		if strings.Contains(c.Subject, "Jenkins") {
			t.Errorf("bot commit leaked into substrate: %+v", c)
		}
	}
	if b.TicketID != "CLI-1396" {
		t.Errorf("TicketID = %q; want CLI-1396", b.TicketID)
	}
}

// Graceful degradation (D8): a non-git directory yields an empty report,
// not an error.
func TestBuildProjectSubstrate_NonGitDirIsEmpty(t *testing.T) {
	rep, err := BuildProjectSubstrate(t.TempDir(), time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("non-git dir must not error: %v", err)
	}
	if len(rep.Services) != 0 {
		t.Errorf("non-git dir must yield no Services, got %d", len(rep.Services))
	}
}

// End-to-end: the substrate from a real repo, fed to GitSubstrateSections
// (via the canonical project path), produces the open-loops + shipped
// sections — proving the full improvement chain wires together.
func TestBuildProjectSubstrate_FeedsGitSubstrateSections(t *testing.T) {
	when := time.Now().Add(-2 * time.Hour)
	dir := newSubstrateRepo(t, when)
	rep, err := BuildProjectSubstrate(dir, when.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("BuildProjectSubstrate: %v", err)
	}
	pp := rep.Services[0].ProjectPath
	secs := GitSubstrateSections(rep, pp)
	md := strings.Join(secs, "\n")
	if !strings.Contains(md, "Shipped") {
		t.Errorf("expected Shipped ledger from real-repo substrate, got:\n%s", md)
	}
	if !strings.Contains(md, "CLI-1396") {
		t.Errorf("expected ticket id in sections, got:\n%s", md)
	}
}
