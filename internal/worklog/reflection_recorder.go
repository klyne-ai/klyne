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

// WWDPayload is the typed What-was-done payload the MCP tool receives.
// Mirrors store.WWDPayload exactly — see that type's doc-comment for
// the v1/v2 generation distinction (legacy Details vs new Cards). The
// recorder accepts either or both; ValidateWWDPayload picks the right
// rule set per generation.
type WWDPayload struct {
	Service        string      `json:"service"`
	ServiceSummary string      `json:"service_summary,omitempty"`
	Stats          *WWDStats   `json:"stats,omitempty"`
	Cards          []WWDCard   `json:"cards,omitempty"`
	Followup       string      `json:"followup,omitempty"`
	// Legacy v1 — kept for back-compat. New writers populate Cards.
	Details []WWDDetail `json:"details,omitempty"`
}

// WWDStats are the per-kind counts shown as stat tiles on the dashboard.
// The composer can compute these from Cards mechanically, but the
// recorder accepts an explicit value when the writer wants to surface
// custom phrasing (e.g. lumping MAJOR into "shipped" for UX).
type WWDStats struct {
	Shipped      int `json:"shipped"`
	Fixed        int `json:"fixed"`
	Decisions    int `json:"decisions"`
	Investigated int `json:"investigated"`
	InProgress   int `json:"in_progress,omitempty"`
}

// WWDCard is one narrative card on the dashboard. ONE card per
// (ticket, kind) — same ticket can appear under SHIPPED AND FIXED. Body
// is markdown prose (2-4 sentences); refs are typed tokens the UI styles
// by kind.
type WWDCard struct {
	Kind     string   `json:"kind"`
	TicketID string   `json:"ticket_id,omitempty"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Refs     []WWDRef `json:"refs,omitempty"`
}

// WWDRef is a typed reference token shown in the card footer.
type WWDRef struct {
	Type string `json:"type"` // file|branch|pr|commit|ticket|test|session
	Text string `json:"text"`
}

// WWDDetail is the legacy v1 detail shape. Retained for back-compat.
type WWDDetail struct {
	Kind      string   `json:"kind"`
	When      string   `json:"when"`
	Text      string   `json:"text"`
	Evidence  []string `json:"evidence"`
	SessionID string   `json:"session_id,omitempty"`
}

// validRefTypes is the closed enum of WWDRef.Type values. Anything else
// is rejected so the UI can render every ref it sees.
var validRefTypes = map[string]bool{
	"file":    true,
	"branch":  true,
	"pr":      true,
	"commit":  true,
	"ticket":  true,
	"test":    true,
	"session": true,
}

// validateWWDCards checks the v2 narrative-card payload. Independent
// from the legacy Details path so each generation can evolve.
func validateWWDCards(p WWDPayload) error {
	if len(p.Cards) == 0 {
		return errors.New("worklog: wwd payload: at least one card required")
	}
	if len(p.Cards) > 20 {
		return fmt.Errorf("worklog: wwd payload: at most 20 cards per service, got %d", len(p.Cards))
	}
	if len(p.ServiceSummary) > 600 {
		return fmt.Errorf("worklog: wwd payload: service_summary %d chars > 600", len(p.ServiceSummary))
	}
	if len(p.Followup) > 400 {
		return fmt.Errorf("worklog: wwd payload: followup %d chars > 400", len(p.Followup))
	}
	for i, c := range p.Cards {
		if !validDetailKinds[c.Kind] {
			return fmt.Errorf("worklog: wwd payload: card %d kind %q not in {SHIPPED,MAJOR,FIXED,DECISION,INVESTIGATED,IN_PROGRESS}", i, c.Kind)
		}
		if strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("worklog: wwd payload: card %d title empty", i)
		}
		if len(c.Title) > 160 {
			return fmt.Errorf("worklog: wwd payload: card %d title %d chars > 160", i, len(c.Title))
		}
		if strings.TrimSpace(c.Body) == "" {
			return fmt.Errorf("worklog: wwd payload: card %d body empty", i)
		}
		if len(c.Body) > 1200 {
			return fmt.Errorf("worklog: wwd payload: card %d body %d chars > 1200", i, len(c.Body))
		}
		for j, r := range c.Refs {
			if !validRefTypes[r.Type] {
				return fmt.Errorf("worklog: wwd payload: card %d ref[%d] type %q not in {file,branch,pr,commit,ticket,test,session}", i, j, r.Type)
			}
			if strings.TrimSpace(r.Text) == "" {
				return fmt.Errorf("worklog: wwd payload: card %d ref[%d] text empty", i, j)
			}
		}
		// PR-ref invariant: any "PR #<n>" mentioned in body MUST appear in
		// some ref with type=pr (or in the title). Prevents fabricated
		// PR numbers on the dashboard.
		if matches := prRefRe.FindAllStringSubmatch(c.Body+" "+c.Title, -1); len(matches) > 0 {
			haystack := strings.ToLower(c.Title + "\n")
			for _, r := range c.Refs {
				haystack += strings.ToLower(r.Text) + "\n"
			}
			for _, m := range matches {
				if len(m) < 2 {
					continue
				}
				num := m[1]
				if !strings.Contains(haystack, "#"+num) &&
					!strings.Contains(haystack, "pr "+num) &&
					!strings.Contains(haystack, "pr#"+num) {
					return fmt.Errorf("worklog: wwd payload: card %d references PR #%s not present in refs", i, num)
				}
			}
		}
	}
	return nil
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
	// v2 narrative-card path. When Cards is populated we run the v2
	// validator and ignore Details (the caller chose the new generation).
	if len(p.Cards) > 0 {
		return validateWWDCards(p)
	}
	// v1 legacy path (Details). Same rules as before — kept verbatim.
	if len(p.Details) == 0 {
		return errors.New("worklog: wwd payload: at least one card or detail required")
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
	if p.ServiceSummary != "" {
		b.WriteString(p.ServiceSummary)
		b.WriteString("\n\n")
	}
	// v2 narrative cards: group by kind with section headers.
	if len(p.Cards) > 0 {
		sectionLabel := map[string]string{
			"SHIPPED":      "Features shipped",
			"MAJOR":        "Major work",
			"FIXED":        "Bugs fixed",
			"DECISION":     "Decisions",
			"INVESTIGATED": "Investigated · no fix landed",
			"IN_PROGRESS":  "In progress",
		}
		byKind := map[string][]WWDCard{}
		for _, c := range p.Cards {
			byKind[c.Kind] = append(byKind[c.Kind], c)
		}
		for _, k := range detailKindOrder {
			cs := byKind[k]
			if len(cs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s\n\n", sectionLabel[k])
			for _, c := range cs {
				head := c.Kind
				if c.TicketID != "" {
					head = fmt.Sprintf("%s · %s", c.Kind, c.TicketID)
				}
				fmt.Fprintf(&b, "**%s** — %s\n\n%s\n\n", head, c.Title, c.Body)
				if len(c.Refs) > 0 {
					tokens := make([]string, 0, len(c.Refs))
					for _, r := range c.Refs {
						tokens = append(tokens, r.Text)
					}
					fmt.Fprintf(&b, "_refs: %s_\n\n", strings.Join(tokens, " · "))
				}
			}
		}
		if p.Followup != "" {
			fmt.Fprintf(&b, "### Open question for tomorrow\n\n%s\n", p.Followup)
		}
		return b.String()
	}
	// v1 legacy details path.
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
	out := make([]string, 0)
	add := func(s string) {
		if strings.TrimSpace(s) == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	// v2 cards: ticket id + each ref's text.
	for _, c := range p.Cards {
		if c.TicketID != "" {
			add(c.TicketID)
		}
		for _, r := range c.Refs {
			add(r.Text)
		}
	}
	// v1 details: session_id + each evidence token.
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

	// Idempotency invariant (Fix B+D, 2026-05-26): /klyne:reflect for a
	// given (project, day, service) MUST be replayable. Earlier same-day
	// typed payloads are deleted before this insert so the dashboard
	// always reflects the latest synthesis instead of accumulating ghost
	// details from prior runs. Prose-only rows (body_json NULL) are left
	// alone — their insights still surface in the prose panel.
	if _, derr := store.DeleteTypedReflectionsForProjectDayService(ctx, db, projectPath, dayLabel, payload.Service); derr != nil {
		return store.Reflection{}, fmt.Errorf("worklog: clear prior typed reflections: %w", derr)
	}

	// Cursor advancement: cover the FULL day window for this (project,
	// day) so a future cross-day reflect sees the day as fully synthesized.
	// Pre-fix this used MAX(ts) of cited session_ids — under-citation by
	// the LLM left non-cited turns "covered" without ever being
	// synthesized into a typed detail, which was the operations-app
	// 2026-05-26 1-detail-card bug.
	cursor := dayEndCursorMs(day)

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

// dayEndCursorMs returns the millisecond timestamp of the last instant
// inside `day`'s local-zone window. Used by the typed-reflection path
// as the cursor watermark: once a day has been synthesized into a typed
// payload, every stop_summary inside that day is considered covered for
// the global "pending entries" UX. Re-reflecting the same day rewrites
// the typed payload but the cursor stays day-end either way.
func dayEndCursorMs(day time.Time) int64 {
	loc := day.Location()
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	return dayStart.Add(24*time.Hour).UnixMilli() - 1
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
