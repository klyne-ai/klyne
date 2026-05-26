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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
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

// Detail kinds for the typed "What was done" payload (spec §1.1 of
// docs/plan/2026-05-26-wwd-typed-cards.md). The enum is frozen — the
// productivity composer (Tier 1 derivation) pivots on these literal
// strings to build pill counts and the templated tldr.
const (
	DetailKindShipped      = "SHIPPED"
	DetailKindMajor        = "MAJOR"
	DetailKindFixed        = "FIXED"
	DetailKindDecision     = "DECISION"
	DetailKindInvestigated = "INVESTIGATED"
	DetailKindInProgress   = "IN_PROGRESS"
)

// validDetailKinds is the closed set the MCP tool + recorder validate
// against. Keep in sync with the §1.1 frozen enum.
var validDetailKinds = map[string]bool{
	DetailKindShipped:      true,
	DetailKindMajor:        true,
	DetailKindFixed:        true,
	DetailKindDecision:     true,
	DetailKindInvestigated: true,
	DetailKindInProgress:   true,
}

// detailKindOrder is the §1.2 Tier 2 render order. The recorder uses it
// to produce a stable, grep-friendly body_md rendering of body_json so
// legacy prose readers stay aligned with the dashboard ordering.
var detailKindOrder = []string{
	DetailKindShipped,
	DetailKindMajor,
	DetailKindFixed,
	DetailKindDecision,
	DetailKindInvestigated,
	DetailKindInProgress,
}

// detailWhenRe matches the HH:MM local-time format the §1.1 schema
// requires for WWDDetail.When. 00..29:00..59 is intentionally permissive
// on the hour (no model of "valid hour" — the schema only constrains
// digit shape).
var detailWhenRe = regexp.MustCompile(`^[0-2][0-9]:[0-5][0-9]$`)

// WWDPayload is the typed What-was-done payload the MCP tool receives
// on record_reflection. It mirrors store.WWDPayload exactly — keeping a
// worklog-package copy lets the recorder validate without importing the
// store package's parsing-side helpers and keeps the wire contract
// authored in one place per layer.
type WWDPayload struct {
	Service string      `json:"service"`
	Details []WWDDetail `json:"details"`
}

// WWDDetail is one typed detail in a WWDPayload. Field order + tags
// match store.WWDDetail so the wire JSON is identical on both sides.
type WWDDetail struct {
	Kind      string   `json:"kind"`
	When      string   `json:"when"`
	Text      string   `json:"text"`
	Evidence  []string `json:"evidence"`
	SessionID string   `json:"session_id,omitempty"`
}

// ValidateWWDPayload enforces the §1.1 invariants the MCP tool surfaces
// to its caller as a hard reject (not a silent strip):
//
//   - service is non-empty
//   - details is non-empty and ≤6 entries
//   - kind ∈ validDetailKinds
//   - when matches HH:MM (^[0-2][0-9]:[0-5][0-9]$)
//   - text ≤ 200 chars
//   - evidence is non-empty
//   - text containing "PR #<n>" must have that "#<n>" present in this
//     detail's own evidence — otherwise the host invented the ref and
//     the whole payload is rejected (stricter than the prose path's
//     deterministic strip, by design: typed details land in a
//     UI-facing component and a fabricated PR number would be more
//     load-bearing there).
//
// Returns a descriptive error naming the first detail index that failed
// so the AI host gets a precise diagnostic on the round-trip back.
func ValidateWWDPayload(p WWDPayload) error {
	if strings.TrimSpace(p.Service) == "" {
		return errors.New("worklog: wwd payload: service required")
	}
	if len(p.Details) == 0 {
		return errors.New("worklog: wwd payload: at least one detail required")
	}
	if len(p.Details) > 6 {
		return fmt.Errorf("worklog: wwd payload: at most 6 details per (day, service) bucket, got %d", len(p.Details))
	}
	for i, d := range p.Details {
		if !validDetailKinds[d.Kind] {
			return fmt.Errorf("worklog: wwd payload: detail %d kind %q not in {SHIPPED,MAJOR,FIXED,DECISION,INVESTIGATED,IN_PROGRESS}", i, d.Kind)
		}
		if !detailWhenRe.MatchString(d.When) {
			return fmt.Errorf("worklog: wwd payload: detail %d when %q must match HH:MM", i, d.When)
		}
		if strings.TrimSpace(d.Text) == "" {
			return fmt.Errorf("worklog: wwd payload: detail %d text empty", i)
		}
		if len(d.Text) > 200 {
			return fmt.Errorf("worklog: wwd payload: detail %d text %d chars > 200", i, len(d.Text))
		}
		if len(d.Evidence) == 0 {
			return fmt.Errorf("worklog: wwd payload: detail %d evidence empty (citation invariant)", i)
		}
		for j, ev := range d.Evidence {
			if strings.TrimSpace(ev) == "" {
				return fmt.Errorf("worklog: wwd payload: detail %d evidence[%d] blank", i, j)
			}
		}
		// Spec §5: a "PR #<n>" in text without that "#<n>" appearing in
		// THIS detail's evidence is the invented-id failure mode. Reject
		// rather than strip so the host has to fix the typed payload.
		if matches := prRefRe.FindAllStringSubmatch(d.Text, -1); len(matches) > 0 {
			haystack := strings.ToLower(strings.Join(d.Evidence, "\n"))
			for _, m := range matches {
				if len(m) < 2 {
					continue
				}
				num := m[1]
				backed := strings.Contains(haystack, "#"+num) ||
					strings.Contains(haystack, "pr "+num) ||
					strings.Contains(haystack, "pr#"+num) ||
					strings.Contains(haystack, "pr-"+num)
				if !backed {
					return fmt.Errorf("worklog: wwd payload: detail %d text references PR #%s not present in evidence", i, num)
				}
			}
		}
	}
	return nil
}

// renderWWDPayloadMarkdown turns a validated typed payload into a
// deterministic markdown bullet list so legacy prose readers (grep,
// older /worklog views, the iterative-reflection cursor's textual
// fallback) stay useful. The render groups by the §1.2 kind order and,
// within a kind, preserves the input order (the slash command emits
// details newest-first inside each kind already).
//
// Output shape:
//
//	## What was done — <service>
//
//	- [SHIPPED] 16:49 — wrote ~/.codex/hooks.json … (evidence: be8cc8c8, ~/.codex/hooks.json)
//	- [FIXED]   17:10 — applied .dot--live across 3 svelte files (evidence: 620a9551)
//	...
//
// The leading "## What was done — <service>" header gives the dashboard
// a stable anchor and the per-bullet `(evidence: …)` tail keeps the
// existing citation invariant visible to readers who can't parse
// body_json.
func renderWWDPayloadMarkdown(p WWDPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## What was done — %s\n\n", p.Service)
	// Group by kind, in §1.2 render order, preserving input order within
	// a kind. Unknown kinds are filtered upstream by ValidateWWDPayload.
	byKind := make(map[string][]WWDDetail, len(detailKindOrder))
	for _, d := range p.Details {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}
	for _, k := range detailKindOrder {
		for _, d := range byKind[k] {
			fmt.Fprintf(&b, "- [%s] %s — %s (evidence: %s)\n",
				d.Kind, d.When, d.Text, strings.Join(d.Evidence, ", "))
		}
	}
	return b.String()
}

// collectTypedEvidence flattens evidence ids across every detail in a
// WWDPayload, preserving first-seen order and de-duplicating across
// details. Detail.SessionID (when set) is prepended to the per-detail
// evidence list so the resulting evidence_entry_ids_json column still
// carries the originating stop_summary id even when the detail's own
// `evidence` list is exclusively commit SHAs / file paths / test names.
func collectTypedEvidence(p WWDPayload) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(p.Details))
	add := func(s string) {
		if strings.TrimSpace(s) == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, d := range p.Details {
		add(d.SessionID)
		for _, ev := range d.Evidence {
			add(ev)
		}
	}
	return out
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

// RecordReflectionTyped is the spec §1.1 typed-payload path the /klyne:
// reflect slash command exercises (one call per (day, service) bucket).
// It validates the WWDPayload against the frozen §1.1 schema, renders a
// deterministic body_md companion from the details so legacy prose
// readers stay useful, persists both columns through the extended
// store.InsertReflection, and advances the iterative-reflection cursor
// identically to the prose path.
//
// projectPath is the project the reflection rolls up under (the
// dashboard's "service" column is derived from this and from
// payload.Service — they should usually be the basename of projectPath
// but the slash command is free to pass any descriptive label).
//
// day is the COVERED calendar day (UTC). Zero falls back to now.Local()
// for back-compat — but the slash command must pass an explicit day.
//
// The returned store.Reflection carries the assembled BodyJSON / BodyMD
// fields so callers can echo the persisted shape back to the host.
func RecordReflectionTyped(ctx context.Context, db *store.DB, projectPath string, day time.Time, payload WWDPayload) (store.Reflection, error) {
	if strings.TrimSpace(projectPath) == "" {
		return store.Reflection{}, errors.New("worklog: project_path required")
	}
	if err := ValidateWWDPayload(payload); err != nil {
		return store.Reflection{}, err
	}

	// Roll worktrees up to the canonical main-repo path so a reflect
	// triggered from a worktree lands under the main repo's project
	// (matches the prose path's behaviour, see recordReflection).
	projectPath = projectpath.Canonical(projectPath)

	// Build the per-row evidence set: SessionID first (so the
	// stop_summary back-pointer is preserved at the head) then each
	// detail's evidence tokens. Dedupe across details.
	allEvidence := collectTypedEvidence(payload)
	if len(allEvidence) == 0 {
		// ValidateWWDPayload already guarantees per-detail evidence is
		// non-empty, but the store CHECK constraint requires the JSON
		// array to be non-trivial (len > 2). Defensive fallback when a
		// detail's evidence is exclusively whitespace strings — should
		// be unreachable in practice.
		return store.Reflection{}, errors.New("worklog: wwd payload: no usable evidence ids across details")
	}

	bodyJSONBytes, err := json.Marshal(payload)
	if err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: marshal wwd payload: %w", err)
	}
	bodyMD := renderWWDPayloadMarkdown(payload)

	now := time.Now()
	if day.IsZero() {
		day = now
	}
	dayLabel := day.Local().Format("2006-01-02")

	// Cursor advancement is identical to the prose path: take MAX(ts)
	// across the cited session_ids inside this day's local window.
	cursor, cerr := stopSummaryCursorForCitations(ctx, db, projectPath, day, allEvidence)
	if cerr != nil {
		cursor = 0
	}

	refl := store.Reflection{
		ID:                  fmt.Sprintf("ref-%s-%d", dayLabel, now.UnixNano()),
		TS:                  now.UnixMilli(),
		ProjectPath:         projectPath,
		Tier:                1,
		Title:               fmt.Sprintf("Daily reflection — %s", dayLabel),
		BodyMD:              bodyMD,
		BodyJSON:            string(bodyJSONBytes),
		EvidenceEntryIDs:    allEvidence,
		Importance:          7,
		SummarySource:       "ai",
		State:               "proposed",
		StateChangedAt:      now.UnixMilli(),
		StopSummaryCursorTS: cursor,
		Day:                 dayLabel,
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist typed reflection: %w", err)
	}
	return refl, nil
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

	// Pre-pass: per-insight dedupe of the cited evidence ids (intra-bullet)
	// and decide whether every insight cites the same set so we can collapse
	// repeated `(evidence: X)` parentheticals into a single trailing line
	// when the source is uniform (the common case: a day of work in one
	// session yields N bullets all citing the same session_id).
	dedupedInsightEv := make([][]string, len(insights))
	var firstSet []string
	sourcesDiffer := false
	for i, ins := range insights {
		dedupedInsightEv[i] = dedupeOrdered(ins.Evidence)
		if i == 0 {
			firstSet = dedupedInsightEv[i]
		} else if !equalStringSet(firstSet, dedupedInsightEv[i]) {
			sourcesDiffer = true
		}
	}

	var allEvidence []string
	seenEvidence := map[string]bool{}
	var body strings.Builder
	for i, ins := range insights {
		if strings.TrimSpace(ins.Text) == "" {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d has empty text", i)
		}
		if len(ins.Evidence) == 0 {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d missing evidence (citation invariant)", i)
		}
		// Global dedupe: prevents the JSON column from storing the same
		// session_id N times when N bullets all cite the same source.
		for _, ev := range dedupedInsightEv[i] {
			if !seenEvidence[ev] {
				seenEvidence[ev] = true
				allEvidence = append(allEvidence, ev)
			}
		}
		// Improvement 4 (spec §7.2 / §7.1 rule 2): the deterministic
		// unverified-artifact-ID guard. Any "PR #<n>" in the AI-authored
		// insight text not backed by the evidence allowlist is the
		// documented "PR #57" hallucination — strip it before persistence.
		allow := append(append([]string{}, dedupedInsightEv[i]...), gitEvidence...)
		text, _ := SanitizeArtifactIDs(ins.Text, allow)
		if sourcesDiffer {
			// Bullets cite different sources — keep per-bullet attribution.
			body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", text, strings.Join(dedupedInsightEv[i], ", ")))
		} else {
			// Uniform sources — render clean bullets; the footer below
			// carries the single shared attribution. Avoids the visual
			// noise of `(evidence: X)` repeated on every line.
			body.WriteString(fmt.Sprintf("- %s\n", text))
		}
	}
	if !sourcesDiffer && len(allEvidence) > 0 {
		body.WriteString(fmt.Sprintf("\n(evidence: %s)\n", strings.Join(allEvidence, ", ")))
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
	// Local-zone day matches the productivity dashboard's per-day bucketing
	// (handlers/productivity.go's localDaysInRange uses Local). Aligning
	// the title's date with the new `day` column means the title, the
	// queryable column, and the dashboard all agree.
	dayLabel := day.Local().Format("2006-01-02")

	// Iterative-reflection cursor (docs/features/iterative-reflection.md):
	// record the latest stop_summary.ts covered by this reflection so the
	// next /klyne:reflect run on the same day can skip everything ≤
	// this watermark and synthesize only the delta slice.
	// Computed from the cited session_ids within the day's local window —
	// the slash command passes those ids in Insight.Evidence; we look up
	// MAX(ts) and store it. Zero when no stop_summaries match (defensive;
	// the citation invariant above usually prevents this).
	cursor, err := stopSummaryCursorForCitations(ctx, db, projectPath, day, allEvidence)
	if err != nil {
		// A cursor lookup error doesn't justify failing the whole
		// reflection write — degrade to NULL cursor (the row still
		// records its work, just without the cursor advance, and the
		// next run will re-evaluate from the row's ts via the legacy
		// fallback in MaxReflectionCursorForDay).
		cursor = 0
	}

	refl := store.Reflection{
		// Date prefix is a debugging aid (grep-friendly); uniqueness comes from UnixNano.
		ID:                  fmt.Sprintf("ref-%s-%d", dayLabel, now.UnixNano()),
		TS:                  now.UnixMilli(),
		ProjectPath:         projectPath,
		Tier:                1, // daily
		Title:               fmt.Sprintf("Daily reflection — %s", dayLabel),
		BodyMD:              body.String(),
		EvidenceEntryIDs:    allEvidence,
		Importance:          7,
		SummarySource:       "ai",
		State:               "proposed",
		StateChangedAt:      now.UnixMilli(),
		StopSummaryCursorTS: cursor,
		// Day is the COVERED day (migration 022) — distinct from TS so
		// the per-day dashboard lookup keys on the day the work happened,
		// not the day this row was written.
		Day: dayLabel,
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist reflection: %w", err)
	}

	// Authoritative per-day snapshot for the productivity dashboard
	// (migration 021). Only runs on the substrate-aware path because the
	// plain RecordReflection caller doesn't have a Report to serialize.
	// Snapshot failure is logged-but-not-fatal: the reflection row is
	// already committed and the dashboard's lazy-backfill path will
	// reconstruct the day on next read if this one fails.
	if rep != nil {
		if err := writeProductivitySnapshot(ctx, db, projectPath, day, *rep); err != nil {
			log.Printf("worklog: snapshot write failed for %s %s: %v", projectPath, day.Local().Format("2006-01-02"), err)
		}
	}
	return refl, nil
}

// writeProductivitySnapshot persists the productivity Report for one
// (project, day) as a daily_productivity_snapshot row so the dashboard
// can render that day deterministically on reload. Source is fixed
// "reflection" — this is the authoritative write path; the handler's
// lazy backfill writes "live" rows that this call will overwrite.
//
// The Report's Day field is forced to the local-day string for day so
// the persisted payload matches the row's day column (defensive — the
// substrate builder uses a UTC-bucketed window but the dashboard reads
// in local zone).
func writeProductivitySnapshot(ctx context.Context, db *store.DB, projectPath string, day time.Time, rep productivity.Report) error {
	dayStr := day.Local().Format("2006-01-02")
	rep.Day = dayStr
	payload, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	now := time.Now().UnixMilli()
	return store.UpsertDailyProductivitySnapshot(ctx, db, store.DailyProductivitySnapshot{
		ProjectPath:        projectPath,
		Day:                dayStr,
		PayloadJSON:        string(payload),
		TotalActiveMinutes: rep.TotalActiveMinutes,
		Source:             "reflection",
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

// stopSummaryCursorForCitations returns MAX(stop_summaries.ts) for the
// cited session_ids inside the day's local window. The session-id space
// is what /klyne:reflect cites; a session can produce multiple
// stop_summary rows (one per turn), so we take the latest covered turn
// as the cursor watermark for the next iterative reflection.
//
// Empty citations → 0 (no advance). Citations that don't match any row
// in the day window → 0. Caller treats 0 as "leave cursor unset
// (NULL)" so the legacy ts-based fallback applies.
func stopSummaryCursorForCitations(ctx context.Context, db *store.DB, projectPath string, day time.Time, citations []string) (int64, error) {
	if len(citations) == 0 {
		return 0, nil
	}
	loc := day.Location()
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	// Build "?, ?, ?, ..." for the IN clause.
	placeholders := strings.Repeat(",?", len(citations))
	placeholders = placeholders[1:] // drop the leading comma
	q := fmt.Sprintf(`
SELECT COALESCE(MAX(ts), 0)
FROM stop_summaries
WHERE project_path = ?
  AND ts >= ? AND ts < ?
  AND session_id IN (%s)`, placeholders)

	args := make([]any, 0, 3+len(citations))
	args = append(args, projectPath, dayStart.UnixMilli(), dayEnd.UnixMilli())
	for _, c := range citations {
		args = append(args, c)
	}
	var maxTs int64
	if err := db.Read().QueryRowContext(ctx, q, args...).Scan(&maxTs); err != nil {
		return 0, fmt.Errorf("worklog: stop_summary cursor lookup: %w", err)
	}
	return maxTs, nil
}

// dedupeOrdered returns the input slice with duplicate strings removed,
// preserving the original first-seen order. Used to scrub repeated
// evidence ids both intra-bullet (when a caller cites the same session
// twice in one insight) and globally (when N bullets all cite the same
// source — the common "single-session day" pattern).
func dedupeOrdered(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// equalStringSet returns true when a and b contain the same elements
// (order-independent). Used to detect whether every insight in a
// reflection cites an identical evidence set, in which case we collapse
// per-bullet `(evidence: X)` parentheticals into one trailing line.
func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
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
