package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

func TestHandleRecordReflection_Persists(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Insights: []worklog.Insight{
			{Text: "shipped auth refactor", Evidence: []string{"s1", "s2"}},
			{Text: "improved test coverage", Evidence: []string{"s3"}},
		},
	}
	out, err := handleRecordReflection(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if out.ReflectionID == "" {
		t.Errorf("expected non-empty reflection id")
	}
	if out.EvidenceCount != 3 {
		t.Errorf("expected 3 evidence entries, got %d", out.EvidenceCount)
	}

	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 persisted reflection, got %d", len(rows))
	}
}

func TestHandleRecordReflection_RejectsEmptyEvidence(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Insights: []worklog.Insight{
			{Text: "vague claim", Evidence: nil},
		},
	}
	_, err := handleRecordReflection(context.Background(), db, in)
	if err == nil {
		t.Errorf("expected citation-invariant error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "citation invariant") {
		t.Errorf("expected error mentioning citation invariant, got %v", err)
	}
}

func TestHandleRecordReflection_RejectsEmptyInsights(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	_, err := handleRecordReflection(context.Background(), db, RecordReflectionInput{ProjectPath: "/p"})
	if err == nil {
		t.Errorf("expected error on empty insights, got nil")
	}
}

func TestHandleRecordReflection_PersistsWithDay(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "2026-05-15",
		Insights: []worklog.Insight{
			{Text: "did the auth thing", Evidence: []string{"s1"}},
		},
	}
	out, err := handleRecordReflection(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if out.ReflectionID == "" {
		t.Errorf("expected non-empty reflection id")
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Title != "Daily reflection — 2026-05-15" {
		t.Errorf("title=%q, want Daily reflection — 2026-05-15", rows[0].Title)
	}
}

// TestHandleRecordReflection_EnrichesWithGitSubstrate proves the
// spec §7.2 Layer-2 wiring end-to-end: record_reflection over a real git
// repo embeds the deterministic git-grounded sections (Shipped ledger,
// commit-dated, ticket id) into body_md.
func TestHandleRecordReflection_EnrichesWithGitSubstrate(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	when := time.Now().Add(-2 * time.Hour)
	repo := t.TempDir()
	gitEnv := func(ts time.Time, email string) []string {
		return append(os.Environ(),
			"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL="+email,
			"GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL="+email,
			"GIT_AUTHOR_DATE="+ts.Format(time.RFC3339),
			"GIT_COMMITTER_DATE="+ts.Format(time.RFC3339),
		)
	}
	run := func(ts time.Time, email string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = gitEnv(ts, email)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(when, "u@u", "init", "-q", "-b", "feat/CLI-1396-pipeline")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Identity seed includes coders@clinikk.com (the env identity).
	run(when, "coders@clinikk.com", "add", "a.go")
	run(when, "coders@clinikk.com", "commit", "-m", "wire pipeline entrypoint")

	in := RecordReflectionInput{
		ProjectPath: repo,
		Day:         when.UTC().Format("2006-01-02"),
		Insights: []worklog.Insight{
			{Text: "built the labstack pipeline", Evidence: []string{"s1"}},
		},
	}
	out, err := handleRecordReflection(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, repo, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 reflection (id %s), got %d", out.ReflectionID, len(rows))
	}
	body := rows[0].BodyMD
	if !strings.Contains(body, "labstack pipeline") {
		t.Errorf("AI insight lost from enriched body:\n%s", body)
	}
	if !strings.Contains(body, "Shipped") {
		t.Errorf("git substrate Shipped ledger missing from body:\n%s", body)
	}
	if !strings.Contains(body, "CLI-1396") {
		t.Errorf("ticket id missing from enriched body:\n%s", body)
	}
}

func TestHandleRecordReflection_RejectsBadDay(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "May 15",
		Insights: []worklog.Insight{
			{Text: "x", Evidence: []string{"e"}},
		},
	}
	_, err := handleRecordReflection(context.Background(), db, in)
	if err == nil {
		t.Fatalf("expected error on malformed day")
	}
	if !strings.Contains(err.Error(), "bad day") {
		t.Errorf("expected error mentioning 'bad day', got %v", err)
	}
}
