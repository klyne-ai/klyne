package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProductivityRecomposeHandler serves POST /api/productivity/recompose
// — the typed What-was-done re-derivation endpoint (§1.3 of
// docs/plan/2026-05-26-wwd-typed-cards.md).
//
// The handler does NO LLM work: it reads worklog_reflections rows for
// (project_path, day), runs the pure-Go productivity.ComposeWWD
// composer over them, and persists the resulting cards into the
// existing daily_productivity_snapshot row for the day (creating one
// when none exists). Idempotent — re-running with the same inputs
// produces the same payload.
//
// The /worklog/reflect/run handler chains a server-side call to this
// path after a successful reflect so the dashboard's What-was-done
// panel shows the new cards on the same UI round-trip.
type ProductivityRecomposeHandler struct {
	db *store.DB
}

// NewProductivityRecomposeHandler constructs a recompose handler bound
// to the daemon's store.
func NewProductivityRecomposeHandler(db *store.DB) *ProductivityRecomposeHandler {
	return &ProductivityRecomposeHandler{db: db}
}

// productivityRecomposeRequest is the §1.3 POST body. Day is optional —
// defaults to today (server-local).
type productivityRecomposeRequest struct {
	ProjectPath string `json:"project_path"`
	Day         string `json:"day,omitempty"`
}

// productivityRecomposeResponse matches §1.3: bare counts so the UI
// can verify the call landed without re-fetching the whole dashboard.
type productivityRecomposeResponse struct {
	ServiceCount int `json:"service_count"`
	CardCount    int `json:"card_count"`
}

// Run handles POST /api/productivity/recompose.
func (h *ProductivityRecomposeHandler) Run(w http.ResponseWriter, r *http.Request) {
	var req productivityRecomposeRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.ProjectPath = strings.TrimSpace(req.ProjectPath)
	if req.ProjectPath == "" {
		http.Error(w, "missing project_path", http.StatusBadRequest)
		return
	}
	dayStr := strings.TrimSpace(req.Day)
	if dayStr == "" {
		dayStr = time.Now().Local().Format("2006-01-02")
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		http.Error(w, "invalid day: must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	cards, err := RecomposeWhatWasDone(r.Context(), h.db, req.ProjectPath, dayStr)
	if err != nil {
		log.Printf("productivity recompose: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := productivityRecomposeResponse{
		ServiceCount: len(cards),
		CardCount:    len(cards),
	}
	writeJSON(w, http.StatusOK, resp)
}

// RecomposeWhatWasDone runs the §1.3 derivation for one (project_path,
// dayStr) tuple: reads worklog_reflections rows for the day, composes
// the typed cards via productivity.ComposeWWD, and upserts them into
// the day's daily_productivity_snapshot row.
//
// Exported (lower-case `r`) so handlers in this package can call it
// inline after a successful /worklog/reflect/run without duplicating
// the marshal/upsert dance. Always returns the composed cards (possibly
// empty) so callers can include them in their response payload.
func RecomposeWhatWasDone(
	ctx context.Context, db *store.DB, projectPath, dayStr string,
) ([]productivity.WhatWasDoneCard, error) {
	rows, err := store.ListReflectionsForProjectDay(ctx, db, projectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("list reflections: %w", err)
	}
	cards := productivity.ComposeWWD(rows)

	if err := persistRecomposedCards(ctx, db, projectPath, dayStr, cards); err != nil {
		return cards, fmt.Errorf("persist snapshot: %w", err)
	}
	return cards, nil
}

// persistRecomposedCards writes the typed cards into the day's
// daily_productivity_snapshot row for projectPath. When a snapshot
// already exists (the common case — the dashboard or the reflection
// recorder wrote it earlier), the existing payload is preserved and
// only the per-service `what_was_done` field is updated. When no
// snapshot exists, a minimal payload is created that carries just the
// cards under one synthetic Service entry — enough for the dashboard
// to render the panel; the next live-compute will fill in the rest.
//
// Source precedence: the underlying upsert refuses to clobber a
// "reflection" row with a "live" row (productivity_snapshots.go).
// Recompose writes with source="reflection" because the cards were
// derived from the reflection rows themselves — they ARE the
// authoritative typed-cards payload for the day.
func persistRecomposedCards(
	ctx context.Context, db *store.DB, projectPath, dayStr string,
	cards []productivity.WhatWasDoneCard,
) error {
	cardByService := map[string]*productivity.WhatWasDoneCard{}
	for i := range cards {
		c := &cards[i]
		cardByService[c.Service] = c
	}

	now := time.Now().UnixMilli()

	existing, found, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, dayStr)
	if err != nil {
		return err
	}

	var rep productivity.Report
	if found && strings.TrimSpace(existing.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(existing.PayloadJSON), &rep); err != nil {
			// Defensive: treat a corrupt persisted payload as "no snapshot"
			// — better to overwrite garbage than to refuse to recompose.
			rep = productivity.Report{}
		}
	}
	if rep.Day == "" {
		rep.Day = dayStr
	}

	// Splice the new cards onto matching services. Any card whose
	// Service doesn't already appear in the report becomes a new
	// Service stub so the typed panel renders even before the next
	// live-compute fills in branches/risks/sessions.
	//
	// PRESERVE LLM-COMPILED CARDS: when an existing service already
	// carries an llm_compiled=true What-was-done card (written by
	// productivity-sync / PersistLLMCompiledCard), do NOT overwrite it
	// with the mechanical recompose output. The deterministic compose
	// would set llm_compiled=false and pending_compile would jump back
	// up every time /klyne:reflect chains the recompose — wiping the
	// user's previous "Generate productivity" work. The LLM-compiled
	// card stays until productivity-sync overwrites it with a fresh
	// llm_compiled=true payload.
	matched := map[string]struct{}{}
	for i := range rep.Services {
		svc := &rep.Services[i]
		key := serviceKey(*svc)
		if svc.WhatWasDone != nil && svc.WhatWasDone.LLMCompiled {
			// Mark as matched so we don't append a stub for this service
			// below; leave the existing LLM-compiled card untouched.
			matched[key] = struct{}{}
			continue
		}
		if c, ok := cardByService[key]; ok {
			cardCopy := *c
			svc.WhatWasDone = &cardCopy
			matched[key] = struct{}{}
		} else {
			svc.WhatWasDone = nil
		}
	}
	for key, c := range cardByService {
		if _, ok := matched[key]; ok {
			continue
		}
		stub := productivity.Service{
			Repo:        key,
			ProjectPath: projectPath,
			Branches:    []productivity.Branch{},
			Risks:       []productivity.RiskSignal{},
			MergedPRs:   []productivity.MergedPR{},
			WhatWasDone: cloneCardPtr(*c),
		}
		rep.Services = append(rep.Services, stub)
	}

	payload, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	createdAt := now
	if found && existing.CreatedAt > 0 {
		createdAt = existing.CreatedAt
	}

	return store.UpsertDailyProductivitySnapshot(ctx, db, store.DailyProductivitySnapshot{
		ProjectPath:        projectPath,
		Day:                dayStr,
		PayloadJSON:        string(payload),
		TotalActiveMinutes: rep.TotalActiveMinutes,
		Source:             "reflection",
		CreatedAt:          createdAt,
		UpdatedAt:          now,
	})
}

// serviceKey picks the string key a Service should match against the
// typed payload's `service` field. The reflection payload (§1.1) uses
// the repo's short name (e.g. "klyne"); Service.Repo carries the same
// value in practice. ProjectPath's basename is the fallback for
// payloads written before the writer was disciplined.
func serviceKey(svc productivity.Service) string {
	if r := strings.TrimSpace(svc.Repo); r != "" {
		return r
	}
	if p := strings.TrimSpace(svc.ProjectPath); p != "" {
		// last path segment
		if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
			return p[i+1:]
		}
		return p
	}
	return ""
}

// cloneCardPtr returns a heap-allocated copy of c so we don't leak
// slice aliasing between cardByService and the persisted Service.
func cloneCardPtr(c productivity.WhatWasDoneCard) *productivity.WhatWasDoneCard {
	out := c
	if c.Tier2.Details != nil {
		out.Tier2.Details = append([]productivity.WWDDetail(nil), c.Tier2.Details...)
	}
	if c.Tier1.PillCounts != nil {
		out.Tier1.PillCounts = make(map[string]int, len(c.Tier1.PillCounts))
		for k, v := range c.Tier1.PillCounts {
			out.Tier1.PillCounts[k] = v
		}
	}
	if c.Tier1.TopEvidence != nil {
		out.Tier1.TopEvidence = append([]string(nil), c.Tier1.TopEvidence...)
	}
	return &out
}

// Compile-time guard against silent contract drift on the route const.
var _ = api.RouteProductivityRecompose
