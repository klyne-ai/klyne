// Package-level helper for the record_reflection MCP tool.
//
// RecordReflection persists a synthesized DAILY reflection (tier=1)
// produced by the AI host (Claude / Codex via its slash command).
// The host buckets pending entries by date and calls this once per
// date so a single /klyne:reflect run can catch up across N days.
//
// The citation invariant is enforced here AND in store.InsertReflection
// — we keep the AI-facing check tight so the host gets an immediate,
// descriptive error before the row is even attempted.

package worklog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/projectpath"
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
// day is the calendar day the reflection covers. A zero day defaults
// to time.Now() — convenient for the legacy single-day call path but
// every new caller (the /klyne:reflect slash command in particular)
// should pass an explicit day so multi-day catch-up works.
//
// Called by the record_reflection MCP tool (which is invoked by Claude
// after the /klyne:reflect slash command produces insights).
func RecordReflection(ctx context.Context, db *store.DB, projectPath string, day time.Time, insights []Insight) (store.Reflection, error) {
	if strings.TrimSpace(projectPath) == "" {
		return store.Reflection{}, errors.New("worklog: project_path required")
	}
	// Roll worktrees up to the canonical main-repo path so reflections
	// triggered from a worktree appear under that repo's project, not
	// as a sibling card on the worklog page.
	projectPath = projectpath.Canonical(projectPath)
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
	if day.IsZero() {
		day = now
	}
	dayLabel := day.UTC().Format("2006-01-02")
	refl := store.Reflection{
		// Date prefix is a debugging aid (grep-friendly); uniqueness comes from UnixNano.
		ID:               fmt.Sprintf("ref-%s-%d", dayLabel, now.UnixNano()),
		TS:               now.UnixMilli(),
		ProjectPath:      projectPath,
		Tier:             1, // daily
		Title:            fmt.Sprintf("Daily reflection — %s", dayLabel),
		BodyMD:           body.String(),
		EvidenceEntryIDs: allEvidence,
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   now.UnixMilli(),
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist reflection: %w", err)
	}
	return refl, nil
}
