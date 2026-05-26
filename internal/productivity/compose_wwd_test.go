package productivity

import (
	"reflect"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

// mkReflection builds a store.Reflection carrying a typed body_json
// payload — the §1.1 stored shape — so the table-driven cases below
// can spell out the wire payload literally instead of constructing
// struct trees.
func mkReflection(t *testing.T, projectPath, jsonBody string) store.Reflection {
	t.Helper()
	return store.Reflection{
		ID:               "ref-" + projectPath,
		ProjectPath:      projectPath,
		Day:              "2026-05-26",
		BodyJSON:         jsonBody,
		EvidenceEntryIDs: []string{"e1"},
	}
}

func TestComposeWWD_Empty(t *testing.T) {
	if got := ComposeWWD(nil); got != nil {
		t.Fatalf("ComposeWWD(nil) = %v, want nil", got)
	}
	if got := ComposeWWD([]store.Reflection{}); got != nil {
		t.Fatalf("ComposeWWD(empty) = %v, want nil", got)
	}
}

func TestComposeWWD_LegacyProseRowsSkipped(t *testing.T) {
	// Rows without body_json — the legacy prose-only path — must
	// produce zero cards, no error, no panic.
	rows := []store.Reflection{
		{ID: "ref-1", ProjectPath: "/p", Day: "2026-05-26", BodyJSON: ""},
		{ID: "ref-2", ProjectPath: "/p", Day: "2026-05-26", BodyJSON: "   "},
	}
	if got := ComposeWWD(rows); got != nil {
		t.Fatalf("ComposeWWD(legacy-only) = %v, want nil", got)
	}
}

func TestComposeWWD_SingleRow_BasicShape(t *testing.T) {
	body := `{
	  "service": "klyne",
	  "details": [
	    {"kind": "SHIPPED", "when": "16:49", "text": "Wrote ~/.codex/hooks.json with klyne-hook entries; regression test passes.", "evidence": ["be8cc8c8", "~/.codex/hooks.json"], "session_id": "sess-1"}
	  ]
	}`
	cards := ComposeWWD([]store.Reflection{mkReflection(t, "/repo/klyne", body)})
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.Service != "klyne" {
		t.Errorf("Service = %q, want klyne", c.Service)
	}
	if c.Tier1.PillCounts["shipped"] != 1 {
		t.Errorf("pill_counts[shipped] = %d, want 1", c.Tier1.PillCounts["shipped"])
	}
	if !reflect.DeepEqual(c.Tier1.TopEvidence, []string{"be8cc8c8"}) {
		t.Errorf("TopEvidence = %v, want [be8cc8c8]", c.Tier1.TopEvidence)
	}
	if c.Tier1.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", c.Tier1.TurnCount)
	}
	if c.Tier1.CommitCount != 1 {
		t.Errorf("CommitCount = %d, want 1", c.Tier1.CommitCount)
	}
	if !strings.HasPrefix(c.Tier1.TLDR, "1 shipped") {
		t.Errorf("TLDR = %q, want it to start with '1 shipped'", c.Tier1.TLDR)
	}
	if !strings.Contains(c.Tier1.TLDR, "latest:") {
		t.Errorf("TLDR = %q, missing 'latest:'", c.Tier1.TLDR)
	}
	if len(c.Tier2.Details) != 1 {
		t.Errorf("Tier2.Details count = %d, want 1", len(c.Tier2.Details))
	}
}

func TestComposeWWD_MultiRow_KindOrderingAndNewestFirstWithinKind(t *testing.T) {
	// Two rows for the same service. Mix kinds so the test asserts BOTH
	// the cross-kind ordering (SHIPPED → MAJOR → FIXED → DECISION →
	// INVESTIGATED → IN_PROGRESS) AND the within-kind newest-first
	// ordering by When.
	row1 := mkReflection(t, "/repo/klyne", `{
	  "service": "klyne",
	  "details": [
	    {"kind": "FIXED", "when": "10:00", "text": "fixed early", "evidence": ["a1b2c3d4"], "session_id": "sess-a"},
	    {"kind": "SHIPPED", "when": "11:00", "text": "shipped earlier", "evidence": ["1111111"], "session_id": "sess-a"}
	  ]
	}`)
	row2 := mkReflection(t, "/repo/klyne", `{
	  "service": "klyne",
	  "details": [
	    {"kind": "SHIPPED", "when": "15:30", "text": "newest shipped land", "evidence": ["2222222"], "session_id": "sess-b"},
	    {"kind": "FIXED", "when": "12:00", "text": "fixed later", "evidence": ["3333333"], "session_id": "sess-b"},
	    {"kind": "DECISION", "when": "09:15", "text": "decided thing", "evidence": ["doc-1"], "session_id": "sess-b"}
	  ]
	}`)
	cards := ComposeWWD([]store.Reflection{row1, row2})
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	want := []string{"SHIPPED", "SHIPPED", "FIXED", "FIXED", "DECISION"}
	got := make([]string, 0, len(cards[0].Tier2.Details))
	for _, d := range cards[0].Tier2.Details {
		got = append(got, d.Kind)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kind order = %v, want %v", got, want)
	}
	// Within SHIPPED: 15:30 must come before 11:00.
	if cards[0].Tier2.Details[0].When != "15:30" {
		t.Errorf("first SHIPPED When = %q, want 15:30", cards[0].Tier2.Details[0].When)
	}
	if cards[0].Tier2.Details[1].When != "11:00" {
		t.Errorf("second SHIPPED When = %q, want 11:00", cards[0].Tier2.Details[1].When)
	}
	// turn_count = 2 distinct session_ids.
	if cards[0].Tier1.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", cards[0].Tier1.TurnCount)
	}
}

func TestComposeWWD_UnknownKindDropped(t *testing.T) {
	body := `{
	  "service": "klyne",
	  "details": [
	    {"kind": "RANDOM_BOGUS", "when": "10:00", "text": "should not appear", "evidence": ["abc1234"], "session_id": "x"},
	    {"kind": "SHIPPED", "when": "11:00", "text": "real one", "evidence": ["def4567"], "session_id": "x"}
	  ]
	}`
	cards := ComposeWWD([]store.Reflection{mkReflection(t, "/repo/klyne", body)})
	if len(cards) != 1 || len(cards[0].Tier2.Details) != 1 {
		t.Fatalf("expected 1 card with 1 detail, got %+v", cards)
	}
	if cards[0].Tier2.Details[0].Kind != "SHIPPED" {
		t.Errorf("kept detail kind = %q, want SHIPPED", cards[0].Tier2.Details[0].Kind)
	}
}

func TestComposeWWD_CommitShaDetection_AndEvidenceDedup(t *testing.T) {
	// Multiple details share the same sha — top_evidence dedups it.
	// Non-sha tokens (paths, test names) are skipped from top_evidence
	// but counted in evidence overall (not in commit_count).
	body := `{
	  "service": "klyne",
	  "details": [
	    {"kind": "SHIPPED", "when": "10:00", "text": "first", "evidence": ["be8cc8c8", "internal/foo.go"], "session_id": "s1"},
	    {"kind": "SHIPPED", "when": "11:00", "text": "second", "evidence": ["620a9551", "be8cc8c8", "TestFoo"], "session_id": "s1"},
	    {"kind": "SHIPPED", "when": "12:00", "text": "third", "evidence": ["019e6405", "ZZZZ_not_hex"], "session_id": "s2"},
	    {"kind": "SHIPPED", "when": "13:00", "text": "fourth — beyond the cap", "evidence": ["aaaaaaa"], "session_id": "s2"}
	  ]
	}`
	cards := ComposeWWD([]store.Reflection{mkReflection(t, "/repo/klyne", body)})
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	te := cards[0].Tier1.TopEvidence
	// First three shas in encounter order (after sortDetails newest-first):
	// 13:00 aaaaaaa, 12:00 019e6405, 11:00 620a9551 (be8cc8c8 dedup'd later).
	if len(te) != 3 {
		t.Fatalf("TopEvidence count = %d, want 3 (got %v)", len(te), te)
	}
	if cards[0].Tier1.CommitCount < 4 {
		t.Errorf("CommitCount = %d, want >= 4 distinct shas (got %v)", cards[0].Tier1.CommitCount, te)
	}
}

func TestComposeWWD_TLDR_DropsZeroCountsAndTruncates(t *testing.T) {
	// One SHIPPED, no FIXED — TLDR must NOT mention "fixed".
	longText := strings.Repeat("very long detail text that absolutely must be truncated on a word boundary ", 4)
	body := `{
	  "service": "svc-a",
	  "details": [
	    {"kind": "SHIPPED", "when": "10:00", "text": "` + longText + `", "evidence": ["abc1234"], "session_id": "s1"}
	  ]
	}`
	cards := ComposeWWD([]store.Reflection{mkReflection(t, "/repo/svc-a", body)})
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	tldr := cards[0].Tier1.TLDR
	if strings.Contains(tldr, "fixed") {
		t.Errorf("TLDR mentions 'fixed' for zero-count kind: %q", tldr)
	}
	if !strings.Contains(tldr, "shipped") {
		t.Errorf("TLDR missing 'shipped': %q", tldr)
	}
	if !strings.HasSuffix(tldr, "…") {
		t.Errorf("TLDR not truncated with ellipsis: %q", tldr)
	}
	// Latest clause length bounded — strip prefix and check ≤ tldrTextCap+2 (ellipsis).
	if idx := strings.Index(tldr, "latest: "); idx >= 0 {
		latest := tldr[idx+len("latest: "):]
		// ellipsis is a multi-byte rune; cap is byte-count of trimmed prefix
		// plus the ellipsis suffix.
		if len(latest) > tldrTextCap+8 {
			t.Errorf("latest clause too long (%d bytes): %q", len(latest), latest)
		}
	}
}

func TestComposeWWD_MultipleServices_SortedAlphabetically(t *testing.T) {
	// Two rows for two DIFFERENT services on the same day. The composer
	// returns one card per service in alphabetical service order.
	rowB := mkReflection(t, "/repo/zed", `{
	  "service": "zed",
	  "details": [{"kind": "SHIPPED", "when": "10:00", "text": "z thing", "evidence": ["abc1234"], "session_id": "s1"}]
	}`)
	rowA := mkReflection(t, "/repo/apple", `{
	  "service": "apple",
	  "details": [{"kind": "SHIPPED", "when": "10:00", "text": "a thing", "evidence": ["def4567"], "session_id": "s2"}]
	}`)
	cards := ComposeWWD([]store.Reflection{rowB, rowA})
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].Service != "apple" || cards[1].Service != "zed" {
		t.Errorf("services = [%s, %s], want [apple, zed]",
			cards[0].Service, cards[1].Service)
	}
}

func TestComposeWWD_EmptyEvidenceDetailDropped(t *testing.T) {
	body := `{
	  "service": "klyne",
	  "details": [
	    {"kind": "SHIPPED", "when": "10:00", "text": "no evidence", "evidence": [], "session_id": "s1"},
	    {"kind": "SHIPPED", "when": "11:00", "text": "has evidence", "evidence": ["abc1234"], "session_id": "s1"}
	  ]
	}`
	cards := ComposeWWD([]store.Reflection{mkReflection(t, "/repo/klyne", body)})
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	if len(cards[0].Tier2.Details) != 1 {
		t.Errorf("details count = %d, want 1 (unevidenced dropped)", len(cards[0].Tier2.Details))
	}
}

func TestComposeWWD_MalformedJSONSkipped(t *testing.T) {
	rows := []store.Reflection{
		{ID: "ref-bad", ProjectPath: "/p", Day: "2026-05-26", BodyJSON: "{not json"},
		mkReflection(t, "/repo/klyne", `{
		  "service": "klyne",
		  "details": [{"kind": "SHIPPED", "when": "10:00", "text": "ok", "evidence": ["abc1234"], "session_id": "s1"}]
		}`),
	}
	cards := ComposeWWD(rows)
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1 (malformed row skipped)", len(cards))
	}
}
