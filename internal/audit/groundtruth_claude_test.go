package audit

import (
	"os"
	"path/filepath"
	"testing"
)

// writeJSONL drops a fixture transcript on disk for a single test and
// returns its absolute path. The fixture is rooted in t.TempDir() so it
// is auto-cleaned. Each line of body is written as-is plus a newline.
func writeJSONL(t *testing.T, name string, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func TestLatestAssistantTokens_EmptyFile(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "empty.jsonl")
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v, want nil for empty file", err)
	}
	if got.Tokens != 0 || got.Model != "" || got.MessageID != "" {
		t.Errorf("got = %+v, want zero value for empty file", got)
	}
}

func TestLatestAssistantTokens_NoAssistantMessages(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "user-only.jsonl",
		`{"type":"user","sessionId":"s1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1 (must come from line itself)", got.SessionID)
	}
	if got.Tokens != 0 {
		t.Errorf("Tokens = %d, want 0 for user-only transcript", got.Tokens)
	}
}

func TestLatestAssistantTokens_SingleAssistantTurn(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "single-asst.jsonl",
		`{"type":"user","sessionId":"s2","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","sessionId":"s2","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":12345,"output_tokens":67}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 12345 {
		t.Errorf("Tokens = %d, want 12345", got.Tokens)
	}
	if got.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", got.Model)
	}
	if got.MessageID != "a1" {
		t.Errorf("MessageID = %q, want a1", got.MessageID)
	}
	if got.SessionID != "s2" {
		t.Errorf("SessionID = %q, want s2", got.SessionID)
	}
}

func TestLatestAssistantTokens_PicksLatestByTimestamp(t *testing.T) {
	t.Parallel()
	// Three assistant turns; the last one (chronologically) is the
	// signal we want, regardless of file order.
	p := writeJSONL(t, "three-asst.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100}}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a3","timestamp":"2026-04-08T10:00:30.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":300}}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a2","timestamp":"2026-04-08T10:00:15.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":200}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 300 {
		t.Errorf("Tokens = %d, want 300 (the latest by timestamp)", got.Tokens)
	}
	if got.MessageID != "a3" {
		t.Errorf("MessageID = %q, want a3", got.MessageID)
	}
}

func TestLatestAssistantTokens_SkipsZeroUsage(t *testing.T) {
	t.Parallel()
	// Assistant with input_tokens=0 should be ignored — those rows
	// represent tool-use continuations the CLI already billed against
	// an earlier prompt.
	p := writeJSONL(t, "zero-usage.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":500}}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a2","timestamp":"2026-04-08T10:00:10.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":0}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 500 {
		t.Errorf("Tokens = %d, want 500 (zero-usage row skipped)", got.Tokens)
	}
	if got.MessageID != "a1" {
		t.Errorf("MessageID = %q, want a1", got.MessageID)
	}
}

func TestLatestAssistantTokens_TolerantOfMalformedLines(t *testing.T) {
	t.Parallel()
	// Real transcripts include garbage / partial lines after crashes.
	// The extractor must skip them, not error out.
	p := writeJSONL(t, "garbage.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":777}}}`,
		`{"this is not valid JSON`,
		``, // blank line
		`{"type":"file-history-snapshot","sessionId":"s","timestamp":"2026-04-08T10:00:05.000Z"}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v, want nil (malformed lines should be skipped)", err)
	}
	if got.Tokens != 777 {
		t.Errorf("Tokens = %d, want 777", got.Tokens)
	}
}

func TestLatestAssistantTokens_SumsCacheTokens(t *testing.T) {
	t.Parallel()
	// Real Claude Code transcripts (verified on 2026-05-08 against
	// ~/.claude/projects) report input_tokens=1 with the bulk in
	// cache_read + cache_creation. The audit must surface the
	// cache-aware total (matches what the connector parser stores
	// in messages.tokens_in at parse.go:244-246), not the raw
	// input_tokens.
	p := writeJSONL(t, "cached.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":290914,"cache_creation_input_tokens":1261}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := int64(1 + 290914 + 1261)
	if got.Tokens != want {
		t.Errorf("Tokens = %d, want %d (cache-aware sum)", got.Tokens, want)
	}
}

func TestLatestAssistantTokens_AllCacheZeroSkipped(t *testing.T) {
	t.Parallel()
	// All three usage fields zero → total zero → row must be skipped
	// even though the message exists.
	p := writeJSONL(t, "all-zero.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":500,"cache_read_input_tokens":1500}}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a2","timestamp":"2026-04-08T10:00:10.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 2000 {
		t.Errorf("Tokens = %d, want 2000 (latest non-empty turn)", got.Tokens)
	}
}

func TestLatestAssistantTokens_MultipleSessionsInFile(t *testing.T) {
	t.Parallel()
	// Real-world transcript files occasionally carry stray
	// cross-references to other session ids. The audit must report
	// the SessionID of the dominant (latest-by-timestamp assistant
	// row) session, not the first session id that happens to appear.
	// Discovered on 2026-05-08 against a real ~/.claude/projects file
	// where one stray line mis-attributed an entire 364K-token
	// transcript.
	p := writeJSONL(t, "two-sessions.jsonl",
		// First non-empty session id is "stray-session" — must NOT be
		// chosen as the audit row's identity.
		`{"type":"user","sessionId":"stray-session","timestamp":"2026-04-08T09:59:00.000Z","message":{"role":"user","content":"oops"}}`,
		// Real assistant rows belong to "primary-session".
		`{"type":"assistant","sessionId":"primary-session","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":50000}}}`,
		`{"type":"assistant","sessionId":"primary-session","uuid":"a2","timestamp":"2026-04-08T10:00:10.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":75000}}}`,
	)
	got, err := LatestAssistantTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.SessionID != "primary-session" {
		t.Errorf("SessionID = %q, want primary-session (must come from dominant assistant row, not first stray reference)", got.SessionID)
	}
	if got.Tokens != 75001 {
		t.Errorf("Tokens = %d, want 75001 (latest primary-session turn)", got.Tokens)
	}
}

func TestLatestAssistantTokens_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LatestAssistantTokens("/nonexistent.klyne-audit/no.jsonl")
	if err == nil {
		t.Fatalf("err = nil, want non-nil for missing file")
	}
}

func TestCompactBoundaryStats_NoCompacts(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "no-compacts.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a","timestamp":"2026-04-08T10:00:01.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":100}}}`,
	)
	got, err := CompactBoundaryStats(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 0 || got.TotalPreTokens != 0 {
		t.Errorf("expected zero stats, got %+v", got)
	}
}

func TestCompactBoundaryStats_SingleManualCompact(t *testing.T) {
	t.Parallel()
	// Real-world record shape verified against ~/.claude/projects on
	// 2026-05-08: type=system, subtype=compact_boundary, with
	// compactMetadata.trigger and compactMetadata.preTokens.
	p := writeJSONL(t, "one-manual.jsonl",
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c1","timestamp":"2026-04-13T03:51:55.506Z","compactMetadata":{"trigger":"manual","preTokens":826799}}`,
	)
	got, err := CompactBoundaryStats(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
	if got.Manual != 1 {
		t.Errorf("Manual = %d, want 1", got.Manual)
	}
	if got.Auto != 0 {
		t.Errorf("Auto = %d, want 0", got.Auto)
	}
	if got.TotalPreTokens != 826799 {
		t.Errorf("TotalPreTokens = %d, want 826799", got.TotalPreTokens)
	}
	if got.LargestPreTokens != 826799 {
		t.Errorf("LargestPreTokens = %d, want 826799", got.LargestPreTokens)
	}
}

func TestCompactBoundaryStats_MultipleEventsMixedTrigger(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "mixed.jsonl",
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c1","timestamp":"2026-04-08T10:00:00.000Z","compactMetadata":{"trigger":"manual","preTokens":300000}}`,
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c2","timestamp":"2026-04-08T11:00:00.000Z","compactMetadata":{"trigger":"auto","preTokens":750000}}`,
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c3","timestamp":"2026-04-08T12:00:00.000Z","compactMetadata":{"trigger":"manual","preTokens":250000}}`,
	)
	got, err := CompactBoundaryStats(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 3 || got.Manual != 2 || got.Auto != 1 {
		t.Errorf("counts wrong: %+v", got)
	}
	if got.TotalPreTokens != 1300000 {
		t.Errorf("TotalPreTokens = %d, want 1300000", got.TotalPreTokens)
	}
	if got.LargestPreTokens != 750000 {
		t.Errorf("LargestPreTokens = %d, want 750000 (the auto one)", got.LargestPreTokens)
	}
}

func TestCompactBoundaryStats_IgnoresSimilarSubtypes(t *testing.T) {
	t.Parallel()
	// Other system subtypes (turn_duration, stop_hook_summary, etc.)
	// must not be miscounted as compacts.
	p := writeJSONL(t, "other-systems.jsonl",
		`{"type":"system","subtype":"turn_duration","sessionId":"s","uuid":"t1","timestamp":"2026-04-08T10:00:00.000Z","durationMs":1000,"messageCount":5}`,
		`{"type":"system","subtype":"stop_hook_summary","sessionId":"s","uuid":"h1","timestamp":"2026-04-08T10:01:00.000Z","hookCount":3}`,
	)
	got, err := CompactBoundaryStats(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 0 {
		t.Errorf("Count = %d, want 0 (no compact_boundary lines)", got.Count)
	}
}

func TestCompactBoundaryStats_MissingMetadata(t *testing.T) {
	t.Parallel()
	// A compact_boundary line without compactMetadata still counts
	// (older Claude Code versions may have shipped a leaner record),
	// but contributes 0 to TotalPreTokens.
	p := writeJSONL(t, "no-meta.jsonl",
		`{"type":"system","subtype":"compact_boundary","sessionId":"s","uuid":"c1","timestamp":"2026-04-08T10:00:00.000Z"}`,
	)
	got, err := CompactBoundaryStats(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
	if got.TotalPreTokens != 0 {
		t.Errorf("TotalPreTokens = %d, want 0", got.TotalPreTokens)
	}
}
