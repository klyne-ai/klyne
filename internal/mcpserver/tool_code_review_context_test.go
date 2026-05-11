package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleCodeReviewContext_Absent(t *testing.T) {
	root := t.TempDir()
	_, out, err := HandleCodeReviewContext(context.Background(), nil, CodeReviewContextInput{ProjectRoot: root})
	if err != nil {
		t.Fatalf("HandleCodeReviewContext: %v", err)
	}
	if out.Detected {
		t.Errorf("Detected = true, want false")
	}
	if out.ProjectRoot != root {
		t.Errorf("ProjectRoot = %q, want %q", out.ProjectRoot, root)
	}
	if len(out.HighRiskFiles) != 0 || len(out.RecentBlockers) != 0 || len(out.FrequentReviewers) != 0 {
		t.Errorf("expected empty enrichment, got %+v", out)
	}
}

func TestHandleCodeReviewContext_Present(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".code-review-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summary := `{
		"high_risk_files": ["a.go"],
		"recent_blockers": [{"title": "open review", "severity": "high"}],
		"frequent_reviewers": ["alice"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	_, out, err := HandleCodeReviewContext(context.Background(), nil, CodeReviewContextInput{ProjectRoot: root})
	if err != nil {
		t.Fatalf("HandleCodeReviewContext: %v", err)
	}
	if !out.Detected {
		t.Fatal("expected Detected=true")
	}
	if len(out.HighRiskFiles) != 1 || out.HighRiskFiles[0] != "a.go" {
		t.Errorf("HighRiskFiles = %v", out.HighRiskFiles)
	}
	if len(out.RecentBlockers) != 1 || out.RecentBlockers[0].Title != "open review" {
		t.Errorf("RecentBlockers = %+v", out.RecentBlockers)
	}
	if len(out.FrequentReviewers) != 1 || out.FrequentReviewers[0] != "alice" {
		t.Errorf("FrequentReviewers = %v", out.FrequentReviewers)
	}
}

func TestHandleCodeReviewContext_Malformed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".code-review-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	_, _, err := HandleCodeReviewContext(context.Background(), nil, CodeReviewContextInput{ProjectRoot: root})
	if err == nil {
		t.Fatal("expected error for malformed summary.json, got nil")
	}
}

func TestHandleCodeReviewContext_DefaultsToCwd(t *testing.T) {
	// Switch into a temp dir so the os.Getwd() fallback path runs;
	// without a summary file we still expect a clean "absent" result.
	root := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	_, out, err := HandleCodeReviewContext(context.Background(), nil, CodeReviewContextInput{})
	if err != nil {
		t.Fatalf("HandleCodeReviewContext: %v", err)
	}
	if out.Detected {
		t.Error("expected Detected=false in clean cwd")
	}
	if out.ProjectRoot == "" {
		t.Error("expected non-empty ProjectRoot from cwd fallback")
	}
}
