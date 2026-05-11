package handlers_test

import (
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
)

func TestCodeReviewContext_Absent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := doCodeReviewContext(t, root)
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

	body := doCodeReviewContext(t, root)
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
	dir := filepath.Join(root, ".code-review-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	r := chi.NewRouter()
	h := handlers.NewCodeReviewContextHandler()
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
}

func doCodeReviewContext(t *testing.T, projectRoot string) api.CodeReviewContextResponse {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewCodeReviewContextHandler()
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
