package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// RecordReflectionInput is the MCP-facing input schema. Insights MUST
// each carry at least one evidence id (session_id from the proposer
// output); the underlying worklog helper rejects empty-evidence
// insights up-front.
//
// Day is the YYYY-MM-DD (UTC) calendar day the reflection covers.
// Strongly recommended — omitting it files the reflection under
// today's UTC date, which is wrong for multi-day catch-up (the
// reason Day exists). Empty is allowed for back-compat with the
// legacy single-day call shape.
//
// BodyJSON is the typed "What was done" service-card payload (spec §1.1
// of docs/plan/2026-05-26-wwd-typed-cards.md). When set, the call
// follows the typed path — the payload is validated against the §1.1
// frozen schema (kind enum, HH:MM when, ≤200-char text, non-empty
// evidence, PR-ref must be backed by detail evidence), the recorder
// renders a deterministic body_md companion for legacy readers, and
// both columns land in worklog_reflections. When omitted, Insights is
// required and the call follows the existing prose-only path.
type RecordReflectionInput struct {
	ProjectPath string              `json:"project_path" jsonschema:"absolute project path"`
	Day         string              `json:"day,omitempty" jsonschema:"calendar day this reflection covers, YYYY-MM-DD (UTC); strongly recommended (omitting it files under today UTC which is wrong for multi-day catch-up)"`
	Insights    []worklog.Insight   `json:"insights,omitempty" jsonschema:"synthesized insights (prose path) — each must cite at least one entry session_id. Omit when sending body_json."`
	BodyJSON    *worklog.WWDPayload `json:"body_json,omitempty" jsonschema:"typed What-was-done payload (spec §1.1) — one service per call, ≤6 details with kind∈{SHIPPED,MAJOR,FIXED,DECISION,INVESTIGATED,IN_PROGRESS}, HH:MM when, ≤200-char text, non-empty evidence drawn literally from source stop_summaries"`
}

// RecordReflectionOutput surfaces the persisted id and a citation count
// so the AI host can confirm the round-trip back to the user.
type RecordReflectionOutput struct {
	ReflectionID  string `json:"reflection_id"`
	EvidenceCount int    `json:"evidence_count"`
}

func handleRecordReflection(ctx context.Context, db *store.DB, in RecordReflectionInput) (*RecordReflectionOutput, error) {
	var day time.Time
	if in.Day != "" {
		parsed, err := time.ParseInLocation("2006-01-02", in.Day, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("record_reflection: bad day %q (want YYYY-MM-DD): %w", in.Day, err)
		}
		day = parsed
	}

	// Typed-payload path (spec §1.1): when body_json is supplied the
	// call lands a typed "What was done" service card. The prose path
	// is bypassed because the dashboard's Tier 1 composer reads
	// body_json directly — running the substrate enrichment for a
	// typed row would double-render the same git facts the slash
	// command already cited as evidence tokens. The §7.1 PR-ref guard
	// runs INSIDE ValidateWWDPayload (rejecting unbacked refs rather
	// than stripping them) — typed details face a UI consumer where
	// an invented PR number would be more load-bearing than in prose.
	if in.BodyJSON != nil {
		refl, err := worklog.RecordReflectionTyped(ctx, db, in.ProjectPath, day, *in.BodyJSON)
		if err != nil {
			return nil, err
		}
		return &RecordReflectionOutput{ReflectionID: refl.ID, EvidenceCount: len(refl.EvidenceEntryIDs)}, nil
	}

	// Prose path (legacy): Layer-2 enrichment (spec §7.2). Build the
	// deterministic git substrate for this project+day and record the
	// reflection through the substrate-aware path so it carries the
	// open-loops / shipped / cross-project sections and the §7.1
	// PR-ref guard runs against real commit evidence. A non-git
	// project yields an empty substrate and the call degrades to the
	// plain RecordReflection behaviour (D8).
	since, until := reflectionDayWindow(day)
	rep, err := worklog.BuildProjectSubstrate(in.ProjectPath, since, until)
	if err != nil {
		// Substrate build must never block the reflection — degrade.
		rep = productivity.Report{}
	}
	refl, err := worklog.RecordReflectionWithSubstrate(ctx, db, in.ProjectPath, day, in.Insights, rep)
	if err != nil {
		return nil, err
	}
	return &RecordReflectionOutput{ReflectionID: refl.ID, EvidenceCount: len(refl.EvidenceEntryIDs)}, nil
}

// reflectionDayWindow returns the [00:00, next-00:00) UTC window for the
// calendar day a reflection covers. A zero day (legacy single-day call)
// falls back to a 24h window ending now so recent git activity is still
// grounded.
func reflectionDayWindow(day time.Time) (since, until time.Time) {
	if day.IsZero() {
		until = time.Now()
		return until.Add(-24 * time.Hour), until
	}
	d := day.UTC()
	since = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return since, since.Add(24 * time.Hour)
}

// HandleRecordReflection is the MCP entry-point. The citation invariant
// is enforced inside worklog.RecordReflection; this thin shell just
// adapts errors to the tool-log surface.
func HandleRecordReflection(ctx context.Context, _ *mcp.CallToolRequest, in RecordReflectionInput) (*mcp.CallToolResult, RecordReflectionOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, RecordReflectionOutput{}, fmt.Errorf("record_reflection: open db: %w", err)
	}
	defer db.Close()
	out, err := handleRecordReflection(ctx, db, in)
	if err != nil {
		return nil, RecordReflectionOutput{}, err
	}
	summary := fmt.Sprintf("record_reflection: stored %s with %d evidence entries", out.ReflectionID, out.EvidenceCount)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
