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
type RecordProductivityCardInput struct {
	ProjectPath string            `json:"project_path" jsonschema:"absolute project path"`
	Day         string            `json:"day"          jsonschema:"local YYYY-MM-DD this card covers"`
	Service     string            `json:"service"      jsonschema:"per-§1.1 service key — typically the project basename"`
	Tier1Tldr   string            `json:"tier1_tldr"   jsonschema:"≤120 chars, one verb-led sentence summarising the day's biggest outcome for this service"`
	Details     []store.WWDDetail `json:"details"      jsonschema:"ordered Tier 2 details (SHIPPED → MAJOR → FIXED → DECISION → INVESTIGATED → IN_PROGRESS, newest-first within kind); each carries kind/when/text/evidence/session_id"`
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

// handleRecordProductivityCard is the pure-Go inner handler.
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
	tldr := strings.TrimSpace(in.Tier1Tldr)
	if tldr == "" {
		return nil, fmt.Errorf("record_productivity_card: tier1_tldr required")
	}
	if len(tldr) > tldrMaxLen {
		return nil, fmt.Errorf("record_productivity_card: tier1_tldr %d chars > %d", len(tldr), tldrMaxLen)
	}
	if len(in.Details) == 0 {
		return nil, fmt.Errorf("record_productivity_card: at least one detail required")
	}

	// Bridge to the worklog validator so the §1.1 schema rules + §5
	// PR-#<n> backing guard fire identically to the first LLM pass —
	// any drift between the two tools would silently let the second
	// pass land payloads the first pass would reject.
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

	// Build the productivity.WhatWasDoneCard. Convert the store.WWDDetail
	// inputs to productivity.WWDDetail (same field shape, different
	// package boundary), compute the mechanical Tier 1, then overwrite
	// TLDR with the LLM-authored string.
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
