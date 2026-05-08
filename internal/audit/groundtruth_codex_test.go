package audit

import "testing"

func TestLatestCodexTokens_EmptyFile(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "empty.jsonl")
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 0 || got.SessionID != "" {
		t.Errorf("got = %+v, want zero", got)
	}
}

func TestLatestCodexTokens_SessionMetaOnly(t *testing.T) {
	t.Parallel()
	// session_meta carries the canonical id + model. Verified against
	// real ~/.codex/sessions data on 2026-05-08.
	p := writeJSONL(t, "meta-only.jsonl",
		`{"timestamp":"2026-05-07T17:46:56.247Z","type":"session_meta","payload":{"id":"019e038a-2082-7640-b175-6e6d350d5f98","model":"gpt-5.5"}}`,
	)
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.SessionID != "019e038a-2082-7640-b175-6e6d350d5f98" {
		t.Errorf("SessionID = %q, want canonical id from session_meta", got.SessionID)
	}
	if got.Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5", got.Model)
	}
	if got.Tokens != 0 {
		t.Errorf("Tokens = %d, want 0 (no token_count event)", got.Tokens)
	}
}

func TestLatestCodexTokens_TokenCountWithInfo(t *testing.T) {
	t.Parallel()
	// Verified payload shape from real Codex data — input_tokens
	// already includes cached_input_tokens.
	p := writeJSONL(t, "with-tokens.jsonl",
		`{"timestamp":"2026-05-07T17:46:56.247Z","type":"session_meta","payload":{"id":"sess-1","model":"gpt-5.5"}}`,
		`{"timestamp":"2026-05-07T17:47:01.933Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":17866,"cached_input_tokens":14208,"output_tokens":365,"total_tokens":18231},"model_context_window":258400}}}`,
	)
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 17866 {
		t.Errorf("Tokens = %d, want 17866 (already cache-inclusive per OpenAI contract)", got.Tokens)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", got.SessionID)
	}
}

func TestLatestCodexTokens_PicksLatestByTimestamp(t *testing.T) {
	t.Parallel()
	// Three token_count events; latest by timestamp wins.
	p := writeJSONL(t, "three-events.jsonl",
		`{"timestamp":"2026-05-07T17:47:01.933Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":17866,"cached_input_tokens":14208,"output_tokens":365}}}}`,
		`{"timestamp":"2026-05-07T17:47:16.679Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":30791,"cached_input_tokens":20352,"output_tokens":540}}}}`,
		`{"timestamp":"2026-05-07T17:47:07.403Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20585,"cached_input_tokens":17792,"output_tokens":365}}}}`,
	)
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 30791 {
		t.Errorf("Tokens = %d, want 30791 (latest by timestamp)", got.Tokens)
	}
}

func TestLatestCodexTokens_SkipsEventsWithNullInfo(t *testing.T) {
	t.Parallel()
	// Real Codex transcripts have token_count events early in the
	// session with `info: null` (rate-limit-only signal). Those must
	// be ignored even when they are the most recent line.
	p := writeJSONL(t, "with-null-info.jsonl",
		`{"timestamp":"2026-05-07T17:47:01.933Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000}}}}`,
		`{"timestamp":"2026-05-07T17:50:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":12.0}}}}`,
	)
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 12000 {
		t.Errorf("Tokens = %d, want 12000 (null-info row skipped)", got.Tokens)
	}
}

func TestLatestCodexTokens_TolerantOfMalformedLines(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "garbage.jsonl",
		`{"timestamp":"2026-05-07T17:47:01.933Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":555}}}}`,
		`{not json`,
		``,
		`{"type":"response_item","payload":{"type":"function_call"}}`,
	)
	got, err := LatestCodexTokens(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Tokens != 555 {
		t.Errorf("Tokens = %d, want 555", got.Tokens)
	}
}

func TestCompactBoundaryStatsCodex_NoCompacts(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "no-compacts.jsonl",
		`{"timestamp":"2026-05-07T17:46:56.247Z","type":"session_meta","payload":{"id":"sess-1"}}`,
		`{"timestamp":"2026-05-07T17:47:01.933Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
	)
	got, err := CompactBoundaryStatsCodex(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 0 {
		t.Errorf("Count = %d, want 0", got.Count)
	}
}

func TestCompactBoundaryStatsCodex_CountsCompactedLines(t *testing.T) {
	t.Parallel()
	// Codex's compact marker is `type: "compacted"` — verified against
	// real ~/.codex/sessions data on 2026-05-08. There is no per-event
	// preTokens field, so Manual/Auto/TotalPreTokens stay 0.
	p := writeJSONL(t, "two-compacts.jsonl",
		`{"timestamp":"2026-05-07T18:00:00.000Z","type":"compacted","payload":{"message":"summary text"}}`,
		`{"timestamp":"2026-05-07T20:00:00.000Z","type":"compacted","payload":{"message":"summary text 2"}}`,
	)
	got, err := CompactBoundaryStatsCodex(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	// Codex does not expose pre-token counts; the audit honestly
	// reports zero for these subfields rather than fabricating numbers.
	if got.Manual != 0 || got.Auto != 0 || got.TotalPreTokens != 0 {
		t.Errorf("Codex compact stats should not populate Manual/Auto/PreTokens; got %+v", got)
	}
}

func TestCompactBoundaryStatsCodex_IgnoresOtherTypes(t *testing.T) {
	t.Parallel()
	// An assistant message that happens to mention the word "compacted"
	// in its text content must NOT count.
	p := writeJSONL(t, "false-positive.jsonl",
		`{"timestamp":"2026-05-07T18:00:00.000Z","type":"response_item","payload":{"type":"text","text":"the file was compacted earlier"}}`,
	)
	got, err := CompactBoundaryStatsCodex(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Count != 0 {
		t.Errorf("Count = %d, want 0 (text mention is not a compact event)", got.Count)
	}
}
