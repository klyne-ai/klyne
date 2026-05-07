package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// newSearchRouter creates a router with only the search handler (no middleware).
func newSearchRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewSearchHandler(db)
	r.Get(api.RouteSearch, h.Search)
	return r
}

func seedSearchData(t *testing.T, db *store.DB) {
	t.Helper()
	sess := &connectors.Session{
		ID:          "search-sess-1",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/search-proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	msgs := []struct {
		id      string
		content string
	}{
		{"smsg-1", "the quick brown fox jumps over the lazy dog"},
		{"smsg-2", "Go programming language features goroutines"},
		{"smsg-3", "SQLite is a lightweight embedded database engine"},
	}
	for i, m := range msgs {
		msg := &connectors.Message{
			ID:        m.id,
			SessionID: "search-sess-1",
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleUser,
			Content:   m.content,
			Ts:        int64(1000 + i*100),
		}
		if err := store.InsertMessage(context.Background(), db, msg); err != nil {
			t.Fatalf("insert message %q: %v", m.id, err)
		}
	}
}

func TestSearch_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSearchData(t, db)

	srv := httptest.NewServer(newSearchRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/search?q=goroutines")
	if err != nil {
		t.Fatalf("GET /search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Query != "goroutines" {
		t.Errorf("expected query 'goroutines', got %q", body.Query)
	}
	if len(body.Hits) == 0 {
		t.Error("expected at least one hit")
	}
	if body.Took < 0 {
		t.Errorf("took_ms should be non-negative, got %d", body.Took)
	}
}

func TestSearch_MissingQuery(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newSearchRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/search")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing q, got %d", resp.StatusCode)
	}
}

func TestSearch_InvalidLimit(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newSearchRouter(t, db))
	t.Cleanup(srv.Close)

	for _, tc := range []string{"?q=fox&limit=abc", "?q=fox&limit=0", "?q=fox&limit=201"} {
		t.Run(tc, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/search" + tc)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// No data seeded — search should return empty hits.

	srv := httptest.NewServer(newSearchRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/search?q=xyzzy_unique_term")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body api.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Hits) != 0 {
		t.Errorf("expected 0 hits, got %d", len(body.Hits))
	}
}

// TestSearch_LoopbackMiddleware verifies loopback enforcement on /search.
// Sequential: buildFullRouter mutates config.HomeDir.
func TestSearch_LoopbackMiddleware(t *testing.T) {
	router := buildFullRouter(t)

	// Non-loopback IP should get 403.
	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-loopback should get 403, got %d", w.Code)
	}

	// Loopback IP should pass through.
	req2 := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	req2.RemoteAddr = "127.0.0.1:4321"

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("loopback should not get 403, got %d", w2.Code)
	}
}
