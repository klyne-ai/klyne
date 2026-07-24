package handlers

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/productivity"
)

func TestRelevantPullRequests_UsesBranchTicketAndExplicitReference(t *testing.T) {
	svc := productivity.Service{
		Branches: []productivity.Branch{
			{Name: "feature/CLI-101-checkout", TicketID: "CLI-101"},
		},
		WhatWasDone: &productivity.WhatWasDoneCard{
			Narrative: &productivity.WWDNarrative{Cards: []productivity.WWDCard{
				{
					Kind: "IN_PROGRESS", TicketID: "CLI-202", Title: "Refund work",
					Refs: []productivity.WWDRef{{Type: "pr", Text: "PR #33"}},
				},
			}},
		},
	}
	candidates := []productivity.PullRequest{
		{Number: 11, HeadRef: "feature/CLI-101-checkout", Title: "Checkout"},
		{Number: 22, HeadRef: "feature/misc", Title: "CLI-202 refund support"},
		{Number: 33, HeadRef: "feature/unrelated", Title: "Explicitly referenced"},
		{Number: 44, HeadRef: "feature/unrelated", Title: "Unrelated"},
	}

	got := relevantPullRequests(svc, candidates)
	numbers := map[int]bool{}
	for _, pr := range got {
		numbers[pr.Number] = true
	}
	for _, want := range []int{11, 22, 33} {
		if !numbers[want] {
			t.Fatalf("associated PR #%d missing; got %+v", want, got)
		}
	}
	if numbers[44] {
		t.Fatalf("unrelated PR was associated: %+v", got)
	}
}

func TestReconcileCardPRRefs_VerifiesAndAddsTicketPRs(t *testing.T) {
	svc := productivity.Service{
		PullRequests: []productivity.PullRequest{
			{Number: 33, URL: "https://example.test/pr/33", State: "OPEN", ReviewDecision: "CHANGES_REQUESTED", HeadRef: "misc"},
			{Number: 44, URL: "https://example.test/pr/44", State: "OPEN", ReviewDecision: "APPROVED", HeadRef: "feature/CLI-202-refund"},
		},
		WhatWasDone: &productivity.WhatWasDoneCard{
			Narrative: &productivity.WWDNarrative{Cards: []productivity.WWDCard{{
				Kind: "IN_PROGRESS", TicketID: "CLI-202", Title: "Refund work",
				Refs: []productivity.WWDRef{{Type: "pr", Text: "PR #33"}},
			}}},
		},
	}

	reconcileCardPRRefs(&svc)

	refs := svc.WhatWasDone.Narrative.Cards[0].Refs
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want explicit and ticket-associated PRs", refs)
	}
	byText := map[string]productivity.WWDRef{}
	for _, ref := range refs {
		byText[ref.Text] = ref
	}
	if !byText["PR #33"].Verified || byText["PR #33"].ReviewDecision != "CHANGES_REQUESTED" {
		t.Fatalf("explicit PR was not verified: %+v", byText["PR #33"])
	}
	if !byText["PR #44"].Verified || byText["PR #44"].ReviewDecision != "APPROVED" {
		t.Fatalf("ticket-associated PR was not added: %+v", byText["PR #44"])
	}
}
