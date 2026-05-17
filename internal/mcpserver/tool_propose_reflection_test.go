package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHandleProposeReflection_ReturnsPendingEntries(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now().Add(-1*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "s2", true, 7, time.Now().Add(-30*time.Minute))
	// Suppressed entry must NOT appear in the proposal.
	seedStopSummary(t, db, "/p", "claude", "s3", false, 9, time.Now())

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 pending entries (visible only), got %d", len(out.Entries))
	}
	if out.ProjectPath != "/p" {
		t.Errorf("project_path lost: %q", out.ProjectPath)
	}
	// Reason should classify: 8 + 7 = 15, below default 150 → user-invoked
	// (unless the CI clock falls on Sunday evening, which we tolerate).
	if !strings.Contains(out.Markdown, "Reflection proposal for /p") {
		t.Errorf("markdown missing header: %s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "[claude]") || !strings.Contains(out.Markdown, "[codex]") {
		t.Errorf("markdown should tag both CLIs: %s", out.Markdown)
	}
}

func TestHandleProposeReflection_ImportanceSumReason(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	// Three entries scoring 60 each → sum 180 ≥ 150 → importance-sum.
	seedStopSummary(t, db, "/p", "claude", "e1", true, 60, time.Now().Add(-3*time.Hour))
	seedStopSummary(t, db, "/p", "claude", "e2", true, 60, time.Now().Add(-2*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "e3", true, 60, time.Now().Add(-1*time.Hour))

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !strings.HasPrefix(out.Reason, "importance-sum") {
		t.Errorf("expected importance-sum reason, got %q", out.Reason)
	}
	if !strings.Contains(out.Markdown, "importance-sum") {
		t.Errorf("markdown should surface trigger reason: %s", out.Markdown)
	}
}

func TestHandleProposeReflection_EmptyProject(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(out.Entries) != 0 {
		t.Errorf("expected 0 entries on empty project, got %d", len(out.Entries))
	}
	if !strings.Contains(out.Markdown, "Nothing to synthesize") {
		t.Errorf("markdown should explain empty case: %s", out.Markdown)
	}
}
