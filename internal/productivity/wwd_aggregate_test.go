package productivity

import "testing"

func TestMergeWhatWasDoneCards_PreservesEveryDayAndOutcome(t *testing.T) {
	first := &WhatWasDoneCard{
		Service: "orders", Day: "2026-07-23", LLMCompiled: true,
		Narrative: &WWDNarrative{Cards: []WWDCard{
			{Kind: "SHIPPED", TicketID: "CLI-100", Title: "Checkout shipped", Day: "2026-07-23", CLIs: []string{"claude"}},
			{Kind: "FIXED", TicketID: "CLI-100", Title: "Checkout regression fixed", Day: "2026-07-23", CLIs: []string{"claude"}},
		}},
	}
	second := &WhatWasDoneCard{
		Service: "orders", Day: "2026-07-24", LLMCompiled: true,
		Narrative: &WWDNarrative{Cards: []WWDCard{
			{Kind: "IN_PROGRESS", TicketID: "CLI-200", Title: "Refund work pending", Day: "2026-07-24", CLIs: []string{"codex"}},
		}},
	}

	got := MergeWhatWasDoneCards(first, second)

	if got == nil || got.Narrative == nil || len(got.Narrative.Cards) != 3 {
		t.Fatalf("merged card = %+v, want all three daily outcomes", got)
	}
	if got.Day != "2026-07-24" {
		t.Fatalf("day = %q, want latest day", got.Day)
	}
	if got.Narrative.Stats.Shipped != 1 || got.Narrative.Stats.Fixed != 1 || got.Narrative.Stats.InProgress != 1 {
		t.Fatalf("stats = %+v, want one shipped/fixed/in-progress", got.Narrative.Stats)
	}
	if got.Narrative.Cards[0].Day != "2026-07-24" || got.Narrative.Cards[0].CLIs[0] != "codex" {
		t.Fatalf("latest Codex outcome not first: %+v", got.Narrative.Cards)
	}
}
