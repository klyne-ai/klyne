package productivity

import "testing"

func TestReconcileOpenWork_CombinesSummaryGitAndPRState(t *testing.T) {
	svc := Service{
		Repo: "orders",
		WhatWasDone: &WhatWasDoneCard{
			Service: "orders",
			Narrative: &WWDNarrative{Cards: []WWDCard{
				{
					Kind: "IN_PROGRESS", TicketID: "CLI-100", Title: "Checkout validation is blocked",
					Body: "Waiting on the schema update.", CLIs: []string{"codex"}, Day: "2026-07-24",
				},
				{
					Kind: "SHIPPED", TicketID: "CLI-200", Title: "Old work shipped",
					Body: "The implementation landed.", Day: "2026-07-24",
				},
				{
					Kind: "MAJOR", TicketID: "CLI-200", Title: "Old implementation work",
					Body: "Implementation was in progress.", Day: "2026-07-23",
				},
			}},
		},
		Risks: []RiskSignal{{
			Kind: "unpushed", Branch: "feature/CLI-300", WorktreePath: "/orders",
			Detail: "Two commits are not pushed",
		}},
		PullRequests: []PullRequest{
			{Number: 41, Title: "CLI-400 checkout", State: "OPEN", ReviewDecision: "CHANGES_REQUESTED", URL: "https://example.test/pr/41"},
			{Number: 42, Title: "CLI-500 ready", State: "OPEN", ReviewDecision: "APPROVED", URL: "https://example.test/pr/42"},
			{Number: 43, Title: "CLI-600 merged", State: "MERGED"},
		},
	}

	ReconcileOpenWork(&svc)

	statusByKey := map[string]string{}
	for _, item := range svc.OpenItems {
		statusByKey[item.Key] = item.Status
	}
	if statusByKey["summary:CLI-100:IN_PROGRESS:Checkout validation is blocked"] != "blocked" {
		t.Fatalf("blocked summary missing or misclassified: %+v", svc.OpenItems)
	}
	if _, found := statusByKey["summary:CLI-200:MAJOR:Old implementation work"]; found {
		t.Fatalf("older open state survived a later shipped outcome: %+v", svc.OpenItems)
	}
	if statusByKey["git:unpushed:/orders:feature/CLI-300"] != "pending" {
		t.Fatalf("unpushed work missing: %+v", svc.OpenItems)
	}
	if statusByKey["pr:41"] != "blocked" || statusByKey["pr:42"] != "ready_to_merge" {
		t.Fatalf("review states not reconciled: %+v", svc.OpenItems)
	}
	if _, found := statusByKey["pr:43"]; found {
		t.Fatalf("merged PR must not remain open: %+v", svc.OpenItems)
	}
	if len(svc.WhatWasDone.OpenItems) != len(svc.OpenItems) {
		t.Fatalf("card open items = %d, service open items = %d", len(svc.WhatWasDone.OpenItems), len(svc.OpenItems))
	}
}

func TestReconcileOpenWork_CreatesOpenOnlyDashboardCard(t *testing.T) {
	svc := Service{
		Repo: "billing",
		PullRequests: []PullRequest{{
			Number: 9, Title: "Draft billing migration", State: "OPEN", IsDraft: true,
		}},
	}

	ReconcileOpenWork(&svc)

	if svc.WhatWasDone == nil || svc.WhatWasDone.Narrative == nil {
		t.Fatal("open-only service must receive a dashboard card")
	}
	if len(svc.OpenItems) != 1 || svc.OpenItems[0].Status != "pending" {
		t.Fatalf("open items = %+v, want one pending draft PR", svc.OpenItems)
	}
}
