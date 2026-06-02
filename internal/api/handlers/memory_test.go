package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

// newMemoryRouter wires the /memory + /memory/{id} routes on a fresh
// chi router with the test DB. Mirrors newCostRouter's shape.
func newMemoryRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewMemoryHandler(db)
	r.Get(api.RouteMemory, h.List)
	r.Delete(api.RouteMemoryItem, h.Delete)
	return r
}

// seedMemoryRows inserts a deterministic mix of globals + per-project
// memories so the grouping behaviour can be asserted precisely.
func seedMemoryRows(t *testing.T, db *store.DB) {
	t.Helper()
	now := time.Now().UnixMilli()
	rows := []*store.Decision{
		// Global memory (oldest).
		{ID: "d-global-1", Ts: now - 5000, ProjectPath: "", Text: "RUNBOOK: deploy any service via ./scripts/deploy.sh $SVC", Tags: []string{"runbook", "deploy"}},
		// Two project A memories.
		{ID: "d-projA-1", Ts: now - 4000, ProjectPath: "/proj/auth-service", Text: "Use bao for secrets, not env files.", Tags: []string{"decision", "secrets"}},
		{ID: "d-projA-2", Ts: now - 1000, ProjectPath: "/proj/auth-service", Text: "Bucket layout: auth-service/main holds prod, auth-service/staging holds staging.", Tags: []string{"infra"}},
		// One project B memory (most recent — should sort first among projects).
		{ID: "d-projB-1", Ts: now - 500, ProjectPath: "/proj/consult-service", Text: "GoogleAI key lives in consult-service/main bucket.", Tags: []string{"secrets"}},
	}
	for _, d := range rows {
		if err := store.InsertDecision(context.Background(), db, d); err != nil {
			t.Fatalf("insert %s: %v", d.ID, err)
		}
	}
}

func TestMemory_List_GroupsByProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedMemoryRows(t, db)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/memory/items")
	if err != nil {
		t.Fatalf("GET /memory/items: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.MemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.GlobalCount != 1 {
		t.Errorf("GlobalCount = %d, want 1", body.GlobalCount)
	}
	if body.ProjectCount != 2 {
		t.Errorf("ProjectCount = %d, want 2", body.ProjectCount)
	}
	if body.Total != 4 {
		t.Errorf("Total = %d, want 4", body.Total)
	}

	// Global row should be the deploy runbook.
	if len(body.Global) != 1 || body.Global[0].ID != "d-global-1" {
		t.Errorf("Global rows = %+v, want [d-global-1]", body.Global)
	}

	// Project order: consult-service first (most-recent memory at
	// now-500ms), then auth-service (now-1000ms).
	if len(body.ByProject) != 2 {
		t.Fatalf("ByProject len = %d, want 2", len(body.ByProject))
	}
	if body.ByProject[0].Name != "consult-service" {
		t.Errorf("group[0].Name = %q, want consult-service", body.ByProject[0].Name)
	}
	if body.ByProject[1].Name != "auth-service" {
		t.Errorf("group[1].Name = %q, want auth-service", body.ByProject[1].Name)
	}

	// Each project group's memories are newest-first.
	if g := body.ByProject[1]; len(g.Memories) != 2 || g.Memories[0].ID != "d-projA-2" {
		t.Errorf("auth-service memories = %+v, want newest first (d-projA-2, d-projA-1)", g.Memories)
	}

	// Counts on each group mirror the slice lengths.
	for _, g := range body.ByProject {
		if g.Count != len(g.Memories) {
			t.Errorf("group %s: Count=%d but len(Memories)=%d", g.Name, g.Count, len(g.Memories))
		}
	}
}

func TestMemory_List_FilterByTag(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedMemoryRows(t, db)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/memory/items?tag=secrets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.MemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Only "secrets"-tagged rows. Two of them, both project-scoped.
	if body.GlobalCount != 0 {
		t.Errorf("GlobalCount = %d, want 0 (runbook tag is 'deploy' not 'secrets')", body.GlobalCount)
	}
	if body.Total != 2 {
		t.Errorf("Total = %d, want 2 (d-projA-1, d-projB-1)", body.Total)
	}
}

func TestMemory_List_FilterByQuery(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedMemoryRows(t, db)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	// Case-insensitive substring "BUCKET" should hit the two bucket-related rows.
	resp, err := http.Get(srv.URL + "/memory/items?q=bucket")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.MemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 {
		t.Errorf("Total = %d, want 2 (d-projA-2 mentions 'Bucket layout'; d-projB-1 mentions 'consult-service/main bucket')", body.Total)
	}
}

func TestMemory_Delete_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedMemoryRows(t, db)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	// DELETE one row.
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/memory/items/d-projA-1", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Confirm it's gone via a follow-up GET.
	gresp, err := http.Get(srv.URL + "/memory/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer gresp.Body.Close()
	var body api.MemoryResponse
	if err := json.NewDecoder(gresp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 3 {
		t.Errorf("Total after delete = %d, want 3", body.Total)
	}
	for _, g := range body.ByProject {
		for _, m := range g.Memories {
			if m.ID == "d-projA-1" {
				t.Errorf("deleted id d-projA-1 still present in group %s", g.Name)
			}
		}
	}
}

func TestMemory_Delete_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/memory/items/d-does-not-exist", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMemory_List_Empty(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newMemoryRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/memory/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.MemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("Total = %d, want 0", body.Total)
	}
	// Slices must be non-nil even when empty so the JSON renders as `[]`,
	// not `null` (the dashboard relies on this).
	if body.Global == nil {
		t.Errorf("Global is nil, want []")
	}
	if body.ByProject == nil {
		t.Errorf("ByProject is nil, want []")
	}
}
