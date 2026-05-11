package contexthealth

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func TestRenderAdvisor_FiveHourUrgentBeatsHardCeiling(t *testing.T) {
	// Both five_hour_urgent and hard_ceiling could fire; the
	// renderer must pick five_hour_urgent because it has higher
	// priority.
	in := AdvisorInput{
		SessionID:      "s1",
		ContextFillPct: 80,
		FiveHour: FiveHourSummary{
			Cap:            100_000,
			TotalEffective: 80_000,
			PctUsed:        80,
		},
		State: emptyState(),
		NowMs: 5_000,
	}
	adv := RenderAdvisor(in)
	if adv.Fired != TriggerFiveHourUrgent {
		t.Fatalf("Fired=%q, want %q", adv.Fired, TriggerFiveHourUrgent)
	}
	if !strings.Contains(adv.Line, "5-hour window") {
		t.Fatalf("Line missing 5-hour: %q", adv.Line)
	}
}

func TestRenderAdvisor_HardCeilingFiresAtRescueFill(t *testing.T) {
	in := AdvisorInput{
		SessionID:      "s2",
		ContextFillPct: rescueFillThreshold + 1,
		FiveHour:       FiveHourSummary{Cap: 100_000, TotalEffective: 10_000, PctUsed: 10},
		State:          emptyState(),
		NowMs:          1000,
	}
	adv := RenderAdvisor(in)
	if adv.Fired != TriggerHardCeiling {
		t.Fatalf("Fired=%q, want %q", adv.Fired, TriggerHardCeiling)
	}
}

func TestRenderAdvisor_StaleFiresLast(t *testing.T) {
	// Build messages that trigger stale-share but no other rule.
	// The pivot needs enough billing user prompts to push the auth
	// files outside the recency horizon (recentTouchHorizonUserMsgs).
	const bigPayload = 12_000
	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login bug"),
		readCall(1, "/repo/auth/login.go"),
		readResult(2, 1, bigPayload),
		userMsg(3, "the session cookie issuer is wrong"),
		readCall(4, "/repo/auth/session.go"),
		readResult(5, 4, bigPayload),
		userMsg(6, "now switch to billing refund logic"),
		userMsg(7, "the subscription charge isn't applying"),
		userMsg(8, "billing invoice for the refund flow"),
		userMsg(9, "credit the customer for the failed charge"),
		userMsg(10, "make sure the refund subscription path is right"),
		userMsg(11, "double check the refund authorization code"),
		userMsg(12, "the refund retry policy needs adjusting"),
		userMsg(13, "verify the subscription invoice format"),
		userMsg(14, "ensure refund credits log correctly"),
		userMsg(15, "and the customer notification email"),
		readCall(16, "/repo/billing/refund.go"),
		readResult(17, 16, bigPayload),
	}
	in := AdvisorInput{
		SessionID:      "s3",
		Messages:       msgs,
		ContextFillPct: 10,
		FiveHour:       FiveHourSummary{},
		State:          emptyState(),
		NowMs:          1000,
	}
	adv := RenderAdvisor(in)
	if adv.Fired != TriggerStale {
		t.Fatalf("Fired=%q, want %q", adv.Fired, TriggerStale)
	}
	if !strings.Contains(adv.Line, "/klyne:handoff scope=current") {
		t.Fatalf("Line missing scoped handoff CTA: %q", adv.Line)
	}
	if !strings.Contains(adv.Line, "refund.go") {
		t.Fatalf("Line should name relevant subset: %q", adv.Line)
	}
}

func TestRenderAdvisor_TopicShiftFiresOnPartialPivot(t *testing.T) {
	// Partial pivot: the user has clearly shifted topic (first 5
	// user prompts are auth-only, last 5 are billing-only) AND
	// some loaded files (>=20%) are stale relative to the new
	// direction, BUT the stale share is below the 50% staleShare
	// trigger. The dedicated topic_shift trigger should fire to
	// catch this earlier-warning case that the headline stale
	// trigger misses.
	//
	// The topicShifted() detector requires at least 2×topicSampleSize
	// (10) user messages, so the fixture must have 10+ distinct
	// user prompts split across the two topics.
	const (
		stalePayload = 5_000  // smaller, so stale share lands below 50%
		freshPayload = 12_000 // larger, so stale share stays modest
	)

	// Need enough user messages on the new-topic side that the
	// auth file's last touch falls OUTSIDE the recency horizon
	// (recentTouchHorizonUserMsgs user messages back). Otherwise
	// the recency override marks login.go relevant and the stale
	// share stays at 0.
	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login bug"),
		userMsg(1, "the session cookie issuer is wrong"),
		userMsg(2, "the auth login redirect is dropping the user"),
		userMsg(3, "verify the auth session refresh path"),
		userMsg(4, "double check the login cookie scope"),
		readCall(5, "/repo/auth/login.go"),
		readResult(6, 5, stalePayload),
		userMsg(7, "now switch to billing refund logic"),
		userMsg(8, "the subscription charge isn't applying"),
		userMsg(9, "billing invoice for the refund flow"),
		userMsg(10, "credit the customer for the failed charge"),
		userMsg(11, "make sure the refund subscription path is right"),
		userMsg(12, "double check the refund authorization code"),
		userMsg(13, "the refund retry policy needs adjusting"),
		userMsg(14, "verify the subscription invoice format"),
		userMsg(15, "ensure refund credits log correctly"),
		userMsg(16, "and the customer notification email"),
		readCall(17, "/repo/billing/refund.go"),
		readResult(18, 17, freshPayload),
	}

	adv := RenderAdvisor(AdvisorInput{
		SessionID: "s-topic",
		Messages:  msgs,
		State:     emptyState(),
		NowMs:     1000,
	})
	if adv.Fired != TriggerTopicShift {
		t.Fatalf("Fired=%q, want %q (Line=%q)", adv.Fired, TriggerTopicShift, adv.Line)
	}
	if !strings.Contains(adv.Line, "shifted topic since the session opened") {
		t.Fatalf("Line missing topic-shift anchor phrase: %q", adv.Line)
	}
	if !strings.Contains(adv.Line, "/klyne:handoff scope=current") {
		t.Fatalf("Line missing scoped-handoff CTA: %q", adv.Line)
	}
	if !strings.Contains(adv.Line, "refund.go") {
		t.Fatalf("Line should name the still-relevant file: %q", adv.Line)
	}
}

func TestRenderAdvisor_StalePreemptsTopicShift(t *testing.T) {
	// When BOTH stale (>=50% stale) AND topic_shift conditions
	// hold, the stronger stale advisory must win — it carries a
	// more actionable signal.
	const stalePayload = 24_000
	const freshPayload = 8_000

	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login bug"),
		userMsg(1, "the session cookie issuer is wrong"),
		userMsg(2, "the auth login redirect is dropping the user"),
		userMsg(3, "verify the auth session refresh path"),
		userMsg(4, "double check the login cookie scope"),
		readCall(5, "/repo/auth/login.go"),
		readResult(6, 5, stalePayload),
		userMsg(7, "now switch to billing refund logic"),
		userMsg(8, "the subscription charge isn't applying"),
		userMsg(9, "billing invoice for the refund flow"),
		userMsg(10, "credit the customer for the failed charge"),
		userMsg(11, "make sure the refund subscription path is right"),
		userMsg(12, "double check the refund authorization code"),
		userMsg(13, "the refund retry policy needs adjusting"),
		userMsg(14, "verify the subscription invoice format"),
		userMsg(15, "ensure refund credits log correctly"),
		userMsg(16, "and the customer notification email"),
		readCall(17, "/repo/billing/refund.go"),
		readResult(18, 17, freshPayload),
	}

	adv := RenderAdvisor(AdvisorInput{
		SessionID: "s-priority",
		Messages:  msgs,
		State:     emptyState(),
		NowMs:     1000,
	})
	if adv.Fired != TriggerStale {
		t.Fatalf("expected stale to preempt topic_shift; Fired=%q Line=%q", adv.Fired, adv.Line)
	}
}

func TestRenderAdvisor_TransitionRule(t *testing.T) {
	// Same input twice; the second call should NOT fire because the
	// state already records the trigger.
	in := AdvisorInput{
		SessionID:      "s4",
		ContextFillPct: rescueFillThreshold + 5,
		FiveHour:       FiveHourSummary{},
		State:          emptyState(),
		NowMs:          1000,
	}
	first := RenderAdvisor(in)
	if first.Fired != TriggerHardCeiling {
		t.Fatalf("first call: Fired=%q, want %q", first.Fired, TriggerHardCeiling)
	}

	// Carry state forward.
	in.State = first.State
	in.NowMs = 2000
	second := RenderAdvisor(in)
	if second.Fired != "" {
		t.Fatalf("second call should be silent, got Fired=%q", second.Fired)
	}
}

func TestRenderAdvisor_ClearanceAllowsRefire(t *testing.T) {
	// First call: hard_ceiling fires. Second call: fill is back to
	// healthy → clear. Third call: fill spikes back → fires again.
	in := AdvisorInput{
		SessionID:      "s5",
		ContextFillPct: rescueFillThreshold + 5,
		FiveHour:       FiveHourSummary{},
		State:          emptyState(),
		NowMs:          1000,
	}
	first := RenderAdvisor(in)
	if first.Fired != TriggerHardCeiling {
		t.Fatalf("first: Fired=%q, want %q", first.Fired, TriggerHardCeiling)
	}

	in.State = first.State
	in.ContextFillPct = 30 // back to healthy
	clearer := RenderAdvisor(in)
	if clearer.Fired != "" {
		t.Fatalf("clearer call should be silent: Fired=%q", clearer.Fired)
	}
	if clearer.State.HasFired("s5", TriggerHardCeiling) {
		t.Fatalf("hard_ceiling should be cleared after fill drops")
	}

	in.State = clearer.State
	in.ContextFillPct = rescueFillThreshold + 5 // spike again
	third := RenderAdvisor(in)
	if third.Fired != TriggerHardCeiling {
		t.Fatalf("third call (re-trip): Fired=%q, want %q", third.Fired, TriggerHardCeiling)
	}
}

func TestRenderAdvisor_NoCapNoFiveHourFire(t *testing.T) {
	// Five-hour summary has TotalEffective but Cap == 0 → trigger
	// stays silent regardless.
	in := AdvisorInput{
		SessionID:      "s6",
		ContextFillPct: 10,
		FiveHour:       FiveHourSummary{TotalEffective: 999_999, Cap: 0, PctUsed: 0},
		State:          emptyState(),
		NowMs:          1000,
	}
	adv := RenderAdvisor(in)
	if adv.Fired != "" {
		t.Fatalf("expected silent advisor with cap=0, got Fired=%q", adv.Fired)
	}
}
