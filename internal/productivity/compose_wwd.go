package productivity

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// kindOrder is the locked SHIPPED → MAJOR → FIXED → DECISION →
// INVESTIGATED → IN_PROGRESS render order from §1.2 of
// docs/plan/2026-05-26-wwd-typed-cards.md. Unknown kinds are dropped at
// compose time — the §1.1 schema is a closed enum.
var kindOrder = []string{
	"SHIPPED",
	"MAJOR",
	"FIXED",
	"DECISION",
	"INVESTIGATED",
	"IN_PROGRESS",
}

// allowedKinds is a set lookup for the locked enum. Anything else is
// silently dropped — the schema validator (Agent P) is the boundary
// that rejects bad input; the composer is defensive against drift.
var allowedKinds = func() map[string]struct{} {
	out := make(map[string]struct{}, len(kindOrder))
	for _, k := range kindOrder {
		out[k] = struct{}{}
	}
	return out
}()

// pillKey maps a kind enum to its lowercased pill_counts key from §1.2.
// DECISION → "decisions" (the dashboard headline counts plural decisions);
// every other kind lowercases verbatim.
func pillKey(kind string) string {
	switch kind {
	case "DECISION":
		return "decisions"
	default:
		return strings.ToLower(kind)
	}
}

// commitShaRe matches the §1.2 commit-sha shape — a lower-hex token of
// 7..40 characters. Used for both top_evidence selection and
// commit_count.
var commitShaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// tldrTextCap is the §1.2 word-boundary truncation budget for the
// newest detail's text in the headline TLDR. ~90 chars per spec.
const tldrTextCap = 90

// ComposeWWD computes the per-service "What was done" cards from a
// day's worklog_reflections rows. Pure function: no I/O, no DB, no
// LLM. Matches §1.2 of docs/plan/2026-05-26-wwd-typed-cards.md.
//
// Rows without a typed body_json payload are skipped (legacy prose-only
// reflections never produce a typed card; the UI falls back to the
// legacy reflection_markdown path for those). Rows whose body_json
// fails to parse are skipped — the composer cannot panic the dashboard.
//
// Cards are returned in alphabetical Service order for stable
// iteration. Within each card, Tier 2 details are ordered by kindOrder
// then newest-first by When. Tier 1 is derived deterministically from
// the merged Tier 2 set.
func ComposeWWD(rows []store.Reflection) []WhatWasDoneCard {
	if len(rows) == 0 {
		return nil
	}

	// Bucket details by service. A single payload covers ONE service
	// (§1.1), but multiple reflection rows for the same service-on-day
	// merge into one card.
	byService := map[string][]WWDDetail{}
	for _, r := range rows {
		payload, err := store.ParseBodyJSON(r)
		if err != nil {
			// Defensive: a malformed payload was already rejected at the
			// MCP-tool boundary; skip rather than crash the dashboard.
			continue
		}
		service := strings.TrimSpace(payload.Service)
		if service == "" {
			// Fall back to the project basename so a payload that lost its
			// `service` key still surfaces under a sensible card.
			service = filepath.Base(r.ProjectPath)
		}
		for _, d := range payload.Details {
			if _, ok := allowedKinds[d.Kind]; !ok {
				continue
			}
			if strings.TrimSpace(d.Text) == "" {
				continue
			}
			if len(d.Evidence) == 0 {
				// §5 citation invariant: drop unevidenced details
				// defensively so a writer drift can't put unverifiable
				// claims on the dashboard.
				continue
			}
			// store.WWDDetail and productivity.WWDDetail share the §1.1
			// field shape but live in separate packages (the wire shape is
			// duplicated for layer boundaries). Convert at append time so
			// the composer keeps its own type for return-value purity.
			byService[service] = append(byService[service], WWDDetail{
				Kind:      d.Kind,
				When:      d.When,
				Text:      d.Text,
				Evidence:  d.Evidence,
				SessionID: d.SessionID,
			})
		}
	}

	if len(byService) == 0 {
		return nil
	}

	services := make([]string, 0, len(byService))
	for s := range byService {
		services = append(services, s)
	}
	sort.Strings(services)

	out := make([]WhatWasDoneCard, 0, len(services))
	for _, svc := range services {
		details := byService[svc]
		details = sortDetails(details)

		card := WhatWasDoneCard{Service: svc}
		card.Tier1 = buildTier1(details)
		card.Tier2.Details = details
		out = append(out, card)
	}
	return out
}

// sortDetails orders details by kindOrder primary, then When DESC
// within a kind (newest-first). Stable sort so two details with the
// same kind+When keep their input order.
func sortDetails(details []WWDDetail) []WWDDetail {
	kindRank := map[string]int{}
	for i, k := range kindOrder {
		kindRank[k] = i
	}
	out := make([]WWDDetail, len(details))
	copy(out, details)
	sort.SliceStable(out, func(i, j int) bool {
		ri, ki := kindRank[out[i].Kind], kindRank[out[j].Kind]
		if ri != ki {
			return ri < ki
		}
		// Newest-first by When (HH:MM lexically sorts as numerically).
		return out[i].When > out[j].When
	})
	return out
}

// buildTier1 derives the §1.2 deterministic headline row from the
// already-ordered Tier 2 detail list. Counts every detail's kind into
// pill_counts; harvests commit-sha-looking evidence into top_evidence
// (dedup, first-3 in encounter order); counts distinct session_ids and
// commit-sha tokens; composes the templated TLDR with only the
// non-zero counts in §1.2's canonical order (shipped · fixed ·
// decisions · investigated · in_progress · major) plus the newest
// detail's word-boundary-truncated text.
func buildTier1(details []WWDDetail) WWDTier1 {
	t1 := WWDTier1{
		PillCounts:  map[string]int{},
		TopEvidence: []string{},
	}
	if len(details) == 0 {
		return t1
	}

	sessionSeen := map[string]struct{}{}
	shaSeen := map[string]struct{}{}
	for _, d := range details {
		t1.PillCounts[pillKey(d.Kind)]++
		if sid := strings.TrimSpace(d.SessionID); sid != "" {
			sessionSeen[sid] = struct{}{}
		}
		for _, e := range d.Evidence {
			tok := strings.TrimSpace(e)
			if tok == "" {
				continue
			}
			if !commitShaRe.MatchString(tok) {
				continue
			}
			if _, seen := shaSeen[tok]; seen {
				continue
			}
			shaSeen[tok] = struct{}{}
			if len(t1.TopEvidence) < 3 {
				t1.TopEvidence = append(t1.TopEvidence, tok)
			}
		}
	}
	t1.TurnCount = len(sessionSeen)
	t1.CommitCount = len(shaSeen)

	t1.TLDR = renderTLDR(t1.PillCounts, details[0].Text)
	return t1
}

// tldrKeyOrder is the canonical pill-count order used in the headline
// TLDR template. Differs from kindOrder because the template phrases
// kinds with their plural noun forms ("shipped", "fixed", "decisions"
// etc.) and skips MAJOR (covered by the SHIPPED count for the headline
// — MAJOR is a separate chip on the card).
//
// Per §1.2: "{shipped} shipped · {fixed} fixed · {decisions} decisions ·
// latest: …". Zero-count kinds are dropped from the rendering.
var tldrKeyOrder = []string{
	"shipped",
	"fixed",
	"decisions",
	"investigated",
	"in_progress",
	"major",
}

// tldrKeyLabel maps a pill_count key to its phrase in the TLDR template.
// in_progress reads as "in progress" rather than the snake_case key.
func tldrKeyLabel(key string) string {
	switch key {
	case "in_progress":
		return "in progress"
	default:
		return key
	}
}

// renderTLDR assembles the §1.2 TLDR string. Only non-zero counts are
// included, joined with " · ", followed by "latest: <truncated newest
// detail text>". When there are zero non-zero counts the prefix is
// omitted and the line is just "latest: …"; when latest text is empty
// the latest clause is dropped.
func renderTLDR(counts map[string]int, latestText string) string {
	var parts []string
	for _, k := range tldrKeyOrder {
		if c := counts[k]; c > 0 {
			parts = append(parts, formatPill(c, tldrKeyLabel(k)))
		}
	}
	latest := truncateWord(latestText, tldrTextCap)
	if latest != "" {
		parts = append(parts, "latest: "+latest)
	}
	return strings.Join(parts, " · ")
}

// formatPill renders one count+label segment of the TLDR. Pluralisation
// matches the §1.2 example: "3 shipped", "2 decisions", "1 in progress".
func formatPill(n int, label string) string {
	return strings.TrimSpace(itoa(n) + " " + label)
}

// itoa is a tiny strconv-free integer formatter — the composer is on
// the dashboard hot path; this avoids a strconv import for one call
// site. Handles the small non-negative integers compose_wwd ever sees.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

// truncateWord clips s to at most maxLen characters, breaking on a
// word boundary (the last space at-or-before maxLen) and appending "…"
// when truncation actually happened. Returns s unchanged when it
// already fits.
func truncateWord(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		cut = cut[:idx]
	}
	cut = strings.TrimRight(cut, " ,.;:-")
	return cut + "…"
}
