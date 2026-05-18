package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

// openStatsTestDB opens a fresh in-tempdir SQLite DB. Suffixed to avoid
// collision with the shared openTestDB helper used by advisories_test.go.
func openStatsTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "stats.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedStatsData(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertSession(ctx, db, &connectors.Session{
		ID: "s1", CLI: connectors.CLIClaude, ProjectPath: "/p",
		StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive,
		Model: "claude-sonnet-4-5", RawPath: "/tmp/s1",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	for i, ts := range []int64{now, now + 1, now + 2} {
		if err := store.InsertMessage(ctx, db, &connectors.Message{
			ID: "m-" + string(rune('0'+i)), SessionID: "s1", CLI: connectors.CLIClaude,
			Role:             connectors.RoleAssistant,
			TokensIn:         1000,
			TokensOut:        50,
			CachedReadTokens: 200,
			Model:            "claude-sonnet-4-5",
			Ts:               ts,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}

func TestUsageStatsHandler_HappyPath(t *testing.T) {
	db := openStatsTestDB(t)
	engine, err := cost.New(config.Defaults())
	if err != nil {
		t.Fatalf("cost engine: %v", err)
	}
	seedStatsData(t, db)

	h := handlers.NewUsageStatsHandler(db, engine)
	r := chi.NewRouter()
	r.Get(api.RouteUsageStats, h.Get)

	req := httptest.NewRequest(http.MethodGet, "/usage/stats?days=30&heatmap_weeks=4", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["total_messages"].(float64) != 3 {
		t.Errorf("total_messages = %v, want 3", out["total_messages"])
	}
	if out["favorite_model"].(string) != "claude-sonnet-4-5" {
		t.Errorf("favorite_model = %v", out["favorite_model"])
	}
	if heatmap, ok := out["heatmap"].([]any); !ok || len(heatmap) != 28 {
		t.Errorf("heatmap len = %d, want 28 (4 weeks × 7 days)", len(heatmap))
	}
}

func TestUsageStatsHandler_InvalidCLIRejected(t *testing.T) {
	db := openStatsTestDB(t)
	engine, _ := cost.New(config.Defaults())
	h := handlers.NewUsageStatsHandler(db, engine)
	r := chi.NewRouter()
	r.Get(api.RouteUsageStats, h.Get)

	req := httptest.NewRequest(http.MethodGet, "/usage/stats?cli=bogus", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUsageStatsHandler_DefaultsApplyWithoutParams(t *testing.T) {
	db := openStatsTestDB(t)
	engine, _ := cost.New(config.Defaults())
	seedStatsData(t, db)

	h := handlers.NewUsageStatsHandler(db, engine)
	r := chi.NewRouter()
	r.Get(api.RouteUsageStats, h.Get)

	req := httptest.NewRequest(http.MethodGet, "/usage/stats", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// 30-day default + 12-week heatmap.
	if heatmap, ok := out["heatmap"].([]any); !ok || len(heatmap) != 84 {
		t.Errorf("default heatmap len = %d, want 84 (12 weeks)", len(heatmap))
	}
}
