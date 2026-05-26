package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// newRecomposeRouter wires the recompose handler under the global
// router (and thus the same-origin middleware) so the test exercises
// the same request path the daemon serves.
func newRecomposeRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	h := NewProductivityRecomposeHandler(db)
	return api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&recomposeHandlerMounter{h: h}},
	})
}

type recomposeHandlerMounter struct{ h *ProductivityRecomposeHandler }

func (m *recomposeHandlerMounter) Mount(r chi.Router) {
	r.Post(api.RouteProductivityRecompose, m.h.Run)
}

// seedTypedReflection drops one worklog_reflections row carrying a typed
// body_json payload so the recompose handler has something to compose.
func seedTypedReflection(
	t *testing.T, db *store.DB, projectPath, day, service string,
) {
	t.Helper()
	ctx := context.Background()
	body, err := json.Marshal(store.WWDPayload{
		Service: service,
		Details: []store.WWDDetail{
			{Kind: "SHIPPED", When: "10:00",
				Text:      "shipped one thing",
				Evidence:  []string{"abc1234", "internal/foo.go"},
				SessionID: "sess-1"},
			{Kind: "FIXED", When: "11:30",
				Text:      "fixed one thing",
				Evidence:  []string{"def4567"},
				SessionID: "sess-2"},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	r := store.Reflection{
		ID:               "ref-typed-" + day,
		ProjectPath:      projectPath,
		TS:               1_000_000,
		Tier:             1,
		Title:            "Daily reflection — " + day,
		BodyMD:           "- shipped\n- fixed\n",
		Day:              day,
		BodyJSON:         string(body),
		EvidenceEntryIDs: []string{"e-1"},
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   1_000_000,
		Importance:       7,
	}
	if err := store.InsertReflection(ctx, db, r); err != nil {
		t.Fatalf("insert reflection: %v", err)
	}
}

func TestProductivityRecompose_HappyPath(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t) // shared with worklog_reflect_run_test.go
	const proj = "/repo/klyne"
	const day = "2026-05-26"
	seedTypedReflection(t, db, proj, day, "klyne")

	srv := httptest.NewServer(newRecomposeRouter(t, db))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"project_path": proj,
		"day":          day,
	})
	resp, err := http.Post(srv.URL+api.RouteProductivityRecompose,
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got productivityRecomposeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CardCount != 1 || got.ServiceCount != 1 {
		t.Errorf("counts = %+v, want both 1", got)
	}

	// The handler must have persisted a daily_productivity_snapshot row
	// for (proj, day) carrying the typed cards on the matching service.
	snap, found, err := store.GetDailyProductivitySnapshot(context.Background(), db, proj, day)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if !found {
		t.Fatalf("no snapshot row written")
	}
	if snap.Source != "reflection" {
		t.Errorf("Source = %q, want reflection", snap.Source)
	}
	var rep productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &rep); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(rep.Services) != 1 || rep.Services[0].WhatWasDone == nil {
		t.Fatalf("persisted Report missing WhatWasDone card: %+v", rep)
	}
	card := rep.Services[0].WhatWasDone
	if card.Tier1.PillCounts["shipped"] != 1 || card.Tier1.PillCounts["fixed"] != 1 {
		t.Errorf("pill_counts = %+v, want shipped=1 fixed=1", card.Tier1.PillCounts)
	}
	if len(card.Tier2.Details) != 2 {
		t.Errorf("Tier2 detail count = %d, want 2", len(card.Tier2.Details))
	}
}

func TestProductivityRecompose_DefaultsDayToToday(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)

	srv := httptest.NewServer(newRecomposeRouter(t, db))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"project_path": "/p",
		// day intentionally omitted
	})
	resp, err := http.Post(srv.URL+api.RouteProductivityRecompose,
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got productivityRecomposeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// No reflections seeded → zero cards but still 200.
	if got.CardCount != 0 || got.ServiceCount != 0 {
		t.Errorf("counts = %+v, want both 0", got)
	}
}

func TestProductivityRecompose_RejectsMissingProjectPath(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	srv := httptest.NewServer(newRecomposeRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+api.RouteProductivityRecompose,
		"application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProductivityRecompose_RejectsInvalidDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	srv := httptest.NewServer(newRecomposeRouter(t, db))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"project_path": "/p",
		"day":          "not-a-date",
	})
	resp, err := http.Post(srv.URL+api.RouteProductivityRecompose,
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRecomposeWhatWasDone_PreservesExistingSnapshotPayload guards the
// merge semantics in persistRecomposedCards — the existing snapshot
// (e.g. previously written by the reflection recorder with a full
// Service list) must keep its branches/risks/sessions; only the
// per-service WhatWasDone pointer flips.
func TestRecomposeWhatWasDone_PreservesExistingSnapshotPayload(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	ctx := context.Background()
	const proj = "/repo/klyne"
	const day = "2026-05-26"

	// Seed a prior snapshot with one Service carrying a Branch row.
	pre := productivity.Report{
		Day: day,
		Services: []productivity.Service{{
			Repo:        "klyne",
			ProjectPath: proj,
			Branches:    []productivity.Branch{{Name: "main", Narrative: "did things"}},
			Risks:       []productivity.RiskSignal{},
			MergedPRs:   []productivity.MergedPR{},
		}},
	}
	payload, _ := json.Marshal(pre)
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, store.DailyProductivitySnapshot{
		ProjectPath:        proj,
		Day:                day,
		PayloadJSON:        string(payload),
		TotalActiveMinutes: 42,
		Source:             "live",
		CreatedAt:          1,
		UpdatedAt:          1,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	seedTypedReflection(t, db, proj, day, "klyne")

	cards, err := RecomposeWhatWasDone(ctx, db, proj, day)
	if err != nil {
		t.Fatalf("RecomposeWhatWasDone: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}

	snap, _, err := store.GetDailyProductivitySnapshot(ctx, db, proj, day)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	var post productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &post); err != nil {
		t.Fatalf("unmarshal post: %v", err)
	}
	if len(post.Services) != 1 {
		t.Fatalf("Services len = %d, want 1", len(post.Services))
	}
	if len(post.Services[0].Branches) != 1 || post.Services[0].Branches[0].Name != "main" {
		t.Errorf("branches not preserved: %+v", post.Services[0].Branches)
	}
	if post.Services[0].WhatWasDone == nil {
		t.Errorf("WhatWasDone pointer missing after recompose")
	}
}
