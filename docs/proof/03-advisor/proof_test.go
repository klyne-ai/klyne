// Package advisor contains the reproducible proof that klyne's
// proactive session advisor produces the right one-liner for each
// of its four triggers AND fires at most once per state transition.
//
// Run from the repo root:
//
//	go test -v ./docs/proof/03-advisor/
//	make proof
package advisor_test

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// userMsg / asstWithUsage / readCall / readResult are tiny copies
// of the per-package test helpers — duplicated here on purpose so
// the proof tests do not depend on package-internal test fixtures.
// If the helpers ever ship as a public test package, this file can
// migrate.

func userMsg(idx int, content string) *connectors.Message {
	return &connectors.Message{
		ID:      idStr("u", idx),
		Role:    connectors.RoleUser,
		Content: content,
		Ts:      int64(idx) * 1000,
	}
}

func asstWithUsage(idx int, tokensIn, cachedRead int64) *connectors.Message {
	return &connectors.Message{
		ID:               idStr("a", idx),
		Role:             connectors.RoleAssistant,
		Content:          "(reply)",
		Ts:               int64(idx) * 1000,
		TokensIn:         tokensIn,
		CachedReadTokens: cachedRead,
	}
}

func readCall(idx int, path string) *connectors.Message {
	return &connectors.Message{
		ID:      idStr("a", idx),
		Role:    connectors.RoleAssistant,
		Content: "(reading)",
		Ts:      int64(idx) * 1000,
		ToolCalls: []connectors.ToolCall{{
			ID:    idStr("tc", idx),
			Name:  "Read",
			Input: `{"file_path":"` + path + `"}`,
		}},
	}
}

func readResult(idx, callIdx, body int) *connectors.Message {
	out := strings.Repeat("x", body)
	return &connectors.Message{
		ID:      idStr("t", idx),
		Role:    connectors.RoleTool,
		Content: out,
		Ts:      int64(idx)*1000 + 500,
		ToolResults: []connectors.ToolResult{{
			ID:     idStr("tc", callIdx),
			Output: out,
		}},
	}
}

func idStr(prefix string, i int) string {
	const digits = "0123456789"
	if i == 0 {
		return prefix + "0"
	}
	out := []byte{}
	n := i
	if n < 0 {
		n = -n
	}
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return prefix + string(out)
}

// emptyState returns a fresh AdvisorState for the renderer.
func emptyState() contexthealth.AdvisorState {
	return contexthealth.AdvisorState{
		Sessions: map[string]*contexthealth.SessionState{},
	}
}

// TestProof_StaleContextAdvisoryFiresOnTopicShift asserts that the
// stale-context trigger fires when more than half of loaded file
// bytes are anchored in user messages that no longer overlap with
// the current direction, AND that the advisory copy names a
// concrete relevant subset.
func TestProof_StaleContextAdvisoryFiresOnTopicShift(t *testing.T) {
	const bigPayload = 12_000

	// The pivot needs enough billing user prompts to push the auth
	// files at idx 1 and 4 outside the recency horizon (matching
	// recentTouchHorizonUserMsgs in the relevance scorer).
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

	advisory := contexthealth.RenderAdvisor(contexthealth.AdvisorInput{
		SessionID: "proof-stale",
		Messages:  msgs,
		State:     emptyState(),
		NowMs:     12_000,
	})
	if advisory.Fired != contexthealth.TriggerStale {
		t.Fatalf("Fired=%q, want %q\nLine=%q",
			advisory.Fired, contexthealth.TriggerStale, advisory.Line)
	}
	mustContain(t, advisory.Line, "stale", "/klyne:handoff scope=current", "refund.go")
	mustNotContain(t, advisory.Line, "login.go", "session.go")
}

// TestProof_AccelerationAdvisoryFiresOnDoubling asserts the
// acceleration trigger fires when the recent 3-turn mean exceeds
// twice the preceding 5-turn mean AND the latest delta is at least
// the 5K floor.
func TestProof_AccelerationAdvisoryFiresOnDoubling(t *testing.T) {
	msgs := []*connectors.Message{
		asstWithUsage(0, 5_000, 0),
		asstWithUsage(1, 5_500, 0),
		asstWithUsage(2, 4_800, 0),
		asstWithUsage(3, 5_200, 0),
		asstWithUsage(4, 5_000, 0),
		asstWithUsage(5, 9_000, 0),
		asstWithUsage(6, 15_000, 0),
		asstWithUsage(7, 25_000, 0),
	}
	advisory := contexthealth.RenderAdvisor(contexthealth.AdvisorInput{
		SessionID: "proof-accel",
		Messages:  msgs,
		State:     emptyState(),
		NowMs:     8_000,
	})
	if advisory.Fired != contexthealth.TriggerAcceleration {
		t.Fatalf("Fired=%q, want %q\nLine=%q",
			advisory.Fired, contexthealth.TriggerAcceleration, advisory.Line)
	}
	mustContain(t, advisory.Line, "doubled", "/klyne:handoff scope=current")
}

// TestProof_FiveHourUrgentAdvisoryFiresAt75Pct asserts the
// five-hour-window trigger crosses into the urgent tier at 75%
// consumption AND the advisory carries the percentage.
func TestProof_FiveHourUrgentAdvisoryFiresAt75Pct(t *testing.T) {
	advisory := contexthealth.RenderAdvisor(contexthealth.AdvisorInput{
		SessionID: "proof-5h",
		FiveHour: contexthealth.FiveHourSummary{
			Cap:            100_000,
			TotalEffective: 80_000,
			PctUsed:        80,
		},
		State: emptyState(),
		NowMs: 5_000,
	})
	if advisory.Fired != contexthealth.TriggerFiveHourUrgent {
		t.Fatalf("Fired=%q, want %q\nLine=%q",
			advisory.Fired, contexthealth.TriggerFiveHourUrgent, advisory.Line)
	}
	mustContain(t, advisory.Line, "5-hour window", "/klyne:handoff scope=current", "80%")
}

// TestProof_TransitionRuleFiresExactlyOnce asserts the
// fire-once-per-transition contract: identical inputs on a second
// call do not produce a second advisory until the trigger condition
// clears.
func TestProof_TransitionRuleFiresExactlyOnce(t *testing.T) {
	in := contexthealth.AdvisorInput{
		SessionID:      "proof-transition",
		ContextFillPct: 80, // hard-ceiling territory
		State:          emptyState(),
		NowMs:          1_000,
	}
	first := contexthealth.RenderAdvisor(in)
	if first.Fired != contexthealth.TriggerHardCeiling {
		t.Fatalf("first call should fire hard_ceiling; got Fired=%q", first.Fired)
	}

	in.State = first.State
	in.NowMs = 2_000
	second := contexthealth.RenderAdvisor(in)
	if second.Fired != "" {
		t.Fatalf("second call should be silent; got Fired=%q\nLine=%q",
			second.Fired, second.Line)
	}

	// Drop the fill back to healthy → state clears.
	in.ContextFillPct = 30
	in.State = second.State
	in.NowMs = 3_000
	clearer := contexthealth.RenderAdvisor(in)
	if clearer.Fired != "" {
		t.Fatalf("clearance call should be silent; got Fired=%q", clearer.Fired)
	}
	if clearer.State.HasFired("proof-transition", contexthealth.TriggerHardCeiling) {
		t.Fatalf("hard_ceiling should clear after fill drops")
	}

	// Re-trip: the trigger should fire again now that state is clean.
	in.ContextFillPct = 80
	in.State = clearer.State
	in.NowMs = 4_000
	third := contexthealth.RenderAdvisor(in)
	if third.Fired != contexthealth.TriggerHardCeiling {
		t.Fatalf("re-trip should fire hard_ceiling again; got Fired=%q", third.Fired)
	}
}

// mustContain fails the test when md is missing any of the
// fragments. Used by the proof assertions so the failure message
// names the offender.
func mustContain(t *testing.T, md string, fragments ...string) {
	t.Helper()
	for _, frag := range fragments {
		if !strings.Contains(md, frag) {
			t.Errorf("advisory line missing %q\n--- line ---\n%s\n--- end ---", frag, md)
		}
	}
}

// mustNotContain fails the test when md contains any of the
// fragments. Used to assert that stale files don't leak into the
// relevant subset.
func mustNotContain(t *testing.T, md string, fragments ...string) {
	t.Helper()
	for _, frag := range fragments {
		if strings.Contains(md, frag) {
			t.Errorf("advisory line should not contain %q\n--- line ---\n%s\n--- end ---", frag, md)
		}
	}
}
