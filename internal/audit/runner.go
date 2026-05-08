package audit

import (
	"fmt"
	"strings"
	"time"
)

// SessionAudit is the per-session result row of an audit pass.
type SessionAudit struct {
	// Path is the absolute path of the source JSONL transcript.
	Path string
	// SessionID is the session identifier reported by the JSONL itself.
	// May be empty for transcripts whose lines carry no session id.
	SessionID string
	// Source is the ground-truth value extracted from the JSONL.
	Source SourceTokens
	// Compact is the /compact event detail for this transcript. May be
	// the zero value (no events). The runner records it for every row,
	// not just mismatched ones, so the cross-session insight pass can
	// aggregate without re-reading the files.
	Compact CompactStats
	// StoredTokens is the value agentdeck has in its SQLite store for
	// this session's latest assistant message with non-zero TokensIn.
	// Zero when Found is false.
	StoredTokens int64
	// Found is true when the session was located in the agentdeck DB.
	// False means the daemon never ingested this transcript — itself a
	// useful diagnostic.
	Found bool
	// Match is true when Source.Tokens == StoredTokens AND Found is
	// true. A session that isn't in the DB cannot match; the audit
	// reports those separately.
	Match bool
	// Delta is StoredTokens - Source.Tokens. Negative means agentdeck
	// undercounts (the visible bug class). Positive means it overcounts.
	// Zero when Match is true OR when Found is false.
	Delta int64
}

// Report aggregates a full audit pass.
type Report struct {
	// GeneratedAt is the wall-clock instant the audit started.
	GeneratedAt time.Time
	// Sessions is the per-transcript result, in walker order (caller
	// controls sort; the runner does not re-order).
	Sessions []SessionAudit
	// Total is len(Sessions). Provided as a convenience for templates.
	Total int
	// Matches counts sessions where Source.Tokens == StoredTokens.
	Matches int
	// Mismatches counts sessions that were Found but had Match=false.
	Mismatches int
	// NotInDB counts sessions whose ground truth said tokens > 0 but
	// the agentdeck DB had no record. Excluded from Mismatches because
	// the failure mode is different (ingestion gap vs counting bug).
	NotInDB int
	// SkippedNoAssistant counts JSONL files that had no qualifying
	// assistant message yet (fresh sessions). These are neither
	// matches nor mismatches.
	SkippedNoAssistant int

	// TotalCompacts is the sum of per-session Compact.Count. A snapshot
	// of "how many /compact events exist across the audited transcripts."
	TotalCompacts int
	// SessionsWithCompacts is the number of distinct sessions that have
	// at least one /compact event. Useful for the "X% of your sessions
	// have ever been compacted" insight.
	SessionsWithCompacts int
	// TotalCompactedTokens is the sum of pre-compact token counts across
	// every event — i.e. how much context the user has traded away to
	// summaries cumulatively. Zero when no events recorded preTokens.
	TotalCompactedTokens int64
	// ManualCompacts is the subset of TotalCompacts that the user
	// explicitly invoked (typed /compact). The complement is auto.
	ManualCompacts int
	// AutoCompacts is the subset triggered automatically by Claude Code
	// when context approached the limit. These are the "lost without
	// my consent" events.
	AutoCompacts int
}

// LookupFunc returns the agentdeck-stored TokensIn for the most-recent
// assistant message of a given session, plus a `found` flag. Implemented
// against the real *store.DB by the cobra subcommand and against an
// in-memory map by tests.
type LookupFunc func(sessionID string) (tokens int64, found bool)

// CodexReport summarises a Codex audit pass. It deliberately does NOT
// compare against the agentdeck DB — Codex's storage model uses
// per-turn deltas as System messages rather than the latest-assistant
// pattern Claude uses, and an apples-to-apples DB comparison requires
// reconstruction work that lives in a follow-up slice. Reporting the
// JSONL ground truth alone is still valuable: it tells the user how
// much Codex usage exists and how often Codex compacted.
type CodexReport struct {
	GeneratedAt          time.Time
	Total                int
	WithTokenData        int
	SkippedNoTokenData   int
	TotalTokens          int64 // sum of latest-event input_tokens across sessions
	MaxSessionTokens     int64 // single largest session-fill at latest event
	TotalCompacts        int
	SessionsWithCompacts int
	Sessions             []CodexSessionAudit
}

// CodexSessionAudit is the per-transcript Codex result row.
type CodexSessionAudit struct {
	Path           string
	SessionID      string
	Model          string
	LatestTokens   int64
	CompactsCount  int
}

// RunCodex audits a slice of Codex rollout JSONL paths. No DB lookup —
// see CodexReport docs for why.
func RunCodex(paths []string) CodexReport {
	r := CodexReport{
		GeneratedAt: time.Now().UTC(),
		Sessions:    make([]CodexSessionAudit, 0, len(paths)),
	}
	for _, p := range paths {
		src, _ := LatestCodexTokens(p)
		compact, _ := CompactBoundaryStatsCodex(p)
		row := CodexSessionAudit{
			Path:          p,
			SessionID:     src.SessionID,
			Model:         src.Model,
			LatestTokens:  src.Tokens,
			CompactsCount: compact.Count,
		}
		if src.Tokens > 0 {
			r.WithTokenData++
			r.TotalTokens += src.Tokens
			if src.Tokens > r.MaxSessionTokens {
				r.MaxSessionTokens = src.Tokens
			}
		} else {
			r.SkippedNoTokenData++
		}
		if compact.Count > 0 {
			r.TotalCompacts += compact.Count
			r.SessionsWithCompacts++
		}
		r.Sessions = append(r.Sessions, row)
	}
	r.Total = len(r.Sessions)
	return r
}

// RenderCodexMarkdown produces the Codex section of the audit output.
// Designed to compose with RenderMarkdown — append the result to that
// output to get a unified report.
func RenderCodexMarkdown(r CodexReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Codex coverage\n\n")
	if r.Total == 0 {
		b.WriteString("No Codex transcripts found under ~/.codex/sessions.\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "- Codex transcripts sampled: %d\n", r.Total)
	fmt.Fprintf(&b, "- Sessions with token usage data: %d\n", r.WithTokenData)
	fmt.Fprintf(&b, "- Sessions with no token data yet: %d\n", r.SkippedNoTokenData)
	if r.WithTokenData > 0 {
		fmt.Fprintf(&b, "- Cumulative tokens across audited Codex sessions: ~%s\n", humanTokens(r.TotalTokens))
		fmt.Fprintf(&b, "- Largest single Codex session (latest turn): %s tokens\n", humanTokens(r.MaxSessionTokens))
	}
	if r.TotalCompacts > 0 {
		fmt.Fprintf(&b, "- /compact events detected: %d total across %d sessions\n",
			r.TotalCompacts, r.SessionsWithCompacts)
		fmt.Fprintf(&b, "  (Codex JSONL does not expose pre-compact token counts; manual/auto split unavailable)\n")
	} else {
		fmt.Fprintf(&b, "- /compact events detected: 0\n")
	}
	fmt.Fprintf(&b, "\n**Limitation:** Codex DB comparison deferred to a follow-up slice. ")
	fmt.Fprintf(&b, "Codex's connector stores per-turn token deltas as system messages, ")
	fmt.Fprintf(&b, "not latest-assistant rows; comparing requires aggregating those deltas, ")
	fmt.Fprintf(&b, "which the Claude path does not need. Until that lands, the audit reports ")
	fmt.Fprintf(&b, "JSONL ground truth for Codex but does NOT validate the DB stored values.\n\n")
	return b.String()
}

// Run executes the audit against the provided JSONL paths using the
// supplied DB lookup. Pure function (modulo file I/O inside
// LatestAssistantTokens) so cobra wiring and tests share one path.
func Run(paths []string, lookup LookupFunc) Report {
	r := Report{
		GeneratedAt: time.Now().UTC(),
		Sessions:    make([]SessionAudit, 0, len(paths)),
	}
	for _, p := range paths {
		src, _ := LatestAssistantTokens(p) // tolerate read errors per-file; report row records empty source
		// Compact stats are fetched in a second small pass over the
		// same file. Two passes keeps each scanner straight-line and
		// independently testable; the I/O cost is negligible compared
		// to JSON parsing.
		compact, _ := CompactBoundaryStats(p)
		if compact.Count > 0 {
			r.TotalCompacts += compact.Count
			r.SessionsWithCompacts++
			r.TotalCompactedTokens += compact.TotalPreTokens
			r.ManualCompacts += compact.Manual
			r.AutoCompacts += compact.Auto
		}
		row := SessionAudit{
			Path:      p,
			SessionID: src.SessionID,
			Source:    src,
			Compact:   compact,
		}
		if src.Tokens == 0 {
			r.SkippedNoAssistant++
			r.Sessions = append(r.Sessions, row)
			continue
		}
		if src.SessionID == "" {
			// Cannot look up — treat as not-in-DB.
			r.NotInDB++
			r.Sessions = append(r.Sessions, row)
			continue
		}
		stored, found := lookup(src.SessionID)
		row.StoredTokens = stored
		row.Found = found
		if !found {
			r.NotInDB++
			r.Sessions = append(r.Sessions, row)
			continue
		}
		row.Delta = stored - src.Tokens
		row.Match = row.Delta == 0
		if row.Match {
			r.Matches++
		} else {
			r.Mismatches++
		}
		r.Sessions = append(r.Sessions, row)
	}
	r.Total = len(r.Sessions)
	return r
}

// RenderMarkdown produces the human-readable audit report. Format
// mirrors code-review-graph's evaluate/reports/summary.md style: a
// short header, a one-paragraph summary, then per-row tables grouped
// by failure class.
func RenderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# agentdeck audit report\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Sessions sampled: %d\n\n", r.Total)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Latest-assistant input_tokens accuracy: %d/%d ✓ (%s)\n",
		r.Matches, nonZero(r.Matches+r.Mismatches), pct(r.Matches, r.Matches+r.Mismatches))
	fmt.Fprintf(&b, "- Sessions not yet ingested into agentdeck DB: %d\n", r.NotInDB)
	fmt.Fprintf(&b, "- Sessions with no assistant turn yet: %d\n", r.SkippedNoAssistant)
	if r.TotalCompacts > 0 {
		fmt.Fprintf(&b, "- /compact events detected: %d total (%d manual, %d auto) across %d sessions; ~%s tokens compacted away cumulatively\n",
			r.TotalCompacts, r.ManualCompacts, r.AutoCompacts, r.SessionsWithCompacts,
			humanTokens(r.TotalCompactedTokens))
	} else {
		fmt.Fprintf(&b, "- /compact events detected: 0\n")
	}
	b.WriteString("\n")

	if r.Mismatches > 0 {
		fmt.Fprintf(&b, "## Mismatches (FAIL — these are the trust failures)\n\n")
		fmt.Fprintf(&b, "| session_id | model | source tokens | stored tokens | delta |\n")
		fmt.Fprintf(&b, "|---|---|---:|---:|---:|\n")
		for _, s := range r.Sessions {
			if !s.Found || s.Match || s.Source.Tokens == 0 {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %+d |\n",
				short(s.SessionID), s.Source.Model, s.Source.Tokens, s.StoredTokens, s.Delta)
		}
		b.WriteString("\n")
	}

	if r.NotInDB > 0 {
		fmt.Fprintf(&b, "## Not in DB (ingestion gap, not a counting bug)\n\n")
		fmt.Fprintf(&b, "| session_id | source tokens | path |\n")
		fmt.Fprintf(&b, "|---|---:|---|\n")
		for _, s := range r.Sessions {
			if s.Found || s.Source.Tokens == 0 {
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %s |\n", short(s.SessionID), s.Source.Tokens, s.Path)
		}
		b.WriteString("\n")
	}

	if r.Matches > 0 && r.Mismatches == 0 && r.NotInDB == 0 {
		b.WriteString("All sampled sessions match. Trust foundation holds for this slice.\n")
	}
	return b.String()
}

// short trims a UUID-style id to its first 8 hex chars for table
// readability. Empty in → empty out.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// pct formats a fraction as a percentage string with one decimal. Returns
// "n/a" when the denominator is zero so the report doesn't show "NaN%".
func pct(num, denom int) string {
	if denom == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(num)/float64(denom)*100.0)
}

// nonZero returns x or 1 when x is zero. Used in summary lines so the
// "Matches/Total" denominator never reads 0/0 confusingly.
func nonZero(x int) int {
	if x == 0 {
		return 0
	}
	return x
}

// humanTokens formats a token count in a human-friendly way for the
// summary line. Above 1M renders as "1.4M", above 1K as "342K".
// Unsigned only — the audit's token totals are always non-negative.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000.0)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000.0)
	default:
		return fmt.Sprintf("%d", n)
	}
}
