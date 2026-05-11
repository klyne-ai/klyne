package codereviewgraph

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetect_Absent verifies the no-op path when the directory does
// not exist — most repos won't have the upstream tool installed.
func TestDetect_Absent(t *testing.T) {
	t.Parallel()
	if Detect(t.TempDir()) {
		t.Fatal("Detect on empty project root should be false")
	}
}

// TestDetect_EmptyDir checks an empty `.code-review-graph/` is treated
// as "not detected" — the directory needs SOMETHING to enrich from.
func TestDetect_EmptyDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if Detect(root) {
		t.Fatal("Detect on empty .code-review-graph dir should be false")
	}
}

// TestDetect_Populated confirms the happy path: directory present and
// non-empty → Detect returns true.
func TestDetect_Populated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Anything inside counts as populated — schema-conformance is
	// Load's concern, not Detect's.
	if err := os.WriteFile(filepath.Join(dir, "graph.sqlite"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !Detect(root) {
		t.Fatal("Detect on populated dir should be true")
	}
}

// TestLoad_Absent verifies the graceful no-op contract: a project with
// no `.code-review-graph/` returns (nil, nil) so callers can use a
// simple `rg == nil` check.
func TestLoad_Absent(t *testing.T) {
	t.Parallel()
	rg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on absent dir should not error, got %v", err)
	}
	if rg != nil {
		t.Fatalf("Load on absent dir should be nil, got %+v", rg)
	}
}

// TestLoad_NoSummary handles "tool installed, no JSON summary": Load
// returns a non-nil empty ReviewGraph so the UI can render "detected
// but no findings" rather than "not installed".
func TestLoad_NoSummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Drop something other than summary.json so the dir is non-empty
	// (mirrors the upstream tool's SQLite-only layout).
	if err := os.WriteFile(filepath.Join(dir, "graph.sqlite"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("write sqlite stub: %v", err)
	}
	rg, err := Load(root)
	if err != nil {
		t.Fatalf("Load on dir without summary.json should not error, got %v", err)
	}
	if rg == nil {
		t.Fatal("expected non-nil empty ReviewGraph when dir present without summary.json")
	}
	if len(rg.HighRiskFiles) != 0 || len(rg.RecentBlockers) != 0 || len(rg.FrequentReviewers) != 0 {
		t.Errorf("expected empty enrichment, got %+v", rg)
	}
}

// TestLoad_FullParse uses the bundled fixture to confirm the JSON
// contract round-trips cleanly: every field surfaces with the right
// values and ordering preserved.
func TestLoad_FullParse(t *testing.T) {
	t.Parallel()
	root := newFixtureRoot(t, "testdata/full")
	rg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rg == nil {
		t.Fatal("expected ReviewGraph, got nil")
	}
	wantFiles := []string{"internal/cost/engine.go", "cmd/klyne/root.go"}
	if !equalStrings(rg.HighRiskFiles, wantFiles) {
		t.Errorf("HighRiskFiles = %v, want %v", rg.HighRiskFiles, wantFiles)
	}
	wantReviewers := []string{"alice", "bob"}
	if !equalStrings(rg.FrequentReviewers, wantReviewers) {
		t.Errorf("FrequentReviewers = %v, want %v", rg.FrequentReviewers, wantReviewers)
	}
	if len(rg.RecentBlockers) != 2 {
		t.Fatalf("RecentBlockers len = %d, want 2", len(rg.RecentBlockers))
	}
	first := rg.RecentBlockers[0]
	if first.Title != "Refactor cost engine: split per-model rates" {
		t.Errorf("Blocker[0].Title = %q", first.Title)
	}
	if first.Severity != "high" {
		t.Errorf("Blocker[0].Severity = %q, want high", first.Severity)
	}
	if first.URL != "https://github.com/klyne-ai/klyne/pull/123" {
		t.Errorf("Blocker[0].URL = %q", first.URL)
	}
	if first.OpenedAt != "2026-05-09T10:00:00Z" {
		t.Errorf("Blocker[0].OpenedAt = %q", first.OpenedAt)
	}
}

// TestLoad_Malformed verifies a broken JSON summary surfaces an error
// — silent-swallow would let bad fixtures rot indefinitely.
func TestLoad_Malformed(t *testing.T) {
	t.Parallel()
	root := newFixtureRoot(t, "testdata/malformed")
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for malformed summary.json, got nil")
	}
}

// TestLoad_EmptyRoot rejects an empty projectRoot — programmer error,
// not a graceful no-op.
func TestLoad_EmptyRoot(t *testing.T) {
	t.Parallel()
	if _, err := Load(""); err == nil {
		t.Fatal("expected error for empty project root, got nil")
	}
}

// TestLoad_PartialSchema confirms unknown fields are ignored and that
// missing fields default to empty (non-nil) slices.
func TestLoad_PartialSchema(t *testing.T) {
	t.Parallel()
	root := newFixtureRoot(t, "testdata/partial")
	rg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rg == nil {
		t.Fatal("expected non-nil ReviewGraph")
	}
	if len(rg.HighRiskFiles) != 1 || rg.HighRiskFiles[0] != "internal/api/contracts.go" {
		t.Errorf("HighRiskFiles = %v", rg.HighRiskFiles)
	}
	if len(rg.RecentBlockers) != 0 {
		t.Errorf("RecentBlockers should default to empty, got %v", rg.RecentBlockers)
	}
	if len(rg.FrequentReviewers) != 0 {
		t.Errorf("FrequentReviewers should default to empty, got %v", rg.FrequentReviewers)
	}
}

// newFixtureRoot copies a testdata fixture into a fresh temp directory
// and returns its path. Copying avoids mutating the fixture if a test
// ever writes to its `.code-review-graph/` (none do today, but a test
// like that would otherwise corrupt the repo).
func newFixtureRoot(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir(%q, %q): %v", src, dst, err)
	}
	return dst
}

// copyDir is a tiny recursive copy that's enough for our fixtures
// (which are small and shallow). Avoids pulling in a dependency.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
