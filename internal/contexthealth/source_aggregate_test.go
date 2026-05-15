package contexthealth

import (
	"testing"
)

func TestAggregateSources_Empty(t *testing.T) {
	t.Parallel()
	if got := AggregateSources(nil); got != nil {
		t.Errorf("nil input: got %v, want nil", got)
	}
	if got := AggregateSources([][]SourceRow{}); got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
}

func TestAggregateSources_SingleSession(t *testing.T) {
	t.Parallel()
	rows := []SourceRow{
		{Name: "serena", Kind: SourceKindMCP, Tokens: 18000},
		{Name: "superpowers", Kind: SourceKindSkill, Tokens: 62000},
	}
	got := AggregateSources([][]SourceRow{rows})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; got %+v", len(got), got)
	}
	// Expect sorted by TotalTokens desc — superpowers > serena.
	if got[0].Name != "superpowers" {
		t.Errorf("got[0].Name = %q, want superpowers", got[0].Name)
	}
	if got[0].TotalTokens != 62000 {
		t.Errorf("TotalTokens = %d, want 62000", got[0].TotalTokens)
	}
	if got[0].SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", got[0].SessionCount)
	}
}

func TestAggregateSources_TwoSessions_MergesSameSource(t *testing.T) {
	t.Parallel()
	s1 := []SourceRow{
		{Name: "serena", Kind: SourceKindMCP, Tokens: 10000},
		{Name: "klyne", Kind: SourceKindHook, Tokens: 2000},
	}
	s2 := []SourceRow{
		{Name: "serena", Kind: SourceKindMCP, Tokens: 12000},
	}
	got := AggregateSources([][]SourceRow{s1, s2})
	// serena should be merged; klyne should appear once.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; got %+v", len(got), got)
	}
	byName := map[string]AggregatedRow{}
	for _, r := range got {
		byName[r.Name] = r
	}
	serena := byName["serena"]
	if serena.TotalTokens != 22000 {
		t.Errorf("serena TotalTokens = %d, want 22000", serena.TotalTokens)
	}
	if serena.SessionCount != 2 {
		t.Errorf("serena SessionCount = %d, want 2", serena.SessionCount)
	}
	klyne := byName["klyne"]
	if klyne.SessionCount != 1 {
		t.Errorf("klyne SessionCount = %d, want 1", klyne.SessionCount)
	}
}

func TestAggregateSources_SortedByTotalTokensDesc(t *testing.T) {
	t.Parallel()
	s1 := []SourceRow{
		{Name: "small", Kind: SourceKindMCP, Tokens: 100},
		{Name: "big", Kind: SourceKindSkill, Tokens: 50000},
		{Name: "medium", Kind: SourceKindHook, Tokens: 5000},
	}
	got := AggregateSources([][]SourceRow{s1})
	for i := 1; i < len(got); i++ {
		if got[i-1].TotalTokens < got[i].TotalTokens {
			t.Errorf("not sorted desc at [%d,%d]: %d < %d", i-1, i, got[i-1].TotalTokens, got[i].TotalTokens)
		}
	}
}

func TestRedundancy_Hard(t *testing.T) {
	t.Parallel()
	// Never invoked, loaded in 3+ sessions → hard redundant.
	r := AggregatedRow{
		Name:            "playwright",
		SessionCount:    5,
		InvocationCount: 0,
	}
	if got := r.Redundancy(); got != RedundancyHard {
		t.Errorf("Redundancy() = %v, want RedundancyHard", got)
	}
}

func TestRedundancy_HardRequires3Sessions(t *testing.T) {
	t.Parallel()
	// Never invoked, but only 2 sessions → not hard (too few sessions).
	r := AggregatedRow{
		Name:            "playwright",
		SessionCount:    2,
		InvocationCount: 0,
	}
	if got := r.Redundancy(); got == RedundancyHard {
		t.Errorf("Redundancy() = RedundancyHard, want not-hard for 2 sessions")
	}
}

func TestRedundancy_Soft(t *testing.T) {
	t.Parallel()
	// 1 invocation across 10 sessions → 1*5=5 ≤ 10 → soft redundant.
	r := AggregatedRow{
		Name:            "linear",
		SessionCount:    10,
		InvocationCount: 1,
	}
	if got := r.Redundancy(); got != RedundancySoft {
		t.Errorf("Redundancy() = %v, want RedundancySoft", got)
	}
}

func TestRedundancy_None(t *testing.T) {
	t.Parallel()
	// 20 invocations across 10 sessions → well-used.
	r := AggregatedRow{
		Name:            "klyne",
		SessionCount:    10,
		InvocationCount: 20,
	}
	if got := r.Redundancy(); got != RedundancyNone {
		t.Errorf("Redundancy() = %v, want RedundancyNone", got)
	}
}
