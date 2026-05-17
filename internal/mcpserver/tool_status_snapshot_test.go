package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestHandleStatusSnapshot_RendersMarkdown(t *testing.T) {
	db, _ := runbookTestDB(t)
	seedRunbookFixture(t, db, "/proj/demo-status")

	ctx := context.Background()
	_, out, err := HandleStatusSnapshot(ctx, nil, StatusSnapshotInput{
		ProjectPath: "/proj/demo-status",
	})
	if err != nil {
		t.Fatalf("HandleStatusSnapshot: %v", err)
	}
	if out.Markdown == "" {
		t.Fatalf("expected non-empty markdown body")
	}
	if !strings.Contains(out.Markdown, "# klyne status") {
		t.Errorf("markdown missing canonical header:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "/proj/demo-status") {
		t.Errorf("markdown missing project path scope:\n%s", out.Markdown)
	}
	if out.Snapshot.TotalSessions == 0 {
		t.Errorf("expected non-zero session count in snapshot")
	}
}

func TestHandleStatusSnapshot_AllProjects(t *testing.T) {
	db, _ := runbookTestDB(t)
	seedRunbookFixture(t, db, "/proj/a")
	seedRunbookFixture(t, db, "/proj/b")

	ctx := context.Background()
	_, out, err := HandleStatusSnapshot(ctx, nil, StatusSnapshotInput{
		AllProjects: true,
	})
	if err != nil {
		t.Fatalf("HandleStatusSnapshot: %v", err)
	}
	if out.Snapshot.ProjectPath != "" {
		t.Errorf("all-projects mode should leave project_path empty, got %q",
			out.Snapshot.ProjectPath)
	}
	if len(out.Snapshot.TopProjects) < 2 {
		t.Errorf("expected at least 2 projects in top-projects, got %d",
			len(out.Snapshot.TopProjects))
	}
}
