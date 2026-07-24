package productivity

import (
	"fmt"
	"sort"
	"strings"
)

// ReconcileOpenWork derives the service's current actionable items from the
// reconciled summary card, live git risks, and associated GitHub PRs.
func ReconcileOpenWork(svc *Service) {
	items := []OpenWorkItem{}
	terminalTickets := map[string]string{}
	if svc.WhatWasDone != nil && svc.WhatWasDone.Narrative != nil {
		for _, card := range svc.WhatWasDone.Narrative.Cards {
			switch card.Kind {
			case "SHIPPED", "FIXED", "DECISION":
				if card.TicketID != "" && card.Day >= terminalTickets[card.TicketID] {
					terminalTickets[card.TicketID] = card.Day
				}
			}
		}
		for _, card := range svc.WhatWasDone.Narrative.Cards {
			if !isOpenKind(card.Kind) {
				continue
			}
			if terminalDay := terminalTickets[card.TicketID]; card.TicketID != "" && terminalDay != "" && card.Day <= terminalDay &&
				!containsPendingSignal(card.Title+" "+card.Body) {
				continue
			}
			status := openStatusFromText(card.Kind, card.Title+" "+card.Body)
			items = append(items, OpenWorkItem{
				Key:    "summary:" + card.TicketID + ":" + card.Kind + ":" + card.Title,
				Status: status, Title: card.Title, TicketID: card.TicketID,
				Source: "summary", CLIs: append([]string(nil), card.CLIs...),
				Refs: append([]WWDRef(nil), card.Refs...),
			})
		}
		if followup := strings.TrimSpace(svc.WhatWasDone.Narrative.Followup); followup != "" {
			items = append(items, OpenWorkItem{
				Key: "summary:followup:" + followup, Status: openStatusFromText("IN_PROGRESS", followup),
				Title: followup, Source: "summary",
			})
		}
	}

	for _, risk := range svc.Risks {
		status := "in_progress"
		if risk.Kind == "unpushed" {
			status = "pending"
		}
		refs := []WWDRef{}
		if risk.Branch != "" {
			refs = append(refs, WWDRef{Type: "branch", Text: risk.Branch})
		}
		for _, commit := range risk.Commits {
			refs = append(refs, WWDRef{Type: "commit", Text: commit.SHA})
		}
		items = append(items, OpenWorkItem{
			Key:    "git:" + risk.Kind + ":" + risk.WorktreePath + ":" + risk.Branch,
			Status: status, Title: risk.Detail, Source: "git", Refs: refs,
		})
	}

	for _, pr := range svc.PullRequests {
		if strings.EqualFold(pr.State, "MERGED") || strings.EqualFold(pr.State, "CLOSED") {
			continue
		}
		status := "in_review"
		switch {
		case pr.IsDraft:
			status = "pending"
		case strings.EqualFold(pr.ReviewDecision, "CHANGES_REQUESTED"):
			status = "blocked"
		case strings.EqualFold(pr.ReviewDecision, "APPROVED"):
			status = "ready_to_merge"
		}
		items = append(items, OpenWorkItem{
			Key: "pr:" + fmt.Sprint(pr.Number), Status: status,
			Title: pr.Title, Source: "pr",
			Refs: []WWDRef{{
				Type: "pr", Text: fmt.Sprintf("PR #%d", pr.Number), URL: pr.URL,
				Verified: true, PRState: pr.State, ReviewDecision: pr.ReviewDecision,
			}},
		})
	}

	svc.OpenItems = dedupeOpenWork(items)
	if len(svc.OpenItems) > 0 && svc.WhatWasDone == nil {
		svc.WhatWasDone = &WhatWasDoneCard{
			Service: svc.Repo,
			Narrative: &WWDNarrative{
				Cards: []WWDCard{},
			},
		}
	}
	if svc.WhatWasDone != nil {
		svc.WhatWasDone.OpenItems = append([]OpenWorkItem(nil), svc.OpenItems...)
	}
}

func isOpenKind(kind string) bool {
	switch kind {
	case "MAJOR", "IN_PROGRESS", "INVESTIGATED":
		return true
	default:
		return false
	}
}

func containsPendingSignal(text string) bool {
	lower := strings.ToLower(text)
	for _, signal := range []string{
		"pending", "deferred", "not picked", "not started", "not done",
		"follow-up", "follow up", "blocked", "waiting on", "changes requested",
	} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func openStatusFromText(kind, text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "blocked") || strings.Contains(lower, "waiting on") ||
		strings.Contains(lower, "changes requested") || strings.Contains(lower, "cannot proceed") {
		return "blocked"
	}
	if kind == "INVESTIGATED" || strings.Contains(lower, "pending") ||
		strings.Contains(lower, "deferred") || strings.Contains(lower, "not picked") ||
		strings.Contains(lower, "not started") {
		return "pending"
	}
	return "in_progress"
}

func dedupeOpenWork(in []OpenWorkItem) []OpenWorkItem {
	seen := map[string]bool{}
	out := make([]OpenWorkItem, 0, len(in))
	for _, item := range in {
		if item.Key == "" || seen[item.Key] {
			continue
		}
		seen[item.Key] = true
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := map[string]int{"blocked": 0, "ready_to_merge": 1, "in_review": 2, "in_progress": 3, "pending": 4}
		if rank[out[i].Status] != rank[out[j].Status] {
			return rank[out[i].Status] < rank[out[j].Status]
		}
		return out[i].Title < out[j].Title
	})
	return out
}
