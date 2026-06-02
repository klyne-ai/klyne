package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

func openProjectDeleteHandlerDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "pdh.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newProjectDeleteRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewProjectDeleteHandler(db)
	r.Delete(api.RouteProjectDelete, h.Delete)
	return r
}

// seedHandlerProjectRows seeds enough rows so the dry-run + delete paths
// have non-zero counts to report. Only the tables required by the test
// assertions are written (subset of the store-layer seed).
func seedHandlerProjectRows(t *testing.T, db *store.DB, projectPath string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Write().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed exec: %v", err)
		}
	}
	// recap_visible=1 so the path appears in ListWorklogRollup — the
	// allowlist a real (non-dry-run) delete now requires (#9).
	exec(`INSERT INTO stop_summaries (session_id, ts, project_path, summary, recap_visible)
	      VALUES ('s1', 1000, ?, 'x', 1)`, projectPath)
	exec(`INSERT INTO decisions (id, ts, project_path, text)
	      VALUES ('d1', 1, ?, 'd')`, projectPath)
}

func TestProjectDelete_MissingPathIs400(t *testing.T) {
	db := openProjectDeleteHandlerDB(t)
	srv := httptest.NewServer(newProjectDeleteRouter(t, db))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestProjectDelete_DryRunReturnsCountsAndDoesNotMutate(t *testing.T) {
	db := openProjectDeleteHandlerDB(t)
	const projectPath = "/proj/dry"
	seedHandlerProjectRows(t, db, projectPath)

	srv := httptest.NewServer(newProjectDeleteRouter(t, db))
	t.Cleanup(srv.Close)

	q := url.Values{"path": {projectPath}, "dry_run": {"true"}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete+"?"+q.Encode(), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body api.ProjectDeleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Counts.StopSummaries != 1 || body.Counts.Decisions != 1 {
		t.Errorf("dry-run counts: got %+v, want stop=1 dec=1", body.Counts)
	}
	if body.Counts.Total != 2 {
		t.Errorf("dry-run total: got %d, want 2", body.Counts.Total)
	}
	if body.Deleted {
		t.Error("dry-run response should have deleted=false")
	}

	// Re-running dry-run yields the same counts → no mutation occurred.
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	defer resp2.Body.Close() //nolint:errcheck
	var body2 api.ProjectDeleteResponse
	_ = json.NewDecoder(resp2.Body).Decode(&body2)
	if body2.Counts.Total != 2 {
		t.Errorf("dry-run is not read-only: got %d, want 2", body2.Counts.Total)
	}
}

// TestProjectDelete_RealDeleteUnknownPathRejected verifies finding #9: a
// non-dry-run delete of a path NOT in the known-project set returns 403 and
// does not mutate. dry_run for the same unknown path is still allowed.
func TestProjectDelete_RealDeleteUnknownPathRejected(t *testing.T) {
	db := openProjectDeleteHandlerDB(t)
	// Seed a DIFFERENT known path so the rollup is non-empty but the target
	// below is unknown.
	seedHandlerProjectRows(t, db, "/proj/known")

	srv := httptest.NewServer(newProjectDeleteRouter(t, db))
	t.Cleanup(srv.Close)

	const unknown = "/proj/unknown"

	// Real delete → 403.
	q := url.Values{"path": {unknown}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete+"?"+q.Encode(), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("real delete of unknown path: got %d, want 403", resp.StatusCode)
	}

	// dry_run for the unknown path is still allowed (preview, no mutation).
	qd := url.Values{"path": {unknown}, "dry_run": {"true"}}
	reqd, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete+"?"+qd.Encode(), nil)
	respd, err := srv.Client().Do(reqd)
	if err != nil {
		t.Fatalf("dry-run request: %v", err)
	}
	defer respd.Body.Close() //nolint:errcheck
	if respd.StatusCode != http.StatusOK {
		t.Errorf("dry-run of unknown path: got %d, want 200", respd.StatusCode)
	}
}

func TestProjectDelete_RealDeletePerformsAndReports(t *testing.T) {
	db := openProjectDeleteHandlerDB(t)
	const projectPath = "/proj/real"
	seedHandlerProjectRows(t, db, projectPath)

	srv := httptest.NewServer(newProjectDeleteRouter(t, db))
	t.Cleanup(srv.Close)

	q := url.Values{"path": {projectPath}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete+"?"+q.Encode(), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body api.ProjectDeleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Deleted {
		t.Error("real-run response should have deleted=true")
	}
	if body.Counts.Total != 2 {
		t.Errorf("counts: got %d, want 2", body.Counts.Total)
	}

	// Verify the data is actually gone via a follow-up dry-run.
	q2 := url.Values{"path": {projectPath}, "dry_run": {"true"}}
	req2, _ := http.NewRequest(http.MethodDelete, srv.URL+api.RouteProjectDelete+"?"+q2.Encode(), nil)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	defer resp2.Body.Close() //nolint:errcheck
	var body2 api.ProjectDeleteResponse
	_ = json.NewDecoder(resp2.Body).Decode(&body2)
	if body2.Counts.Total != 0 {
		t.Errorf("post-delete dry-run should be zero, got %+v", body2.Counts)
	}
}
