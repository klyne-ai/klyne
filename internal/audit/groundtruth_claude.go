package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// claudeRawLine mirrors the subset of Claude Code's JSONL schema this
// package needs. Kept local rather than imported from the connector so
// the audit can run without taking a dependency on the parser's full
// row schema (the audit's whole purpose is to second-guess that
// parser).
type claudeRawLine struct {
	Type      string             `json:"type"`
	Subtype   string             `json:"subtype"`
	SessionID string             `json:"sessionId"`
	UUID      string             `json:"uuid"`
	Timestamp string             `json:"timestamp"`
	Message   *claudeRawMsg      `json:"message"`
	Compact   *claudeCompactMeta `json:"compactMetadata"`
}

// claudeCompactMeta carries the per-event detail attached to system
// lines whose subtype is "compact_boundary". preTokens tells us how
// much context was in the window immediately before the compact;
// trigger distinguishes user-initiated /compact from the automatic
// near-limit collapse Claude Code does on its own.
type claudeCompactMeta struct {
	Trigger   string `json:"trigger"`
	PreTokens int64  `json:"preTokens"`
}

type claudeRawMsg struct {
	Role  string           `json:"role"`
	Model string           `json:"model"`
	Usage *claudeRawUsage  `json:"usage"`
}

type claudeRawUsage struct {
	// InputTokens is the FRESH portion of the prompt — typically very
	// small (often 1) when the conversation is using prompt caching.
	// On its own it is NOT a meaningful signal of context size.
	InputTokens int64 `json:"input_tokens"`
	// CacheReadInputTokens carries the bulk of a cached prompt and is
	// usually 90%+ of the real context size for any non-trivial turn.
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
	// CacheCreationInputTokens is the portion that wrote new entries
	// to the prompt cache on this turn. Counted toward total context
	// because it occupied the prompt window before being cached.
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// total returns the cache-aware prompt size — the value agentdeck
// stores in messages.tokens_in (see internal/connectors/claude/parse.go
// lines 244-246). This is the canonical "what was actually in this
// turn's context window" number; raw input_tokens is misleading on
// its own when prompt caching is active.
func (u *claudeRawUsage) total() int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// LatestAssistantTokens scans a Claude Code JSONL transcript and returns
// the input_tokens, model id, and uuid of the most-recent (by RFC 3339
// timestamp) assistant message that carries a non-zero usage block.
//
// Returns the zero value of SourceTokens (with SessionID populated when
// the file has any session-bearing line) when no qualifying assistant
// message exists. Malformed lines are silently skipped — real
// transcripts contain stray bytes after CLI crashes, and an audit tool
// that errors on the first bad line is useless.
//
// The function returns an error only when the file itself cannot be
// opened (permissions, missing path).
func LatestAssistantTokens(path string) (SourceTokens, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from explicit user input or audit walker
	if err != nil {
		return SourceTokens{}, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	var (
		out          SourceTokens
		bestTs       int64
		bestSet      bool
		// Larger than the default bufio scan token so transcripts with
		// huge tool_result blobs don't hard-error mid-scan. 16 MiB is
		// well above any line we have observed in the wild.
		scanBuf      = make([]byte, 0, 64*1024)
		maxScanToken = 16 * 1024 * 1024
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	// fallbackSessionID is set from any non-empty session-bearing line,
	// used only when no qualifying assistant message ever appears (so
	// callers can still see "user-only.jsonl belonged to session X").
	var fallbackSessionID string

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw claudeRawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // malformed — skip, don't fail the audit
		}
		if fallbackSessionID == "" && raw.SessionID != "" {
			fallbackSessionID = raw.SessionID
		}
		if raw.Type != "assistant" {
			continue
		}
		if raw.Message == nil || raw.Message.Usage == nil {
			continue
		}
		// Skip when the entire prompt window is empty — fresh + cache_read
		// + cache_creation all zero. Keeping a row with input_tokens=1
		// alone is correct because the cached portion still counts.
		total := raw.Message.Usage.total()
		if total <= 0 {
			continue
		}
		ts := parseClaudeTS(raw.Timestamp)
		if !bestSet || ts > bestTs {
			bestSet = true
			bestTs = ts
			out.Tokens = total
			out.Model = raw.Message.Model
			out.MessageID = raw.UUID
			// Crucial: take the session id from the SAME row whose
			// tokens we are reporting, so a stray cross-reference line
			// (e.g. a continuation that mentions another session) does
			// not mislabel the audit row. A JSONL file can host more
			// than one logical session; v1 reports only the dominant
			// (latest-by-timestamp) one, which is what the user sees
			// in agentdeck's session-page view.
			out.SessionID = raw.SessionID
		}
	}
	if out.SessionID == "" {
		out.SessionID = fallbackSessionID
	}
	// Scanner errors after a successful pass are non-fatal — we have
	// what we have. Surface the error for diagnostic logs only.
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan transcript: %w", err)
	}
	return out, nil
}

// CompactBoundaryStats walks the transcript at path and counts every
// /compact event, splitting by trigger (manual vs auto) and summing
// `compactMetadata.preTokens` so the cross-session insight report can
// quantify "how much context this user has had compacted away."
//
// Returns the zero CompactStats when no compact_boundary line is
// present. Malformed lines are skipped silently — same tolerance
// principle as LatestAssistantTokens.
func CompactBoundaryStats(path string) (CompactStats, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return CompactStats{}, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	var (
		out          CompactStats
		scanBuf      = make([]byte, 0, 64*1024)
		maxScanToken = 16 * 1024 * 1024
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap pre-filter: avoid full JSON parse on the 99% of lines
		// that have nothing to do with /compact. Keeps the audit
		// scanner fast over large transcripts (up to multi-MB lines).
		if !bytesContains(line, "compact_boundary") {
			continue
		}
		var raw claudeRawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Type != "system" || raw.Subtype != "compact_boundary" {
			continue
		}
		out.Count++
		trigger := ""
		var preTokens int64
		if raw.Compact != nil {
			trigger = raw.Compact.Trigger
			preTokens = raw.Compact.PreTokens
		}
		switch trigger {
		case "manual":
			out.Manual++
		case "auto":
			out.Auto++
		}
		out.TotalPreTokens += preTokens
		if preTokens > out.LargestPreTokens {
			out.LargestPreTokens = preTokens
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan transcript: %w", err)
	}
	return out, nil
}

// bytesContains is a tiny inlined substring search used by the
// pre-filter above. Avoids pulling in `bytes` for one call site and is
// fast enough for the JSONL scan loop.
func bytesContains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	n := len(needle)
	for i := 0; i+n <= len(haystack); i++ {
		match := true
		for j := 0; j < n; j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// parseClaudeTS converts the RFC 3339 timestamp Claude writes into an
// epoch-millisecond int64. Returns 0 when the input is empty or
// unparseable; the caller treats 0 as "before any real timestamp,"
// which is correct because zero-tokens lines are skipped before this
// function is reached.
func parseClaudeTS(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fallback: try the second-precision variant just in case a
		// connector ever stripped the fractional seconds.
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}
