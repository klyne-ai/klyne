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
//
// RecordReflectionWithSubstrate is the spec §7.2 Layer-2 upgrade: it
// additionally consumes the deterministic internal/productivity Report
// to (a) widen the §7.1-rule-2 PR-ref guard's evidence allowlist with
// real commit subjects + branch names and (b) append the git-grounded
// sections (open loops, shipped ledger, cross-project thread).

package worklog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
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
	return recordReflection(ctx, db, projectPath, day, insights, nil)
}

// RecordReflectionWithSubstrate is the spec §7.2 Layer-2 enrichment
// path. Beyond everything RecordReflection does it consumes the
// deterministic productivity Report so the persisted reflection is
// git-grounded and salience-correct:
//
//   - Improvement 4: the unverified-artifact-ID guard's evidence
//     allowlist is widened with the matched repo's real commit subjects
//     and branch names — a "PR #<n>" backed by an actual commit message
//     survives, an invented one is stripped.
//   - Improvements 1/3/5/7: GitSubstrateSections (open loops, shipped
//     ledger with commit-dated lines, cross-project initiative thread)
//     are appended to body_md after the AI-authored insight bullets.
//
// Graceful degradation (D8): when the report has no Service for this
// project (it is not a git repo, or had no in-window activity) the
// behaviour is identical to RecordReflection — no git sections, no
// error. The existing reflection flow is never broken.
func RecordReflectionWithSubstrate(ctx context.Context, db *store.DB, projectPath string, day time.Time, insights []Insight, rep productivity.Report) (store.Reflection, error) {
	return recordReflection(ctx, db, projectPath, day, insights, &rep)
}

// recordReflection is the shared core. rep is nil for the plain path and
// non-nil for the substrate-enriched path.
func recordReflection(ctx context.Context, db *store.DB, projectPath string, day time.Time, insights []Insight, rep *productivity.Report) (store.Reflection, error) {
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

	// Build the §7.1-rule-2 guard's evidence allowlist. For the plain
	// path it is just each insight's cited ids. For the substrate path it
	// is widened with the matched repo's real commit subjects + branch
	// names + short SHAs so a genuinely-backed artifact ref survives.
	gitEvidence := gitEvidenceFor(rep, projectPath)

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
		// Improvement 4 (spec §7.2 / §7.1 rule 2): the deterministic
		// unverified-artifact-ID guard. Any "PR #<n>" in the AI-authored
		// insight text not backed by the evidence allowlist is the
		// documented "PR #57" hallucination — strip it before persistence.
		allow := append(append([]string{}, ins.Evidence...), gitEvidence...)
		text, _ := SanitizeArtifactIDs(ins.Text, allow)
		body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", text, strings.Join(ins.Evidence, ", ")))
	}

	// Improvements 1/3/5/7: append the deterministic git-grounded
	// sections. No-op (nil sections) for the plain path or a non-git
	// project — graceful degradation (D8).
	if rep != nil {
		for _, sec := range GitSubstrateSections(*rep, projectPath) {
			body.WriteString("\n" + sec + "\n")
		}
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

// gitEvidenceFor returns the substrate evidence allowlist for projectPath
// — every commit subject, short SHA, branch name and ticket id of the
// matched Service. Empty for the plain path / a non-git project. This is
// the §7.1 input allowlist the PR-ref guard checks against.
func gitEvidenceFor(rep *productivity.Report, projectPath string) []string {
	if rep == nil {
		return nil
	}
	svc := findService(*rep, projectPath)
	if svc == nil {
		return nil
	}
	var out []string
	for _, b := range svc.Branches {
		out = append(out, b.Name)
		if b.TicketID != "" {
			out = append(out, b.TicketID)
		}
		for _, c := range b.Commits {
			out = append(out, c.Subject, c.SHA)
		}
	}
	return out
}
