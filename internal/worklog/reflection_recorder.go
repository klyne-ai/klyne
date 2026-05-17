// Package-level helper for the record_reflection MCP tool.
//
// RecordReflection persists a synthesized weekly reflection produced by
// the AI host (Claude / Codex via its slash command). The citation
// invariant is enforced here AND in store.InsertReflection — we keep
// the AI-facing check tight so the host gets an immediate, descriptive
// error before the row is even attempted.

package worklog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// Insight is one synthesized statement plus the entry IDs it cites.
// Citation invariant: Evidence must be non-empty.
type Insight struct {
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
}

// RecordReflection persists a synthesized reflection. Enforces the
// citation invariant: every Insight must cite at least one entry id,
// and the overall reflection must cite at least one entry id total.
// Returns the persisted store.Reflection so callers can echo the id back.
//
// Called by the record_reflection MCP tool (which is invoked by Claude
// after the /klyne:reflect slash command produces insights).
func RecordReflection(ctx context.Context, db *store.DB, projectPath string, insights []Insight) (store.Reflection, error) {
	if strings.TrimSpace(projectPath) == "" {
		return store.Reflection{}, errors.New("worklog: project_path required")
	}
	if len(insights) == 0 {
		return store.Reflection{}, errors.New("worklog: at least one insight required")
	}
	var allEvidence []string
	var body strings.Builder
	for i, ins := range insights {
		if strings.TrimSpace(ins.Text) == "" {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d has empty text", i)
		}
		if len(ins.Evidence) == 0 {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d missing evidence (citation invariant)", i)
		}
		allEvidence = append(allEvidence, ins.Evidence...)
		body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", ins.Text, strings.Join(ins.Evidence, ", ")))
	}
	now := time.Now()
	refl := store.Reflection{
		ID:               fmt.Sprintf("ref-%d", now.UnixNano()),
		TS:               now.UnixMilli(),
		ProjectPath:      projectPath,
		Tier:             2, // weekly
		Title:            fmt.Sprintf("Weekly reflection — %s", IsoWeek(now)),
		BodyMD:           body.String(),
		EvidenceEntryIDs: allEvidence,
		Importance:       7,
		SummarySource:    "ai", // synthesized by the AI host that called record_reflection
		State:            "proposed",
		StateChangedAt:   now.UnixMilli(),
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist reflection: %w", err)
	}
	return refl, nil
}
