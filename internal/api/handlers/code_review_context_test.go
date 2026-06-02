package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

// openCRCTestDB opens a temp store and seeds projectRoot into the worklog
// rollup so the allowlist check passes for it (and rejects any other path).
func openCRCTestDB(t *testing.T, projectRoot string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if projectRoot != "" {
		err := store.UpsertStopSummaryWithWorklog(context.Background(), db,
			store.StopSummary{
				SessionID:   "crc-seed",
				Ts:          10_000,
				ProjectPath: projectRoot,
				CLI:         "claude",
				Summary:     "seed",
				LastUser:    "user",
			},
			store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted"},
		)
		if err != nil {
			t.Fatalf("seed allowlist: %v", err)
		}
	}
	return db
}

func TestCodeReviewContext_Absent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db := openCRCTestDB(t, root)
	body := doCodeReviewContext(t, db, root)
	if body.Detected {
		t.Error("Detected = true, want false for absent dir")
	}
	if body.ProjectRoot != root {
		t.Errorf("ProjectRoot = %q, want %q", body.ProjectRoot, root)
	}
	if len(body.HighRiskFiles) != 0 || len(body.RecentBlockers) != 0 || len(body.FrequentReviewers) != 0 {
		t.Errorf("expected empty enrichment, got %+v", body)
	}
}

func TestCodeReviewContext_Present(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db := openCRCTestDB(t, root)
	dir := filepath.Join(root, ".code-review-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summary := `{
		"high_risk_files": ["x/y.go"],
		"recent_blockers": [
			{"title": "pending review", "url": "https://example/pr/1", "severity": "medium"}
		],
		"frequent_reviewers": ["carol"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	body := doCodeReviewContext(t, db, root)
	if !body.Detected {
		t.Fatal("expected Detected=true")
	}
	if len(body.HighRiskFiles) != 1 || body.HighRiskFiles[0] != "x/y.go" {
		t.Errorf("HighRiskFiles = %v", body.HighRiskFiles)
	}
	if len(body.RecentBlockers) != 1 || body.RecentBlockers[0].Title != "pending review" {
		t.Errorf("RecentBlockers = %+v", body.RecentBlockers)
	}
	if len(body.FrequentReviewers) != 1 || body.FrequentReviewers[0] != "carol" {
		t.Errorf("FrequentReviewers = %v", body.FrequentReviewers)
	}
}

func TestCodeReviewContext_Malformed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db := openCRCTestDB(t, root)
	dir := filepath.Join(root, ".code-review-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	r := chi.NewRouter()
	h := handlers.NewCodeReviewContextHandler(db)
	r.Get(api.RouteCodeReviewContext, h.Get)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL + api.RouteCodeReviewContext)
	q := u.Query()
	q.Set("project_root", root)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for malformed summary.json, got %d", resp.StatusCode)
	}
	// The 500 body must NOT echo the inspected path (info-leak guard #8).
	var bodyMap map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&bodyMap)
	if _, hasPath := bodyMap["project_root"]; hasPath {
		t.Error("malformed-path 500 body leaks project_root")
	}
	if msg, _ := bodyMap["error"].(string); msg != "internal error" {
		t.Errorf("error = %q, want generic %q", msg, "internal error")
	}
}

// TestCodeReviewContext_UnknownRootRejected verifies finding #8: a
// project_root that is NOT in the known-project set is rejected with 403
// (no filesystem probing oracle).
func TestCodeReviewContext_UnknownRootRejected(t *testing.T) {
	t.Parallel()
	// Seed a DIFFERENT path so the requested one is unknown.
	db := openCRCTestDB(t, "/some/known/project")

	r := chi.NewRouter()
	h := handlers.NewCodeReviewContextHandler(db)
	r.Get(api.RouteCodeReviewContext, h.Get)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL + api.RouteCodeReviewContext)
	q := u.Query()
	q.Set("project_root", t.TempDir()) // a real dir, but not in the allowlist
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for unknown project_root, got %d", resp.StatusCode)
	}
}

func doCodeReviewContext(t *testing.T, db *store.DB, projectRoot string) api.CodeReviewContextResponse {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewCodeReviewContextHandler(db)
	r.Get(api.RouteCodeReviewContext, h.Get)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL + api.RouteCodeReviewContext)
	q := u.Query()
	q.Set("project_root", projectRoot)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body api.CodeReviewContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}
