package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	r.Get(api.RouteWorklogProject, h.Project)
	return r
}

// seedWorklogScenarios populates one project per tier so the sort order can
// be asserted end-to-end.
func seedWorklogScenarios(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()

	// Stale-with-reflection: /proj/stale — reflection at ts=20_000, new entries after.
	mustUpsert(t, ctx, db, "stale-old", 10_000, "/proj/stale", 1)
	mustReflect(t, ctx, db, "stale-r", 20_000, "/proj/stale", "stale-old")
	mustUpsert(t, ctx, db, "stale-new1", 30_000, "/proj/stale", 1)
	mustUpsert(t, ctx, db, "stale-new2", 31_000, "/proj/stale", 1)

	// Cold-start: /proj/cold — visible entry, no reflection.
	mustUpsert(t, ctx, db, "cold-1", 25_000, "/proj/cold", 1)

	// Fresh: /proj/fresh — reflection AFTER the only entry (no pending).
	mustUpsert(t, ctx, db, "fresh-e", 5_000, "/proj/fresh", 1)
	mustReflect(t, ctx, db, "fresh-r", 15_000, "/proj/fresh", "fresh-e")
}

func mustUpsert(t *testing.T, ctx context.Context, db *store.DB, sid string, ts int64, project string, visible int) {
	t.Helper()
	err := store.UpsertStopSummaryWithWorklog(ctx, db,
		store.StopSummary{SessionID: sid, Ts: ts, ProjectPath: project, CLI: "claude", Summary: "x", LastUser: "y"},
		store.WorklogColumns{RecapVisible: visible, Importance: 5, DraftState: "accepted"},
	)
	if err != nil {
		t.Fatalf("upsert %s: %v", sid, err)
	}
}

func mustReflect(t *testing.T, ctx context.Context, db *store.DB, id string, ts int64, project, evidenceID string) {
	t.Helper()
	err := store.InsertReflection(ctx, db, store.Reflection{
		ID: id, TS: ts, ProjectPath: project, Tier: 2, Title: "test reflection",
		BodyMD: "did things", State: "accepted", SummarySource: "ai", Importance: 5,
		EvidenceEntryIDs: []string{evidenceID},
	})
	if err != nil {
		t.Fatalf("insert reflection %s: %v", id, err)
	}
}

func TestWorklog_List_ReturnsOnePerProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogScenarios(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Projects) != 3 {
		t.Fatalf("Projects len=%d, want 3 (stale, cold, fresh)", len(body.Projects))
	}
}

func TestWorklog_List_SortsByMostRecentActivity(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogScenarios(t, db)

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

	// Sort is purely by most-recent activity (entry OR reflection ts),
	// newest first. Per the seed: /proj/stale has entries at 31_000 (newest),
	// /proj/cold has an entry at 25_000, /proj/fresh has a reflection at 15_000.
	wantOrder := []string{"/proj/stale", "/proj/cold", "/proj/fresh"}
	for i, want := range wantOrder {
		if body.Projects[i].ProjectPath != want {
			t.Errorf("Projects[%d].ProjectPath=%s, want %s", i, body.Projects[i].ProjectPath, want)
		}
	}
}

func TestWorklog_List_StalenessAndPendingCount(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogScenarios(t, db)

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

	byPath := map[string]store.WorklogProjectRollup{}
	for _, p := range body.Projects {
		byPath[p.ProjectPath] = p
	}

	stale := byPath["/proj/stale"]
	if !stale.Stale || stale.PendingEntries != 2 || stale.LatestReflection == nil {
		t.Errorf("stale: stale=%v pending=%d hasRefl=%v",
			stale.Stale, stale.PendingEntries, stale.LatestReflection != nil)
	}
	cold := byPath["/proj/cold"]
	if !cold.Stale || cold.PendingEntries != 1 || cold.LatestReflection != nil {
		t.Errorf("cold: stale=%v pending=%d hasRefl=%v",
			cold.Stale, cold.PendingEntries, cold.LatestReflection != nil)
	}
	fresh := byPath["/proj/fresh"]
	if fresh.Stale || fresh.PendingEntries != 0 || fresh.LatestReflection == nil {
		t.Errorf("fresh: stale=%v pending=%d hasRefl=%v",
			fresh.Stale, fresh.PendingEntries, fresh.LatestReflection != nil)
	}
}

func TestWorklog_List_EmptyDB(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

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
	if len(body.Projects) != 0 {
		t.Errorf("Projects len=%d, want 0 for empty DB", len(body.Projects))
	}
}

func TestWorklogProject_ReturnsAllReflectionsForOneProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	// Two reflections under /proj/x, one under /proj/y. The handler must
	// only return /proj/x rows.
	mustUpsert(t, ctx, db, "x-e1", 10_000, "/proj/x", 1)
	mustReflect(t, ctx, db, "x-r1", 20_000, "/proj/x", "x-e1")
	mustUpsert(t, ctx, db, "x-e2", 30_000, "/proj/x", 1)
	mustReflect(t, ctx, db, "x-r2", 40_000, "/proj/x", "x-e2")
	mustUpsert(t, ctx, db, "y-e1", 50_000, "/proj/y", 1)
	mustReflect(t, ctx, db, "y-r1", 60_000, "/proj/y", "y-e1")

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project?path=" + url.QueryEscape("/proj/x"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body api.WorklogProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Project.ProjectPath != "/proj/x" {
		t.Errorf("project=%s, want /proj/x", body.Project.ProjectPath)
	}
	if len(body.Reflections) != 2 {
		t.Fatalf("Reflections len=%d, want 2", len(body.Reflections))
	}
	// Newest first.
	if body.Reflections[0].ID != "x-r2" || body.Reflections[1].ID != "x-r1" {
		t.Errorf("order wrong: got %s,%s; want x-r2,x-r1", body.Reflections[0].ID, body.Reflections[1].ID)
	}
}

func TestWorklogProject_RejectsMissingPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestWorklogProject_UnknownProjectReturnsEmpty(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project?path=" + url.QueryEscape("/no/such/proj"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body api.WorklogProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Project.ProjectPath != "/no/such/proj" {
		t.Errorf("project=%s, want /no/such/proj (echo even when empty)", body.Project.ProjectPath)
	}
	if body.Project.Name != "proj" {
		t.Errorf("Name=%q, want %q (basename of /no/such/proj)", body.Project.Name, "proj")
	}
	if len(body.Reflections) != 0 {
		t.Errorf("Reflections len=%d, want 0", len(body.Reflections))
	}
}
