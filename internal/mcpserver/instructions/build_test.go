package instructions

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// openTempDB opens a fresh store DB in a tempdir, runs migrations,
// and returns the handle. Cleaned up via t.Cleanup.
func openTempDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "klyne.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed inserts one decision row with the given project_path and
// returns the id so tests can assert ordering / membership.
func seed(t *testing.T, db *store.DB, id, projectPath, text string) {
	t.Helper()
	d := &store.Decision{
		ID:          id,
		Ts:          time.Now().UnixMilli(),
		ProjectPath: projectPath,
		Text:        text,
	}
	if err := store.InsertDecision(context.Background(), db, d); err != nil {
		t.Fatalf("InsertDecision %s: %v", id, err)
	}
	// 1ms spacing so ts DESC ordering is deterministic across rows.
	time.Sleep(time.Millisecond)
}

func TestBuild_EmptyDB(t *testing.T) {
	db := openTempDB(t)
	got := Build(context.Background(), "/some/project", db)
	if got != "" {
		t.Errorf("expected empty string for empty DB, got:\n%s", got)
	}
}

func TestBuild_ProjectAndGlobal(t *testing.T) {
	db := openTempDB(t)
	cwd := "/repo/svc-a"

	// Two project + two global runbooks, varied bodies.
	seed(t, db, "d-p1", cwd, "rotate api keys monthly")
	seed(t, db, "d-p2", cwd, "for labstack changes we have four working dirs\nstep 1\nstep 2")
	seed(t, db, "d-g1", "", "RUNBOOK: rotate vendor api key via ops vault")
	seed(t, db, "d-g2", "", "globally true: always confirm before destructive shell")

	// Decoy: a runbook from a DIFFERENT project must not leak in.
	seed(t, db, "d-other", "/repo/svc-b", "irrelevant to svc-a")

	got := Build(context.Background(), cwd, db)
	if got == "" {
		t.Fatal("expected non-empty instructions, got empty string")
	}

	// Directive present.
	if !strings.Contains(got, "klyne tracks runbooks for this project") {
		t.Errorf("missing directive header; got:\n%s", got)
	}

	// Both section headings present, scoped to cwd.
	if !strings.Contains(got, "# Project runbooks (path: /repo/svc-a)") {
		t.Errorf("missing project heading; got:\n%s", got)
	}
	if !strings.Contains(got, "# Global runbooks") {
		t.Errorf("missing global heading; got:\n%s", got)
	}

	// Each runbook's id + first-line title is present.
	for _, want := range []string{
		"d-p1  rotate api keys monthly",
		"d-p2  for labstack changes we have four working dirs",
		"d-g1  RUNBOOK: rotate vendor api key via ops vault",
		"d-g2  globally true: always confirm before destructive shell",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing row %q; got:\n%s", want, got)
		}
	}

	// Decoy from a different project must NOT appear.
	if strings.Contains(got, "d-other") || strings.Contains(got, "irrelevant to svc-a") {
		t.Errorf("decoy row from /repo/svc-b leaked into project section; got:\n%s", got)
	}

	// No footer when under cap.
	if strings.Contains(got, "more, call mcp__klyne__recall") {
		t.Errorf("unexpected overflow footer; got:\n%s", got)
	}
}

func TestBuild_OverflowProjectFooter(t *testing.T) {
	db := openTempDB(t)
	cwd := "/repo/svc-big"

	// 25 project runbooks → expect 20 shown + footer "+5 more".
	for i := 0; i < 25; i++ {
		seed(t, db, fmt.Sprintf("d-p%02d", i), cwd, fmt.Sprintf("project runbook %02d", i))
	}
	// 5 globals → expect 5 shown, no footer.
	for i := 0; i < 5; i++ {
		seed(t, db, fmt.Sprintf("d-g%02d", i), "", fmt.Sprintf("global runbook %02d", i))
	}

	got := Build(context.Background(), cwd, db)

	// Project footer present, global footer absent.
	if !strings.Contains(got, "+5 more, call mcp__klyne__recall to see all") {
		t.Errorf("missing project overflow footer; got:\n%s", got)
	}

	// Newest-first: seed inserts ts in increasing order, so d-p24 is
	// newest. The first 20 shown must be d-p24 down to d-p05.
	if !strings.Contains(got, "d-p24") {
		t.Errorf("newest project row d-p24 missing; got:\n%s", got)
	}
	if strings.Contains(got, "d-p04") {
		t.Errorf("oldest project row d-p04 should have been trimmed; got:\n%s", got)
	}

	// Count footer phrase appearances — must be exactly 1.
	if n := strings.Count(got, "more, call mcp__klyne__recall"); n != 1 {
		t.Errorf("expected exactly 1 footer line, found %d; got:\n%s", n, got)
	}
}

func TestBuild_EmptyCwd_GlobalsOnly(t *testing.T) {
	db := openTempDB(t)

	seed(t, db, "d-g1", "", "globally true: always confirm")
	seed(t, db, "d-other", "/some/project", "should not appear")

	got := Build(context.Background(), "", db)

	if !strings.Contains(got, "d-g1") {
		t.Errorf("global row missing under empty cwd; got:\n%s", got)
	}
	if strings.Contains(got, "# Project runbooks") {
		t.Errorf("project section should be omitted under empty cwd; got:\n%s", got)
	}
	if strings.Contains(got, "d-other") {
		t.Errorf("project-scoped row leaked under empty cwd; got:\n%s", got)
	}
}

func TestBuild_ProjectOnly_NoGlobalSection(t *testing.T) {
	db := openTempDB(t)
	cwd := "/repo/svc-c"

	seed(t, db, "d-p1", cwd, "project-only runbook")

	got := Build(context.Background(), cwd, db)
	if got == "" {
		t.Fatal("expected non-empty instructions, got empty string")
	}

	if !strings.Contains(got, "# Project runbooks (path: /repo/svc-c)") {
		t.Errorf("missing project heading; got:\n%s", got)
	}
	if !strings.Contains(got, "d-p1  project-only runbook") {
		t.Errorf("missing project row; got:\n%s", got)
	}
	if strings.Contains(got, "# Global runbooks") {
		t.Errorf("global section should be omitted when no globals exist; got:\n%s", got)
	}
}
