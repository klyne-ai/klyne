package productivity

import (
	"sort"
	"strings"
)

// MergeWhatWasDoneCards preserves every daily outcome in a multi-day report.
// The previous richness-based winner selection silently dropped other days.
func MergeWhatWasDoneCards(existing, incoming *WhatWasDoneCard) *WhatWasDoneCard {
	if existing == nil {
		if incoming == nil {
			return nil
		}
		copy := cloneCard(*incoming)
		return &copy
	}
	if incoming == nil {
		copy := cloneCard(*existing)
		return &copy
	}

	if existing.Narrative != nil || incoming.Narrative != nil {
		return mergeNarrativeCards(existing, incoming)
	}

	out := cloneCard(*existing)
	seen := map[string]bool{}
	for _, detail := range out.Tier2.Details {
		seen[detail.Day+"\x1f"+detail.SessionID+"\x1f"+detail.Kind+"\x1f"+detail.Text] = true
	}
	for _, detail := range incoming.Tier2.Details {
		key := detail.Day + "\x1f" + detail.SessionID + "\x1f" + detail.Kind + "\x1f" + detail.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Tier2.Details = append(out.Tier2.Details, detail)
	}
	out.Tier2.Details = sortDetails(out.Tier2.Details)
	out.Tier1 = BuildMechanicalTier1(out.Tier2.Details)
	out.LLMCompiled = existing.LLMCompiled && incoming.LLMCompiled
	if incoming.Day > out.Day {
		out.Day = incoming.Day
	}
	return &out
}

func mergeNarrativeCards(existing, incoming *WhatWasDoneCard) *WhatWasDoneCard {
	out := cloneCard(*existing)
	if out.Narrative == nil {
		out.Narrative = &WWDNarrative{Cards: []WWDCard{}}
		for _, detail := range out.Tier2.Details {
			out.Narrative.Cards = append(out.Narrative.Cards, detailToNarrative(detail))
		}
	}
	seen := map[string]bool{}
	for _, card := range out.Narrative.Cards {
		seen[narrativeOutcomeKey(card)] = true
	}

	var incomingCards []WWDCard
	if incoming.Narrative != nil {
		incomingCards = incoming.Narrative.Cards
		if incoming.Narrative.Summary != "" {
			out.Narrative.Summary = incoming.Narrative.Summary
		}
		if incoming.Narrative.Followup != "" {
			out.Narrative.Followup = incoming.Narrative.Followup
		}
	} else {
		for _, detail := range incoming.Tier2.Details {
			incomingCards = append(incomingCards, detailToNarrative(detail))
		}
	}
	for _, card := range incomingCards {
		key := narrativeOutcomeKey(card)
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Narrative.Cards = append(out.Narrative.Cards, card)
	}
	sort.SliceStable(out.Narrative.Cards, func(i, j int) bool {
		if out.Narrative.Cards[i].Day != out.Narrative.Cards[j].Day {
			return out.Narrative.Cards[i].Day > out.Narrative.Cards[j].Day
		}
		return kindRank(out.Narrative.Cards[i].Kind) < kindRank(out.Narrative.Cards[j].Kind)
	})
	out.Narrative.Stats = statsForNarrative(out.Narrative.Cards)
	out.LLMCompiled = existing.LLMCompiled && incoming.LLMCompiled
	if incoming.Day > out.Day {
		out.Day = incoming.Day
	}
	return &out
}

func detailToNarrative(detail WWDDetail) WWDCard {
	ticket := ""
	if match := floorTicketRe.FindString(detail.Text + " " + strings.Join(detail.Evidence, " ")); match != "" {
		ticket = match
	}
	return WWDCard{
		Kind: detail.Kind, TicketID: ticket, Title: detail.Text, Body: detail.Text,
		Refs: floorRefsToTyped(detail.Evidence), CLIs: append([]string(nil), detail.CLIs...),
		Day: detail.Day,
	}
}

func narrativeOutcomeKey(card WWDCard) string {
	return card.Day + "\x1f" + card.TicketID + "\x1f" + card.Kind + "\x1f" + card.Title
}

func kindRank(kind string) int {
	for i, candidate := range kindOrder {
		if candidate == kind {
			return i
		}
	}
	return len(kindOrder)
}

func statsForNarrative(cards []WWDCard) WWDStats {
	stats := WWDStats{}
	for _, card := range cards {
		switch card.Kind {
		case "SHIPPED":
			stats.Shipped++
		case "MAJOR":
			stats.Major++
		case "FIXED":
			stats.Fixed++
		case "DECISION":
			stats.Decisions++
		case "INVESTIGATED":
			stats.Investigated++
		case "IN_PROGRESS":
			stats.InProgress++
		}
	}
	return stats
}
