package productivity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// PersistLLMCompiledCard writes one Sonnet-compiled WhatWasDoneCard into
// the (projectPath, day) productivity_snapshot row. This is the
// persistence helper the second LLM pass (mcp__klyne__record_productivity_card)
// uses to land its output where the dashboard reads it.
//
// The card MUST already carry every Tier-1 mechanical field — the LLM
// tool layer is responsible for computing pill_counts / top_evidence /
// turn_count / commit_count from the details and only owns the
// tier1.tldr prose. This helper is a pure store-layer writer: it
// splices the card onto the matching Service in the existing snapshot
// payload (creating a Service stub if none exists yet for that
// service-on-day) and upserts. Idempotent.
//
// LLMCompiled is set true unconditionally — the helper exists ONLY for
// LLM-compiled writes; cold-start cards from ComposeWWD use the
// existing handlers.RecomposeWhatWasDone path which writes
// LLMCompiled=false.
//
// Source precedence: writes with source="reflection" because typed
// cards are derived from worklog_reflections rows (same as the cold-
// start composer). The DB upsert refuses to clobber a "reflection" row
// with a "live" row — that protection is unchanged.
func PersistLLMCompiledCard(
	ctx context.Context, db *store.DB, projectPath, dayStr string, card WhatWasDoneCard,
) error {
	if strings.TrimSpace(projectPath) == "" {
		return fmt.Errorf("productivity: persist llm card: project_path required")
	}
	if strings.TrimSpace(dayStr) == "" {
		return fmt.Errorf("productivity: persist llm card: day required")
	}
	if strings.TrimSpace(card.Service) == "" {
		return fmt.Errorf("productivity: persist llm card: card.service required")
	}

	card.LLMCompiled = true
	now := time.Now().UnixMilli()

	existing, found, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, dayStr)
	if err != nil {
		return fmt.Errorf("productivity: persist llm card: get snapshot: %w", err)
	}

	var rep Report
	if found && strings.TrimSpace(existing.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(existing.PayloadJSON), &rep); err != nil {
			// Defensive: corrupt persisted payload is overwritten.
			rep = Report{}
		}
	}
	if rep.Day == "" {
		rep.Day = dayStr
	}

	// Splice onto matching Service; create a stub when no service row
	// exists yet. Match on Service.Repo basename (the same key the
	// typed payload's `service` field uses, §1.1 / §1.2).
	matched := false
	for i := range rep.Services {
		svc := &rep.Services[i]
		key := serviceMatchKey(*svc)
		if key == card.Service {
			c := cloneCard(card)
			svc.WhatWasDone = &c
			matched = true
			break
		}
	}
	if !matched {
		c := cloneCard(card)
		stub := Service{
			Repo:        card.Service,
			ProjectPath: projectPath,
			Branches:    []Branch{},
			Risks:       []RiskSignal{},
			MergedPRs:   []MergedPR{},
			WhatWasDone: &c,
		}
		rep.Services = append(rep.Services, stub)
	}

	payload, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("productivity: persist llm card: marshal report: %w", err)
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

// serviceMatchKey is the service-key projection used to match an
// existing Service row to an incoming typed card. Repo first (the
// canonical short name); ProjectPath basename is the fallback for
// stub services created by an earlier recompose call.
func serviceMatchKey(svc Service) string {
	if r := strings.TrimSpace(svc.Repo); r != "" {
		return r
	}
	if p := strings.TrimSpace(svc.ProjectPath); p != "" {
		if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
			return p[i+1:]
		}
		return p
	}
	return ""
}

// cloneCard returns a deep copy so caller-supplied slices/maps don't
// alias the persisted snapshot's fields.
func cloneCard(c WhatWasDoneCard) WhatWasDoneCard {
	out := c
	if c.Tier2.Details != nil {
		out.Tier2.Details = append([]WWDDetail(nil), c.Tier2.Details...)
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
	return out
}
