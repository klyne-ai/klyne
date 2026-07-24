// L1 floor extractor (2026-05-26 fix).
//
// The L3 reflection synthesis layer is fallible — an under-cited LLM
// run, an aggressive suppression rule upstream, or a stale typed
// payload can leave the dashboard's "What was done" card showing far
// fewer details than the day actually contains. The fix is to compute
// a DETERMINISTIC floor directly from L1 stop_summaries: regex-extract
// the day's CLI ticket ids, commit SHAs, PR refs, and feature branches
// from each turn's ai_drafted_summary, group by ticket and outcome, and
// emit one WWDDetail per distinct state. The dashboard merges this floor
// with the LLM-authored card so completed and unfinished outcomes both
// appear even when the LLM under-collapsed.
//
// Hard determinism boundary (spec §7.1): NO LLM, NO network. Pure
// SQLite read + regex.
package productivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

// L1FloorDetail is one extracted detail from L1 stop_summaries. Mirrors
// WWDDetail exactly so it can be merged into a WhatWasDoneCard without
// conversion gymnastics, but lives behind its own type so callers can
// distinguish "computed floor" from "LLM-authored detail" at the merge
// boundary.
type L1FloorDetail struct {
	Kind      string
	When      string
	Text      string
	Evidence  []string
	SessionID string
	CLIs      []string
	TS        int64
	// TicketID is the grouping key — usually a CLI-NNNN match. Empty when
	// the detail was grouped by feature-branch fallback.
	TicketID string
}

// l1Patterns is the regex set the floor extractor uses to mine evidence
// tokens out of an ai_drafted_summary line. All patterns are case-sensitive
// where the underlying token convention is case-sensitive; CLI-NNNN
// tickets are uppercase by convention.
var (
	floorTicketRe        = regexp.MustCompile(`\bCLI-\d+\b`)
	floorCommitShaRe     = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	floorPRRefRe         = regexp.MustCompile(`\bPR\s*#(\d+)\b`)
	floorFeatureBranchRe = regexp.MustCompile(`\b(?:feature|fix|hotfix|chore|refactor)/[A-Za-z0-9._-]+\b`)
	// Verb classifier — first match wins.
	floorVerbPatterns = []struct {
		re   *regexp.Regexp
		kind string
	}{
		{regexp.MustCompile(`(?i)\b(blocked|blocking|waiting\s+on|cannot\s+proceed|can't\s+proceed|changes\s+requested)\b`), "IN_PROGRESS"},
		{regexp.MustCompile(`(?i)\b(pending|deferred|not\s+picked|not\s+started|not\s+done|todo|to-do|follow[- ]?up)\b`), "IN_PROGRESS"},
		{regexp.MustCompile(`(?i)\b(shipped|landed|pushed|merged|opened\s+PR|wrote|wired|implemented|deployed)\b`), "SHIPPED"},
		{regexp.MustCompile(`(?i)\b(fixed|resolved|repaired|restored|corrected|patched)\b`), "FIXED"},
		{regexp.MustCompile(`(?i)\b(decided|adopted|chose|switched\s+to|recommended|agreed)\b`), "DECISION"},
		{regexp.MustCompile(`(?i)\b(investigated|traced|confirmed|debugged|root\s+caused?|audited|reviewed)\b`), "INVESTIGATED"},
		{regexp.MustCompile(`(?i)\b(in\s+progress|wip|started|drafted|outlined|brainstormed)\b`), "IN_PROGRESS"},
	}
)

// BuildFloorFromStopSummaries scans the day's stop_summaries for
// projectPath and emits one L1FloorDetail per distinct ticket/outcome (or
// feature branch/outcome fallback) — the deterministic coverage floor for the
// dashboard's per-service "What was done" card. The result is sorted
// ASC by When so the dashboard's newest-first composer interleaves it
// correctly with any LLM-authored details.
//
// Rows with recap_visible=0 are INCLUDED whenever ai_drafted_summary is
// non-empty. The client-side contract already requires trivial turns to emit
// KLYNE_SUMMARY: skip, which is stored as an empty summary. Applying a second
// length/token gate here dropped valid short Codex summaries from the
// dashboard even though capture had succeeded.
//
// dayStr is the local-zone YYYY-MM-DD the dashboard already uses for
// per-day bucketing. The SQL query mirrors ListReflectionsForProjectDay's
// localtime conversion so the floor's day boundary lines up with the
// reflection layer's day boundary.
// floorTurnAdmitted reports whether a stop_summaries turn contributes to
// the L1 floor. A turn is admitted only when its summary is non-empty, is
// not a klyne tool-run turn (a session whose prompt invoked a /klyne:…
// slash command — productivity-sync, reflect, bootstrap, etc.), and is not
// a purely tool-only summary. Centralised so the floor and the freshness
// check (LatestFloorTurnTs) admit the exact same turns.
//
// The lastUser check is the load-bearing exclusion for freshness: the
// headless /klyne:productivity-sync compile run ends its own session,
// writing a stop_summary whose AI-drafted prose ("Wrote 3 cards … CLI-…")
// would otherwise be admitted with ts > the card's compile time and make
// the just-compiled day look perpetually stale (recompile loop). Keying
// off the invoking prompt is structural and robust where prose-matching
// (isToolOnlySummary) is not.
func floorTurnAdmitted(summary, lastUser string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	if isKlyneToolPrompt(lastUser) {
		return false
	}
	if isToolOnlySummary(summary) {
		return false
	}
	return true
}

// isKlyneToolPrompt reports whether a turn's user prompt invoked a klyne
// slash command (e.g. "/klyne:productivity-sync project_path=… day=…",
// "/klyne:reflect", "/klyne:bootstrap"). Such turns are klyne's own
// machinery, never user work, so they must not contribute to — or refresh
// the staleness of — the L1 floor.
func isKlyneToolPrompt(lastUser string) bool {
	s := strings.ToLower(strings.TrimSpace(lastUser))
	return strings.HasPrefix(s, "/klyne:") || strings.HasPrefix(s, "/productivity-sync")
}

// LatestFloorTurnTs returns the newest ts among stop_summaries turns that
// the L1 floor would ADMIT for a (project_path, local-day) bucket, or 0
// when none. It applies floorTurnAdmitted, so a day's freshness reflects
// exactly the turns the floor surfaces — a tool-only run (e.g. the headless
// /klyne:productivity-sync compile itself) does NOT make a compiled day
// look stale. Used to decide whether a day's llm_compiled card is out of
// date relative to new user work.
func LatestFloorTurnTs(ctx context.Context, db dbReader, projectPath, dayStr string) (int64, error) {
	const q = `
SELECT ts, COALESCE(ai_drafted_summary,''), COALESCE(last_user,'')
  FROM stop_summaries
 WHERE project_path = ?
   AND date(ts / 1000, 'unixepoch', 'localtime') = ?`
	rows, err := db.QueryContext(ctx, q, projectPath, dayStr)
	if err != nil {
		return 0, err
	}
	defer rows.Close() //nolint:errcheck
	var latest int64
	for rows.Next() {
		var ts int64
		var summary, lastUser string
		if err := rows.Scan(&ts, &summary, &lastUser); err != nil {
			return 0, err
		}
		if !floorTurnAdmitted(summary, lastUser) {
			continue
		}
		if ts > latest {
			latest = ts
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return latest, nil
}

func BuildFloorFromStopSummaries(
	ctx context.Context, db dbReader, projectPath, dayStr string,
) ([]L1FloorDetail, error) {
	const q = `
	SELECT session_id, ts, COALESCE(cli,''), COALESCE(ai_drafted_summary,''),
	       COALESCE(last_bash,''), COALESCE(last_user,''), COALESCE(files_json,'[]')
  FROM stop_summaries
 WHERE project_path = ?
   AND date(ts / 1000, 'unixepoch', 'localtime') = ?
 ORDER BY ts ASC`
	rows, err := db.QueryContext(ctx, q, projectPath, dayStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	type pendingTurn struct {
		sessionID string
		ts        int64
		cli       string
		summary   string
		bash      string
		lastUser  string
		kind      string
		files     []string
	}
	var turns []pendingTurn
	for rows.Next() {
		var t pendingTurn
		var filesJSON string
		if err := rows.Scan(&t.sessionID, &t.ts, &t.cli, &t.summary, &t.bash, &t.lastUser, &filesJSON); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(filesJSON), &t.files)
		t.summary = strings.TrimSpace(t.summary)
		if !floorTurnAdmitted(t.summary, t.lastUser) {
			continue
		}
		t.cli = strings.ToLower(strings.TrimSpace(t.cli))
		t.kind = classifyFloorKind(t.summary)
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Group by outcome as well as ticket/branch/session. A ticket can move
	// from IN_PROGRESS to SHIPPED or carry both SHIPPED and FIXED outcomes;
	// ticket-only grouping collapsed those distinct states.
	type bucket struct {
		key         string
		ticket      string
		branch      string
		latestTS    int64
		kind        string
		turns       []pendingTurn
		evidenceSet map[string]bool
	}
	buckets := map[string]*bucket{}
	order := []string{}
	addToBucket := func(key, ticket, branch string, t pendingTurn) {
		b, ok := buckets[key]
		if !ok {
			b = &bucket{
				key: key, ticket: ticket, branch: branch,
				latestTS: t.ts, kind: t.kind,
				evidenceSet: map[string]bool{},
			}
			buckets[key] = b
			order = append(order, key)
		}
		if t.ts > b.latestTS {
			b.latestTS = t.ts
		}
		b.turns = append(b.turns, t)
	}
	for _, t := range turns {
		// Prefer ticket id grouping.
		tickets := uniqStrings(floorTicketRe.FindAllString(t.summary, -1))
		if len(tickets) > 0 {
			// A single turn that mentions N tickets contributes to all N
			// buckets — the dashboard wants per-ticket coverage even when
			// the user multitasks across tickets in one turn.
			for _, tk := range tickets {
				addToBucket("ticket:"+tk+"\x1fkind:"+t.kind, tk, "", t)
			}
			continue
		}
		branches := uniqStrings(floorFeatureBranchRe.FindAllString(t.summary, -1))
		if len(branches) > 0 {
			for _, br := range branches {
				addToBucket("branch:"+br+"\x1fkind:"+t.kind, "", br, t)
			}
			continue
		}
		// Catch-all by session — preserves investigation turns the model
		// summarised without a ticket reference. Multiple summary-only
		// turns from the same session collapse into one bucket so a
		// chatty session doesn't produce one detail per turn.
		addToBucket("session:"+t.sessionID+"\x1fkind:"+t.kind, "", "", t)
	}

	out := make([]L1FloorDetail, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		detail := L1FloorDetail{
			TicketID:  b.ticket,
			SessionID: b.turns[0].sessionID,
			When:      time.UnixMilli(b.latestTS).Local().Format("15:04"),
			Kind:      b.kind,
			TS:        b.latestTS,
		}
		// Compose evidence: ticket id first (when present), then every
		// commit SHA / PR ref / feature branch / file path / session_id
		// seen across the bucket's turns, deduped.
		add := func(s string) {
			s = strings.TrimSpace(s)
			if s == "" || b.evidenceSet[s] {
				return
			}
			b.evidenceSet[s] = true
			detail.Evidence = append(detail.Evidence, s)
		}
		if b.ticket != "" {
			add(b.ticket)
		}
		if b.branch != "" {
			add(b.branch)
		}
		// Pick the longest summary within this outcome bucket as the
		// representative text and preserve every contributing CLI.
		repText := ""
		repSession := b.turns[0].sessionID
		cliSet := map[string]bool{}
		for _, t := range b.turns {
			for _, sha := range floorCommitShaRe.FindAllString(t.summary, -1) {
				add(sha)
			}
			for _, pr := range floorPRRefRe.FindAllStringSubmatch(t.summary, -1) {
				if len(pr) >= 2 {
					add("PR #" + pr[1])
				}
			}
			for _, br := range floorFeatureBranchRe.FindAllString(t.summary, -1) {
				add(br)
			}
			for _, file := range t.files {
				add(file)
			}
			add(t.sessionID)
			if t.cli != "" {
				cliSet[t.cli] = true
			}
			if len(t.summary) > len(repText) {
				repText = t.summary
				repSession = t.sessionID
			}
		}
		detail.SessionID = repSession
		for cli := range cliSet {
			detail.CLIs = append(detail.CLIs, cli)
		}
		sort.Strings(detail.CLIs)
		detail.Text = floorTruncateText(repText, 200)
		if len(detail.Evidence) == 0 || strings.TrimSpace(detail.Text) == "" {
			continue
		}
		out = append(out, detail)
	}

	// Dedupe near-duplicates: when the same session covers two ticket
	// buckets with IDENTICAL representative text AND identical evidence
	// (a "referenced but not worked-on" secondary ticket like CLI-689
	// cited as a regression gate inside CLI-1473's investigation), keep
	// only the first occurrence. This avoids the dashboard showing the
	// same prose twice under different ticket pills.
	out = dedupeFloorBySessionAndText(out)
	out = suppressSupersededOpenFloor(out)

	// Stable sort: earlier When first, then ticket alphabetical for
	// determinism. Callers (ComposeWWD-style mergers) re-sort by §1.2 kind
	// order, but we want the input order itself to be reproducible.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].When != out[j].When {
			return out[i].When < out[j].When
		}
		return out[i].TicketID < out[j].TicketID
	})
	return out, nil
}

// suppressSupersededOpenFloor removes an older open-state outcome after the
// same ticket reaches a later terminal outcome. A later open turn is retained
// because it represents follow-up work after the ship/fix.
func suppressSupersededOpenFloor(in []L1FloorDetail) []L1FloorDetail {
	merged := append([]L1FloorDetail(nil), in...)
	latestTerminal := map[string]int{}
	for i, d := range merged {
		if d.TicketID == "" {
			continue
		}
		switch d.Kind {
		case "SHIPPED", "FIXED", "DECISION":
			current, found := latestTerminal[d.TicketID]
			if !found || d.TS > merged[current].TS {
				latestTerminal[d.TicketID] = i
			}
		}
	}
	out := make([]L1FloorDetail, 0, len(in))
	for i, d := range merged {
		if terminalIndex, found := latestTerminal[d.TicketID]; found && d.TS <= merged[terminalIndex].TS {
			switch d.Kind {
			case "MAJOR", "IN_PROGRESS", "INVESTIGATED":
				// Preserve contribution provenance on the terminal outcome even
				// though the stale open-state row itself is removed.
				merged[terminalIndex].CLIs = unionStrings(merged[terminalIndex].CLIs, d.CLIs)
				merged[terminalIndex].Evidence = unionStrings(merged[terminalIndex].Evidence, d.Evidence)
				continue
			}
		}
		out = append(out, merged[i])
	}
	return out
}

// dedupeFloorBySessionAndText drops entries whose (SessionID, Text)
// pair has already been emitted. A single turn that mentions multiple
// CLI-NNNN tickets buckets under each — we keep the first ticket the
// turn names (alphabetical input order from regex) and drop the
// secondary mentions so the same prose doesn't surface twice.
func dedupeFloorBySessionAndText(in []L1FloorDetail) []L1FloorDetail {
	seen := make(map[string]bool, len(in))
	out := make([]L1FloorDetail, 0, len(in))
	for _, d := range in {
		key := d.SessionID + "\x1f" + d.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// toolOnlyPrefixes are case-insensitive prefixes whose presence at the
// start of an ai_drafted_summary marks the turn as a self-referential
// tool run (running /klyne:reflect, /productivity-sync, /klyne:bootstrap,
// /klyne:status, etc.) instead of real engineering work. The floor
// extractor drops these BEFORE bucketing so they cannot dominate the
// per-service card.
var toolOnlyPrefixes = []string{
	"ran /klyne:",
	"ran the /klyne:",
	"ran productivity-sync",
	"ran the productivity-sync",
	"ran /productivity-sync",
	"ran /klyne:productivity-sync",
	"compiled 0 productivity",
	"compiled 1 productivity",
	"compiled 2 productivity",
	"compiled 3 productivity",
	"compiled 4 productivity",
	"compiled 5 productivity",
	"synthesized 0 typed reflection",
	"synthesized 1 typed reflection",
	"synthesized 2 typed reflection",
	"synthesized 3 typed reflection",
	"synthesized one typed reflection",
	"synthesized 0 daily reflection",
	"synthesized 1 daily reflection",
	"synthesized one daily reflection",
	"synthesized and persisted",
	"called propose_reflection",
	"audited stop_summaries",
}

// toolOnlySubstrings catch tool-only turns whose summary lead doesn't
// match the prefix list (e.g. "Generated strict-JSON productivity
// dashboard output", "Pulled and showed Opus's actual reflect output").
// These describe the dashboard tooling itself, not user code work.
var toolOnlySubstrings = []string{
	"productivity dashboard json",
	"productivity dashboard output",
	"productivity-sync second pass",
	"klyne:reflect for ",
	"two-tier dashboard json",
	"kind-tagged timeline json",
	"compact json panel",
	"worklog entries for ",
	"pending worklog entries",
	"pending worklog entry",
}

// mergeFloorIntoNarrative is the v2 path of MergeFloorIntoCard. The
// LLM-authored card already has Narrative.Cards; we only ADD cards for
// tickets the LLM missed. Refs are typed via inferRefType so the
// dashboard styles them consistently with the LLM's output.
func mergeFloorIntoNarrative(service string, llmCard *WhatWasDoneCard, floor []L1FloorDetail) *WhatWasDoneCard {
	out := *llmCard
	out.Service = service
	// Deep-copy the narrative pointer + Cards slice so we can mutate
	// without aliasing the snapshot.
	nv := *llmCard.Narrative
	nv.Cards = append([]WWDCard(nil), llmCard.Narrative.Cards...)
	out.Narrative = &nv

	covered := map[string]bool{}
	for i := range nv.Cards {
		c := &nv.Cards[i]
		if c.TicketID != "" {
			covered[c.TicketID+"\x1f"+c.Kind] = true
			for _, f := range floor {
				if f.TicketID == c.TicketID && f.Kind == c.Kind {
					c.CLIs = unionStrings(c.CLIs, f.CLIs)
				}
			}
		}
	}
	added := 0
	for _, f := range floor {
		// Skip floor entries with no ticket id (untickatable buckets) when
		// the LLM has any narrative cards already — the LLM's untickatable
		// coverage is qualitative and we shouldn't append session-bucketed
		// noise on top.
		if f.TicketID == "" {
			continue
		}
		outcomeKey := f.TicketID + "\x1f" + f.Kind
		if covered[outcomeKey] {
			continue
		}
		nv.Cards = append(nv.Cards, WWDCard{
			Kind:     f.Kind,
			TicketID: f.TicketID,
			Title:    floorTruncateText(f.Text, 120),
			Body:     f.Text,
			Refs:     floorRefsToTyped(f.Evidence),
			CLIs:     append([]string(nil), f.CLIs...),
		})
		covered[outcomeKey] = true
		added++
	}
	if added == 0 {
		// LLM already covered everything — preserve the original card
		// (including its llm_compiled=true flag and TLDR).
		return llmCard
	}
	// Refresh stats from the merged card set.
	stats := WWDStats{}
	for _, c := range nv.Cards {
		switch c.Kind {
		case "SHIPPED":
			stats.Shipped++
		case "MAJOR":
			stats.Major++
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
	nv.Stats = stats
	out.Narrative = &nv
	// The card is no longer purely LLM-authored.
	out.LLMCompiled = false
	return &out
}

// floorRefsToTyped converts an L1 floor's evidence []string into typed
// WWDRefs by guessing the type from token shape.
func floorRefsToTyped(evidence []string) []WWDRef {
	out := make([]WWDRef, 0, len(evidence))
	seen := map[string]bool{}
	for _, e := range evidence {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, WWDRef{Type: inferRefType(e), Text: e})
		if len(out) >= 6 {
			break
		}
	}
	return out
}

// inferRefType picks a WWDRef.Type from a literal token. Order matters:
// PR shape catches "PR #N" before commit SHA, ticket catches "CLI-NNN"
// before file (which contains a path separator), etc.
func inferRefType(tok string) string {
	switch {
	case strings.HasPrefix(strings.ToLower(tok), "pr #"),
		strings.HasPrefix(strings.ToLower(tok), "pr#"):
		return "pr"
	case floorTicketRe.MatchString(tok):
		return "ticket"
	case strings.HasPrefix(tok, "feature/"),
		strings.HasPrefix(tok, "fix/"),
		strings.HasPrefix(tok, "hotfix/"),
		strings.HasPrefix(tok, "chore/"),
		strings.HasPrefix(tok, "refactor/"):
		return "branch"
	case floorCommitShaRe.MatchString(tok) && len(tok) >= 7 && len(tok) <= 40:
		return "commit"
	case strings.Contains(tok, "/") || strings.Contains(tok, "."):
		return "file"
	case strings.HasPrefix(tok, "Test") || strings.HasSuffix(tok, "_test"):
		return "test"
	default:
		return "session"
	}
}

func isToolOnlySummary(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	for _, p := range toolOnlyPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	for _, p := range toolOnlySubstrings {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// classifyFloorKind picks the applicable §1.1 kind enum from a turn's AI
// summary. Explicit blocked/pending language takes priority so unfinished
// state cannot be counted as shipped merely because the same sentence also
// mentions implementation. Falls back to MAJOR when nothing matches.
func classifyFloorKind(text string) string {
	for _, p := range floorVerbPatterns {
		if p.re.MatchString(text) {
			return p.kind
		}
	}
	return "MAJOR"
}

// floorTruncateText caps text at maxLen runes, trimming at a word
// boundary and appending an ellipsis when truncation actually fires.
// Matches the §1.1 200-char limit on WWDDetail.Text.
func floorTruncateText(s string, maxLen int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	if idx := strings.LastIndex(cut, " "); idx > maxLen/2 {
		cut = cut[:idx]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}

// CardTicketIDs returns the set of L1 ticket IDs a WhatWasDone card already
// covers — from narrative card TicketIDs plus any CLI-NNNN reference in the
// narrative titles/bodies/summary or the Tier2 detail text/evidence. Used
// to decide whether a compiled card still covers the current L1 floor; an
// uncovered floor ticket means the day still has compile work outstanding
// (e.g. a second service under the same project_path, or a ticket the LLM
// under-cited). Returns an empty (non-nil) set for a nil card.
func CardTicketIDs(card *WhatWasDoneCard) map[string]bool {
	out := map[string]bool{}
	if card == nil {
		return out
	}
	add := func(s string) {
		for _, tk := range floorTicketRe.FindAllString(s, -1) {
			out[tk] = true
		}
	}
	if card.Narrative != nil {
		add(card.Narrative.Summary)
		for _, c := range card.Narrative.Cards {
			if c.TicketID != "" {
				out[c.TicketID] = true
			}
			add(c.Title)
			add(c.Body)
		}
	}
	for _, d := range card.Tier2.Details {
		add(d.Text)
		for _, ev := range d.Evidence {
			add(ev)
		}
	}
	return out
}

// CardOutcomeKeys returns ticket+kind coverage keys. Unlike CardTicketIDs,
// this distinguishes an in-progress card from a later shipped/fixed outcome.
func CardOutcomeKeys(card *WhatWasDoneCard) map[string]bool {
	out := map[string]bool{}
	if card == nil {
		return out
	}
	if card.Narrative != nil {
		for _, c := range card.Narrative.Cards {
			if c.TicketID != "" {
				out[c.TicketID+"\x1f"+c.Kind] = true
			}
		}
	}
	for _, d := range card.Tier2.Details {
		for _, tk := range floorTicketRe.FindAllString(d.Text, -1) {
			out[tk+"\x1f"+d.Kind] = true
		}
		for _, ev := range d.Evidence {
			for _, tk := range floorTicketRe.FindAllString(ev, -1) {
				out[tk+"\x1f"+d.Kind] = true
			}
		}
	}
	return out
}

// uniqStrings returns xs with duplicates removed, preserving first-seen
// order.
func uniqStrings(xs []string) []string {
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

// dbReader is the minimal interface BuildFloorFromStopSummaries needs.
// Both *store.DB.Read() and a raw *sql.DB satisfy it — keeping the seam
// narrow lets tests pass a tempdir DB without importing the full
// internal/store DB type.
type dbReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// MergeFloorIntoCard takes an LLM-authored card (possibly nil or thin)
// and a deterministic L1 floor for the same service. Two branches:
//
//   - V2 narrative path (llmCard.Narrative != nil): build "covered
//     tickets" from Narrative.Cards' ticket_id field. For each floor
//     entry whose TicketID is NOT in that set, append a synthesized
//     narrative WWDCard so the dashboard surfaces every distinct ticket
//     the user touched today. Refs are typed via inferRefType. LLM-
//     compiled flag stays true when no cards added (LLM coverage was
//     complete) and flips to false when the floor backfilled gaps.
//
//   - V1 legacy path (Narrative == nil): existing Tier2.Details merge.
//     Build covered-ticket set from Tier2.Details, append missing
//     floor entries as Tier2 details, rebuild Tier1 mechanically.
//
// nil llmCard ⇒ start fresh with only the floor (legacy v1 shape so
// the deterministic fallback always renders something even with no LLM).
func MergeFloorIntoCard(service string, llmCard *WhatWasDoneCard, floor []L1FloorDetail) *WhatWasDoneCard {
	// V2 narrative-aware path.
	if llmCard != nil && llmCard.Narrative != nil {
		return mergeFloorIntoNarrative(service, llmCard, floor)
	}
	out := WhatWasDoneCard{Service: service}
	if llmCard != nil {
		out = *llmCard
		out.Service = service
	}
	covered := map[string]bool{}
	for i := range out.Tier2.Details {
		d := &out.Tier2.Details[i]
		for _, tk := range floorTicketRe.FindAllString(d.Text, -1) {
			covered[tk+"\x1f"+d.Kind] = true
			for _, f := range floor {
				if f.TicketID == tk && f.Kind == d.Kind {
					d.CLIs = unionStrings(d.CLIs, f.CLIs)
				}
			}
		}
		for _, ev := range d.Evidence {
			for _, tk := range floorTicketRe.FindAllString(ev, -1) {
				covered[tk+"\x1f"+d.Kind] = true
			}
		}
	}
	added := 0
	for _, f := range floor {
		// Untickatable floor entries (sessions / branches) only fill in
		// when the LLM card has zero matching entries — otherwise we'd
		// duplicate investigation turns that the LLM already covered.
		if f.TicketID == "" {
			// Untickatable buckets (session/branch fallbacks) dedupe by
			// When + prefix-of-truncated-text. Forgiving of trivial
			// wording differences without a full string match.
			prefix := floorTruncateText(f.Text, 60)
			dup := false
			for _, d := range out.Tier2.Details {
				if d.When == f.When && prefix != "" && strings.HasPrefix(d.Text, prefix) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		} else if covered[f.TicketID+"\x1f"+f.Kind] {
			continue
		}
		out.Tier2.Details = append(out.Tier2.Details, WWDDetail{
			Kind:      f.Kind,
			When:      f.When,
			Text:      f.Text,
			Evidence:  append([]string{}, f.Evidence...),
			SessionID: f.SessionID,
			CLIs:      append([]string(nil), f.CLIs...),
		})
		if f.TicketID != "" {
			covered[f.TicketID+"\x1f"+f.Kind] = true
		}
		added++
	}
	if added == 0 && llmCard != nil && llmCard.LLMCompiled {
		// Nothing to merge — return the LLM card as-is so its authored
		// TLDR is preserved verbatim.
		copy := *llmCard
		return &copy
	}
	// Re-sort by §1.2 kind order, newest-first within a kind.
	out.Tier2.Details = sortDetails(out.Tier2.Details)
	// Rebuild Tier1 deterministically from the merged set. If the LLM
	// authored a custom TLDR we keep it; otherwise the mechanical TLDR
	// from BuildMechanicalTier1 replaces it.
	mech := BuildMechanicalTier1(out.Tier2.Details)
	if llmCard != nil && llmCard.LLMCompiled && strings.TrimSpace(llmCard.Tier1.TLDR) != "" {
		mech.TLDR = llmCard.Tier1.TLDR
	}
	out.Tier1 = mech
	// Mark as not-llm-compiled when we merged in floor entries — the
	// merged shape is partially deterministic, the badge would mislead.
	if added > 0 {
		out.LLMCompiled = false
	}
	if len(out.Tier2.Details) == 0 {
		return nil
	}
	return &out
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, values := range [][]string{a, b} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
