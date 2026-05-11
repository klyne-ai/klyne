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
