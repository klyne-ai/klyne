package contexthealth

import (
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// timeline.go — per-turn token timeline for a single session.
//
// The proactive advisor surfaces an alarm WHEN the session crosses
// a threshold; users frequently want to look at the underlying
// trend that led there. ComputeTimeline returns the per-assistant-
// turn token-cost values within a configurable window, ready for
// either the CLI's ASCII sparkline or the cockpit's line chart.
//
// Pure function over a message slice. No I/O, no state. The slash
// command and the cockpit both call this.

// TimelineWindow is the default lookback when the caller doesn't
// override. Matches the rate-limit window so the user sees exactly
// what counts toward the 5-hour cap.
const TimelineWindow = 5 * time.Hour

// TimelinePoint is one assistant turn's token usage values.
type TimelinePoint struct {
	// TsMs is the assistant message timestamp in epoch-ms.
	TsMs int64 `json:"ts_ms"`
	// EffectiveInput is TokensIn - CachedReadTokens — the portion
	// that burns the user's rate-limit budget at full rate.
	EffectiveInput int64 `json:"effective_input"`
	// TotalInput is the raw TokensIn (fresh + cached read + cached
	// write). Useful for visualising the cache discount.
	TotalInput int64 `json:"total_input"`
	// CachedReadTokens is the prefix served from prompt cache.
	CachedReadTokens int64 `json:"cached_read_tokens"`
	// CachedWriteTokens is the new content written to cache.
	CachedWriteTokens int64 `json:"cached_write_tokens"`
	// OutputTokens is the completion token count.
	OutputTokens int64 `json:"output_tokens"`
}

// TokenTimeline is the full per-session breakdown returned by
// ComputeTimeline.
//
// Two axes of measurement that callers must NOT mix up:
//
//  1. Per-turn TotalInput (TokensIn) — the prefix size at that
//     turn, including cached prefix. This is what the user sees
//     grow as the session continues; it is the right metric for
//     "how full is my context window?"
//  2. Per-turn EffectiveInput (TokensIn - CachedReadTokens) — the
//     uncached portion that burns the 5-hour rate-limit budget at
//     full rate. This is the right metric for the rate-limit
//     advisor. Same data, different question.
//
// /klyne:tokens leads on (1). The advisor (klyne advise) uses (2).
type TokenTimeline struct {
	// SessionID identifies the session this timeline describes.
	SessionID string `json:"session_id"`
	// Model is the assistant model on the most recent qualifying
	// turn. Used to look up the context window size.
	Model string `json:"model,omitempty"`
	// ContextWindow is the model's maximum input-context size in
	// tokens. Drives the per-turn % of context calculation.
	ContextWindow int64 `json:"context_window,omitempty"`
	// WindowStartMs / WindowEndMs bracket the timestamps included.
	WindowStartMs int64 `json:"window_start_ms"`
	WindowEndMs   int64 `json:"window_end_ms"`
	// Points are the per-turn rows in chronological order.
	Points []TimelinePoint `json:"points"`
	// LatestInput is the most recent qualifying turn's TokensIn —
	// "how big is my prefix right now?"
	LatestInput int64 `json:"latest_input"`
	// PeakInput is max(TotalInput) across Points — the largest
	// single-turn prefix the session ever carried.
	PeakInput int64 `json:"peak_input"`
	// FirstInput is the oldest qualifying turn's TokensIn — "where
	// did this session start?"
	FirstInput int64 `json:"first_input"`
	// TotalEffective is sum(EffectiveInput) across Points. Carried
	// here so the advisor's separate concern (5h cap) can still
	// consume the same struct.
	TotalEffective int64 `json:"total_effective"`
	// PeakEffective is max(EffectiveInput). Same use as TotalEffective.
	PeakEffective int64 `json:"peak_effective"`
	// CapEffective is the user's configured 5-hour cap. Zero when
	// no plan tier is set. Carried for the advisor's use; the
	// /klyne:tokens renderer ignores it on purpose.
	CapEffective int64 `json:"cap_effective"`
}

// ComputeTimeline walks msgs and returns a TokenTimeline restricted
// to assistant turns whose Ts falls within [nowMs-windowMs, nowMs].
// cap is the user's configured 5-hour effective-token cap; pass 0
// when unset so the renderer can hide the percentage column.
//
// Empty input or no assistant turns inside the window produces an
// empty Points slice with the window bounds set.
func ComputeTimeline(msgs []*connectors.Message, nowMs, windowMs, cap int64) TokenTimeline {
	startMs := nowMs - windowMs
	out := TokenTimeline{
		WindowStartMs: startMs,
		WindowEndMs:   nowMs,
		CapEffective:  cap,
	}
	if len(msgs) == 0 {
		return out
	}
	for _, m := range msgs {
		if m.Role != connectors.RoleAssistant {
			continue
		}
		if m.TokensIn <= 0 {
			continue
		}
		if m.Ts < startMs || m.Ts > nowMs {
			continue
		}
		eff := m.TokensIn - m.CachedReadTokens
		if eff < 0 {
			eff = 0
		}
		if out.SessionID == "" {
			out.SessionID = m.SessionID
		}
		if m.Model != "" {
			out.Model = m.Model
		}
		out.Points = append(out.Points, TimelinePoint{
			TsMs:              m.Ts,
			EffectiveInput:    eff,
			TotalInput:        m.TokensIn,
			CachedReadTokens:  m.CachedReadTokens,
			CachedWriteTokens: m.CachedWriteTokens,
			OutputTokens:      m.TokensOut,
		})
		out.TotalEffective += eff
		if eff > out.PeakEffective {
			out.PeakEffective = eff
		}
		if m.TokensIn > out.PeakInput {
			out.PeakInput = m.TokensIn
		}
	}
	if len(out.Points) > 0 {
		out.FirstInput = out.Points[0].TotalInput
		out.LatestInput = out.Points[len(out.Points)-1].TotalInput
	}
	return out
}

// PctUsed returns TotalEffective / CapEffective × 100, capped at
// 100. Returns 0 when CapEffective is 0 (the cap is not configured).
// Used by the advisor's 5-hour-window check, NOT by the timeline
// renderer's headline.
func (t TokenTimeline) PctUsed() float64 {
	if t.CapEffective <= 0 {
		return 0
	}
	pct := float64(t.TotalEffective) / float64(t.CapEffective) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}

// PctOfContext returns LatestInput / ContextWindow × 100, capped
// at 100. This is the headline the /klyne:tokens renderer leads
// on: "how full is my prefix right now?"
func (t TokenTimeline) PctOfContext() float64 {
	if t.ContextWindow <= 0 || t.LatestInput <= 0 {
		return 0
	}
	pct := float64(t.LatestInput) / float64(t.ContextWindow) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}
