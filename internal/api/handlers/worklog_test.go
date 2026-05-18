package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

func newWorklogRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewWorklogHandler(db)
	r.Get(api.RouteWorklog, h.List)
	return r
}

func seedWorklogRows(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	// Plain int timestamps — mirrors stop_summaries_test.go which avoids
	// the time import on purpose. tsNewest is the most recent.
	const tsNewest = int64(30_000)
	const tsMid = int64(20_000)
	const tsOldest = int64(10_000)

	rows := []struct {
		row store.StopSummary
		w   store.WorklogColumns
	}{
		{
			row: store.StopSummary{SessionID: "wl-a-visible", Ts: tsNewest, ProjectPath: "/proj/a", CLI: "claude", Summary: "## did stuff", LastUser: "create hello.js", Files: []string{"hello.js"}},
			w:   store.WorklogColumns{RecapVisible: 1, Importance: 7, RecapTopic: "feature", DraftState: "accepted"},
		},
		{
			row: store.StopSummary{SessionID: "wl-a-suppressed", Ts: tsOldest, ProjectPath: "/proj/a", CLI: "claude", Summary: "## trivia", LastUser: "what is 2+2"},
			w:   store.WorklogColumns{RecapVisible: 0, Importance: 2, DraftState: "proposed"},
		},
		{
			row: store.StopSummary{SessionID: "wl-b-visible", Ts: tsMid, ProjectPath: "/proj/b", CLI: "codex", Summary: "## fix", LastUser: "fix bug"},
			w:   store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted"},
		},
	}
	for _, r := range rows {
		if err := store.UpsertStopSummaryWithWorklog(ctx, db, r.row, r.w); err != nil {
			t.Fatalf("seed %s: %v", r.row.SessionID, err)
		}
	}
}

func TestWorklog_List_GroupsByProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items")
	if err != nil {
		t.Fatalf("GET /worklog/items: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.GlobalCount != 0 {
		t.Errorf("GlobalCount = %d, want 0 (no project_path='' rows seeded)", body.GlobalCount)
	}
	if body.ProjectCount != 2 {
		t.Errorf("ProjectCount = %d, want 2 (/proj/a + /proj/b)", body.ProjectCount)
	}
	if body.Total != 3 {
		t.Errorf("Total = %d, want 3", body.Total)
	}
	// /proj/a should sort first (has the newest entry, tsNewest=30_000).
	if len(body.ByProject) == 0 || body.ByProject[0].ProjectPath != "/proj/a" {
		t.Errorf("ByProject[0]=%v, want /proj/a first", body.ByProject)
	}
}

func TestWorklog_List_IncludesSuppressedRows(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var anySuppressed bool
	for _, g := range body.ByProject {
		for _, e := range g.Entries {
			if e.RecapVisible == 0 {
				anySuppressed = true
			}
		}
	}
	if !anySuppressed {
		t.Error("expected suppressed rows in response; got none")
	}
}

func TestWorklog_List_ScopedByProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items?project=/proj/a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.ProjectCount != 1 {
		t.Errorf("ProjectCount = %d, want 1 (only /proj/a)", body.ProjectCount)
	}
	if body.Total != 2 {
		t.Errorf("Total = %d, want 2 (/proj/a has 2 rows)", body.Total)
	}
}
