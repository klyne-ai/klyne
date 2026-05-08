package audit

import (
	"strings"
	"testing"
)

// fakeLookup implements LookupFunc against a map. Tests build the map
// inline so each case fully describes its DB state.
func fakeLookup(m map[string]int64) LookupFunc {
	return func(id string) (int64, bool) {
		v, ok := m[id]
		return v, ok
	}
}

func TestRun_Match(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "match.jsonl",
		`{"type":"assistant","sessionId":"s1","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1000}}}`,
	)
	r := Run([]string{p}, fakeLookup(map[string]int64{"s1": 1000}))

	if r.Total != 1 {
		t.Errorf("Total = %d, want 1", r.Total)
	}
	if r.Matches != 1 {
		t.Errorf("Matches = %d, want 1", r.Matches)
	}
	if r.Mismatches != 0 || r.NotInDB != 0 {
		t.Errorf("unexpected non-zero failure counts: %+v", r)
	}
	row := r.Sessions[0]
	if !row.Match || !row.Found || row.Delta != 0 {
		t.Errorf("row not flagged match: %+v", row)
	}
}

func TestRun_Mismatch(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "mismatch.jsonl",
		`{"type":"assistant","sessionId":"s1","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":297000}}}`,
	)
	// DB has the wrong number — exactly the trust-failure shape.
	r := Run([]string{p}, fakeLookup(map[string]int64{"s1": 200000}))

	if r.Mismatches != 1 || r.Matches != 0 {
		t.Errorf("counts wrong: matches=%d mismatches=%d", r.Matches, r.Mismatches)
	}
	row := r.Sessions[0]
	if row.Match {
		t.Errorf("row flagged match, want mismatch: %+v", row)
	}
	if row.Delta != -97000 {
		t.Errorf("Delta = %d, want -97000", row.Delta)
	}
}

func TestRun_NotInDB(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "ingest-gap.jsonl",
		`{"type":"assistant","sessionId":"never-ingested","uuid":"a1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1500}}}`,
	)
	r := Run([]string{p}, fakeLookup(map[string]int64{}))

	if r.NotInDB != 1 {
		t.Errorf("NotInDB = %d, want 1", r.NotInDB)
	}
	if r.Mismatches != 0 || r.Matches != 0 {
		t.Errorf("misclassified as mismatch/match: %+v", r)
	}
}

func TestRun_SkippedNoAssistant(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "user-only.jsonl",
		`{"type":"user","sessionId":"s1","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`,
	)
	r := Run([]string{p}, fakeLookup(map[string]int64{"s1": 0}))

	if r.SkippedNoAssistant != 1 {
		t.Errorf("SkippedNoAssistant = %d, want 1", r.SkippedNoAssistant)
	}
	if r.Mismatches != 0 || r.Matches != 0 || r.NotInDB != 0 {
		t.Errorf("user-only file should not count as failure: %+v", r)
	}
}

func TestRenderMarkdown_AllPass(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "ok.jsonl",
		`{"type":"assistant","sessionId":"s","uuid":"a","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":42}}}`,
	)
	r := Run([]string{p}, fakeLookup(map[string]int64{"s": 42}))
	md := RenderMarkdown(r)
	if !strings.Contains(md, "All sampled sessions match.") {
		t.Errorf("expected pass banner in:\n%s", md)
	}
	if strings.Contains(md, "Mismatches") {
		t.Errorf("Mismatches section should not appear when there are none:\n%s", md)
	}
}

func TestRenderMarkdown_FlagsMismatchTable(t *testing.T) {
	t.Parallel()
	p := writeJSONL(t, "bad.jsonl",
		`{"type":"assistant","sessionId":"abcdef1234","uuid":"a","timestamp":"2026-04-08T10:00:00.000Z","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":297000}}}`,
	)
	r := Run([]string{p}, fakeLookup(map[string]int64{"abcdef1234": 200000}))
	md := RenderMarkdown(r)
	if !strings.Contains(md, "Mismatches") {
		t.Errorf("expected Mismatches table:\n%s", md)
	}
	if !strings.Contains(md, "abcdef12") {
		t.Errorf("expected truncated session id in table:\n%s", md)
	}
	if !strings.Contains(md, "-97000") {
		t.Errorf("expected delta in table:\n%s", md)
	}
}
