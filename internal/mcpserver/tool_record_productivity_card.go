package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// tldrMaxLen is the §1.2 / plan-doc Tier-1 prose budget: ≤120 chars,
// one verb-led sentence summarising the day's biggest outcome for a
// service. The tool rejects anything over the cap — drift here would
// blow out the dashboard's headline row.
const tldrMaxLen = 120

// RecordProductivityCardInput is the second-LLM-pass tool input. The
// LLM owns only `tier1_tldr` and the prose inside `details`; every
// other Tier 1 field (pill_counts / top_evidence / turn_count /
// commit_count) is computed deterministically from `details` by Go
// inside the tool — the LLM never sets them.
//
// Validation:
//   - tier1_tldr non-empty, ≤120 chars
//   - details non-empty, each detail's evidence non-empty (§5 citation invariant)
//   - same PR-#<n> backing rule as tool_record_reflection.go
//
// On success the tool builds the productivity.WhatWasDoneCard, runs
// productivity.BuildMechanicalTier1 over the details to populate the
// mechanical Tier 1 fields, overwrites tier1.tldr with the LLM-authored
// string, sets LLMCompiled=true, and persists via
// productivity.PersistLLMCompiledCard.
// RecordProductivityCardInput accepts EITHER:
//
//   - v2 narrative shape (preferred): service_summary + cards[] with
//     kind/ticket_id/title/body (markdown prose, ≤1200 chars) and typed
//     refs[]. The dashboard renders this as stat tiles + per-ticket
//     cards grouped by section (Features shipped / Bugs fixed /
//     Decisions / Investigated · no fix landed). Optional followup line
//     surfaces an open question for tomorrow.
//
//   - v1 legacy shape: tier1_tldr + details[] with kind/when:HH:MM/
//     text≤200/evidence. Retained for back-compat with the older
//     productivity-sync prompt.
//
// At least one of Cards or Details must be non-empty. When Cards is
// present, Tier1Tldr / Details may both be omitted.
type RecordProductivityCardInput struct {
	ProjectPath    string            `json:"project_path"              jsonschema:"absolute project path"`
	Day            string            `json:"day"                       jsonschema:"local YYYY-MM-DD this card covers"`
	Service        string            `json:"service"                   jsonschema:"per-§1.1 service key — typically the project basename"`
	ServiceSummary string            `json:"service_summary,omitempty" jsonschema:"v2: 1-2 sentence paragraph capturing the day's theme for this service (≤600 chars)"`
	Cards          []store.WWDCard   `json:"cards,omitempty"           jsonschema:"v2 narrative cards: one per (ticket, kind). title is ≤160-char outcome-led headline (NOT verb-led); body is ≤1200-char markdown prose narrative (2-4 sentences); refs[] are typed tokens {type:file|branch|pr|commit|ticket|test|session, text:literal}"`
	Followup       string            `json:"followup,omitempty"        jsonschema:"v2: optional 1-sentence open question / edge case the user should remember tomorrow (≤400 chars)"`
	Tier1Tldr      string            `json:"tier1_tldr,omitempty"      jsonschema:"v1 legacy: ≤120 chars, one verb-led sentence summarising the day's biggest outcome for this service"`
	Details        []store.WWDDetail `json:"details,omitempty"         jsonschema:"v1 legacy: ordered Tier 2 details (SHIPPED → MAJOR → FIXED → DECISION → INVESTIGATED → IN_PROGRESS); each carries kind/when/text/evidence/session_id"`
}

// RecordProductivityCardOutput surfaces the persisted shape so the LLM
// can confirm the round-trip back to the user. DetailCount echoes the
// number of details persisted; ShaCount mirrors the Tier-1
// commit_count Go computed from the details (helpful for the LLM's
// follow-up summary line).
type RecordProductivityCardOutput struct {
	ProjectPath string `json:"project_path"`
	Day         string `json:"day"`
	Service     string `json:"service"`
	DetailCount int    `json:"detail_count"`
	ShaCount    int    `json:"sha_count"`
}

// handleRecordProductivityCard is the pure-Go inner handler. Branches on
// the input shape: v2 narrative (Cards present) or v1 legacy (Details).
func handleRecordProductivityCard(ctx context.Context, db *store.DB, in RecordProductivityCardInput) (*RecordProductivityCardOutput, error) {
	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		return nil, fmt.Errorf("record_productivity_card: project_path required")
	}
	dayStr := strings.TrimSpace(in.Day)
	if dayStr == "" {
		return nil, fmt.Errorf("record_productivity_card: day required")
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		return nil, fmt.Errorf("record_productivity_card: bad day %q (want YYYY-MM-DD): %w", dayStr, err)
	}
	service := strings.TrimSpace(in.Service)
	if service == "" {
		return nil, fmt.Errorf("record_productivity_card: service required")
	}

	// v2 narrative path — preferred for the redesigned dashboard layout.
	if len(in.Cards) > 0 {
		return handleNarrativeCard(ctx, db, projectPath, dayStr, service, in)
	}

	// v1 legacy path — back-compat for the older productivity-sync prompt.
	tldr := strings.TrimSpace(in.Tier1Tldr)
	if tldr == "" {
		return nil, fmt.Errorf("record_productivity_card: tier1_tldr required (v1 path) or cards required (v2 path)")
	}
	if len(tldr) > tldrMaxLen {
		return nil, fmt.Errorf("record_productivity_card: tier1_tldr %d chars > %d", len(tldr), tldrMaxLen)
	}
	if len(in.Details) == 0 {
		return nil, fmt.Errorf("record_productivity_card: at least one detail required (v1 path)")
	}

	worklogPayload := worklog.WWDPayload{Service: service, Details: make([]worklog.WWDDetail, 0, len(in.Details))}
	for _, d := range in.Details {
		worklogPayload.Details = append(worklogPayload.Details, worklog.WWDDetail{
			Kind:      d.Kind,
			When:      d.When,
			Text:      d.Text,
			Evidence:  append([]string(nil), d.Evidence...),
			SessionID: d.SessionID,
		})
	}
	if err := worklog.ValidateWWDPayload(worklogPayload); err != nil {
		return nil, err
	}

	prodDetails := make([]productivity.WWDDetail, 0, len(in.Details))
	for _, d := range in.Details {
		prodDetails = append(prodDetails, productivity.WWDDetail{
			Kind:      d.Kind,
			When:      d.When,
			Text:      d.Text,
			Evidence:  append([]string(nil), d.Evidence...),
			SessionID: d.SessionID,
		})
	}
	tier1 := productivity.BuildMechanicalTier1(prodDetails)
	tier1.TLDR = tldr

	card := productivity.WhatWasDoneCard{
		Service:     service,
		Tier1:       tier1,
		LLMCompiled: true,
	}
	card.Tier2.Details = prodDetails

	if err := productivity.PersistLLMCompiledCard(ctx, db, projectPath, dayStr, card); err != nil {
		return nil, fmt.Errorf("record_productivity_card: persist: %w", err)
	}

	return &RecordProductivityCardOutput{
		ProjectPath: projectPath,
		Day:         dayStr,
		Service:     service,
		DetailCount: len(prodDetails),
		ShaCount:    tier1.CommitCount,
	}, nil
}

// handleNarrativeCard is the v2 path: validates the narrative card
// payload via the worklog validator, computes stats deterministically
// from the cards, builds a productivity.WhatWasDoneCard with the
// Narrative field populated, and persists.
func handleNarrativeCard(ctx context.Context, db *store.DB, projectPath, dayStr, service string, in RecordProductivityCardInput) (*RecordProductivityCardOutput, error) {
	// Bridge to the worklog validator so v2 card rules + the PR-ref
	// invariant fire identically across the two write tools.
	worklogPayload := worklog.WWDPayload{
		Service:        service,
		ServiceSummary: strings.TrimSpace(in.ServiceSummary),
		Followup:       strings.TrimSpace(in.Followup),
		Cards:          make([]worklog.WWDCard, 0, len(in.Cards)),
	}
	for _, c := range in.Cards {
		wc := worklog.WWDCard{
			Kind:     c.Kind,
			TicketID: c.TicketID,
			Title:    c.Title,
			Body:     c.Body,
			Refs:     make([]worklog.WWDRef, 0, len(c.Refs)),
		}
		for _, r := range c.Refs {
			wc.Refs = append(wc.Refs, worklog.WWDRef{Type: r.Type, Text: r.Text})
		}
		worklogPayload.Cards = append(worklogPayload.Cards, wc)
	}
	if err := worklog.ValidateWWDPayload(worklogPayload); err != nil {
		return nil, err
	}

	// Convert to productivity types and compute stats from card kinds.
	prodCards := make([]productivity.WWDCard, 0, len(in.Cards))
	stats := productivity.WWDStats{}
	for _, c := range in.Cards {
		prodCards = append(prodCards, productivity.WWDCard{
			Kind: c.Kind, TicketID: c.TicketID, Title: c.Title, Body: c.Body,
			Refs: copyProdRefs(c.Refs),
		})
		switch c.Kind {
		case "SHIPPED", "MAJOR":
			stats.Shipped++
		case "FIXED":
			stats.Fixed++
		case "DECISION":
			stats.Decisions++
		case "INVESTIGATED":
			stats.Investigated++
		case "IN_PROGRESS":
			stats.InProgress++
		}
	}

	// Build a synthetic Tier1 so legacy dashboard code paths (chip row,
	// counts header) still render. The TLDR is the service summary
	// truncated to tldrMaxLen.
	tier1 := productivity.WWDTier1{
		PillCounts:  map[string]int{},
		TopEvidence: collectNarrativeTopEvidence(in.Cards),
	}
	if stats.Shipped > 0 {
		tier1.PillCounts["shipped"] = stats.Shipped
	}
	if stats.Fixed > 0 {
		tier1.PillCounts["fixed"] = stats.Fixed
	}
	if stats.Decisions > 0 {
		tier1.PillCounts["decisions"] = stats.Decisions
	}
	if stats.Investigated > 0 {
		tier1.PillCounts["investigated"] = stats.Investigated
	}
	if stats.InProgress > 0 {
		tier1.PillCounts["in_progress"] = stats.InProgress
	}
	summary := strings.TrimSpace(in.ServiceSummary)
	if summary == "" {
		summary = synthesizeTier1TLDR(in.Cards)
	}
	tier1.TLDR = summary
	if len(tier1.TLDR) > tldrMaxLen {
		// Truncate at a word boundary so the legacy headline stays
		// readable even for long service-summary paragraphs.
		cut := tier1.TLDR[:tldrMaxLen]
		if idx := strings.LastIndex(cut, " "); idx > tldrMaxLen/2 {
			cut = cut[:idx]
		}
		tier1.TLDR = strings.TrimRight(cut, " ,.;:") + "…"
	}
	// Best-effort commit/turn counts derived from the cards' refs.
	for _, c := range in.Cards {
		for _, r := range c.Refs {
			if r.Type == "commit" {
				tier1.CommitCount++
			}
		}
	}

	card := productivity.WhatWasDoneCard{
		Service:     service,
		Tier1:       tier1,
		LLMCompiled: true,
		Narrative: &productivity.WWDNarrative{
			Summary:  summary,
			Stats:    stats,
			Cards:    prodCards,
			Followup: strings.TrimSpace(in.Followup),
		},
	}
	// Tier2.Details is left empty in v2 — UI prefers Narrative.

	if err := productivity.PersistLLMCompiledCard(ctx, db, projectPath, dayStr, card); err != nil {
		return nil, fmt.Errorf("record_productivity_card: persist: %w", err)
	}

	return &RecordProductivityCardOutput{
		ProjectPath: projectPath,
		Day:         dayStr,
		Service:     service,
		DetailCount: len(prodCards),
		ShaCount:    tier1.CommitCount,
	}, nil
}

func copyProdRefs(refs []store.WWDRef) []productivity.WWDRef {
	out := make([]productivity.WWDRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, productivity.WWDRef{Type: r.Type, Text: r.Text})
	}
	return out
}

// collectNarrativeTopEvidence returns the first three commit-sha refs
// across the narrative cards. Mirrors BuildMechanicalTier1's evidence
// promotion so the legacy chip row above the card still surfaces commit
// hashes.
func collectNarrativeTopEvidence(cards []store.WWDCard) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, c := range cards {
		for _, r := range c.Refs {
			if r.Type != "commit" {
				continue
			}
			if seen[r.Text] {
				continue
			}
			seen[r.Text] = true
			out = append(out, r.Text)
			if len(out) >= 3 {
				return out
			}
		}
	}
	return out
}

// synthesizeTier1TLDR templates a fallback headline when the writer
// omitted ServiceSummary. Picks the first SHIPPED card's title.
func synthesizeTier1TLDR(cards []store.WWDCard) string {
	for _, c := range cards {
		if c.Kind == "SHIPPED" {
			return c.Title
		}
	}
	if len(cards) > 0 {
		return cards[0].Title
	}
	return ""
}

// HandleRecordProductivityCard is the MCP transport adaptor.
func HandleRecordProductivityCard(ctx context.Context, _ *mcp.CallToolRequest, in RecordProductivityCardInput) (*mcp.CallToolResult, RecordProductivityCardOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, RecordProductivityCardOutput{}, fmt.Errorf("record_productivity_card: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck
	out, err := handleRecordProductivityCard(ctx, db, in)
	if err != nil {
		return nil, RecordProductivityCardOutput{}, err
	}
	summary := fmt.Sprintf("record_productivity_card: persisted %s/%s (%d details, %d commit shas)",
		out.Service, out.Day, out.DetailCount, out.ShaCount)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
