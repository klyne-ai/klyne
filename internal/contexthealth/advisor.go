package contexthealth

import (
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// advisor.go — high-level renderer for the proactive session
// advisor. Given a session snapshot, the cross-session 5-hour
// summary, and the persisted advisor state, return the single
// advisory line to inject (or an empty Line for "stay silent").
//
// All four triggers are evaluated independently, then a priority
// rule picks the highest-priority trigger that has NOT already
// fired. State for the firing trigger is marked; states for
// triggers whose conditions no longer hold are cleared so the
// next trip can fire fresh.
//
// Priority order (highest first):
//   1. five_hour_urgent — losing the rate-limit cap is the most
//      actionable signal regardless of which session caused it.
//   2. hard_ceiling — this session's per-turn cost is about to
//      balloon (≥75% fill).
//   3. five_hour_warn — half the rate-limit cap is gone.
//   4. acceleration — per-turn growth is trending bad.
//   5. stale — opportunity-cost: switching saves bytes.
//
// Why this order: rate-limit > session quality > prediction >
// opportunity cost. The user explicitly flagged "save my vibes"
// (rate-limit budget) as the primary goal.

// AdvisorInput bundles every piece of data the renderer needs.
// Pure data — the renderer performs no I/O.
type AdvisorInput struct {
	// SessionID identifies the session for state keying. Required.
	SessionID string
	// Messages is the chronological message slice (oldest first).
	Messages []*connectors.Message
	// ContextFillPct is the cache-aware fill ratio 0..100, computed
	// upstream by the snapshot loader.
	ContextFillPct float64
	// FiveHour is the cross-session summary. Pass an empty
	// FiveHourSummary when the user has not configured a plan tier;
	// the 5-hour triggers will be silent.
	FiveHour FiveHourSummary
	// State is the persisted transition state. The renderer uses
	// it to enforce "fire once per state transition" and returns
	// the updated copy.
	State AdvisorState
	// NowMs is the epoch-ms timestamp the call should treat as
	// "now," used for state housekeeping.
	NowMs int64
}

// Advisory is the renderer's verdict. Empty Line means "no advisory
// fires this turn."
type Advisory struct {
	// Fired names the trigger that produced this advisory, or empty
	// when nothing fires.
	Fired TriggerKind
	// Line is the user-visible advisory text. Always one line.
	// Empty when Fired is empty.
	Line string
	// State is the updated AdvisorState the caller should persist.
	// Already reflects the firing of Fired and any clearance for
	// triggers that are no longer active.
	State AdvisorState
}

// RenderAdvisor is the deterministic entry point. Pure function —
// same input always yields the same output.
func RenderAdvisor(in AdvisorInput) Advisory {
	state := in.State

	relevance := ScoreFiles(in.Messages)
	accel := EvaluateAcceleration(in.Messages)
	hardCeiling := in.ContextFillPct >= rescueFillThreshold
	fiveHourThresh := in.FiveHour.Threshold()

	// Clearance: when a trigger's condition is no longer true but
	// state says it was fired, clear it so it can re-fire later.
	if !relevance.ShouldFire {
		state.ClearTrigger(in.SessionID, TriggerStale)
	}
	if !accel.ShouldFire {
		state.ClearTrigger(in.SessionID, TriggerAcceleration)
	}
	if !hardCeiling {
		state.ClearTrigger(in.SessionID, TriggerHardCeiling)
	}
	if fiveHourThresh < FiveHourThresholdUrgent {
		state.ClearFiveHour(TriggerFiveHourUrgent)
	}
	if fiveHourThresh < FiveHourThresholdWarn {
		state.ClearFiveHour(TriggerFiveHourWarn)
	}

	// Priority resolution: walk the order, pick the first trigger
	// that should fire AND has not already fired.
	switch {
	case fiveHourThresh == FiveHourThresholdUrgent && !state.HasFiredFiveHour(TriggerFiveHourUrgent):
		state.MarkFiredFiveHour(TriggerFiveHourUrgent, in.FiveHour.PctUsed, in.NowMs)
		return Advisory{
			Fired: TriggerFiveHourUrgent,
			Line:  fiveHourUrgentLine(in.FiveHour),
			State: state,
		}
	case hardCeiling && !state.HasFired(in.SessionID, TriggerHardCeiling):
		state.MarkFired(in.SessionID, TriggerHardCeiling, in.NowMs)
		return Advisory{
			Fired: TriggerHardCeiling,
			Line:  hardCeilingLine(in.ContextFillPct),
			State: state,
		}
	case fiveHourThresh == FiveHourThresholdWarn && !state.HasFiredFiveHour(TriggerFiveHourWarn):
		state.MarkFiredFiveHour(TriggerFiveHourWarn, in.FiveHour.PctUsed, in.NowMs)
		return Advisory{
			Fired: TriggerFiveHourWarn,
			Line:  fiveHourWarnLine(in.FiveHour),
			State: state,
		}
	case accel.ShouldFire && !state.HasFired(in.SessionID, TriggerAcceleration):
		state.MarkFired(in.SessionID, TriggerAcceleration, in.NowMs)
		return Advisory{
			Fired: TriggerAcceleration,
			Line:  accelerationLine(accel),
			State: state,
		}
	case relevance.ShouldFire && !state.HasFired(in.SessionID, TriggerStale):
		state.MarkFired(in.SessionID, TriggerStale, in.NowMs)
		return Advisory{
			Fired: TriggerStale,
			Line:  staleLine(relevance),
			State: state,
		}
	}

	return Advisory{State: state}
}

// staleLine renders the relevance advisory. Mentions stale share
// and points at the scoped handoff.
func staleLine(v RelevanceVerdict) string {
	pct := int(v.StaleShare*100 + 0.5)
	subset := v.RelevantBasenames()
	if subset == "" {
		return fmt.Sprintf(
			"klyne: ~%d%% of loaded file context is stale relative to your current direction. "+
				"Consider /klyne:handoff scope=current and a fresh session.",
			pct,
		)
	}
	return fmt.Sprintf(
		"klyne: ~%d%% of loaded file context is stale relative to your current direction. "+
			"Files still relevant: %s. Run /klyne:handoff scope=current to carry forward only those.",
		pct, subset,
	)
}

// accelerationLine renders the acceleration advisory.
func accelerationLine(v AccelerationVerdict) string {
	return fmt.Sprintf(
		"klyne: per-turn cost has roughly doubled (latest turn ~%dK uncached). "+
			"Continuing here will burn through your 5-hour window faster than starting fresh — "+
			"/klyne:handoff scope=current keeps the relevant context.",
		v.LatestEffectiveInput/1000,
	)
}

// hardCeilingLine renders the hard-ceiling advisory.
func hardCeilingLine(fill float64) string {
	return fmt.Sprintf(
		"klyne: this session is %d%% full — the next turn's prefix will keep growing. "+
			"Run /klyne:handoff scope=current and start fresh.",
		int(fill+0.5),
	)
}

// fiveHourWarnLine renders the 50%-threshold five-hour advisory.
func fiveHourWarnLine(s FiveHourSummary) string {
	pct := int(s.PctUsed + 0.5)
	parts := []string{fmt.Sprintf("you've used ~%d%% of your 5-hour window", pct)}
	if dom := s.DominantSession(); dom != nil && dom.SessionID != "" && len(s.Sessions) > 1 {
		share := int(float64(dom.EffectiveInput)/float64(s.TotalEffective)*100 + 0.5)
		parts = append(parts, fmt.Sprintf("this session is the dominant consumer (~%d%% of the burn)", share))
	}
	return "klyne: " + strings.Join(parts, " — ") + " (estimated against your configured plan)."
}

// fiveHourUrgentLine renders the 75%-threshold five-hour advisory.
func fiveHourUrgentLine(s FiveHourSummary) string {
	pct := int(s.PctUsed + 0.5)
	return fmt.Sprintf(
		"klyne: you're at ~%d%% of your 5-hour window — the cheapest next step is "+
			"/klyne:handoff scope=current and a fresh session, otherwise you'll likely tip over the cap mid-task.",
		pct,
	)
}
