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
type TokenTimeline struct {
	// SessionID identifies the session this timeline describes.
	SessionID string `json:"session_id"`
	// WindowStartMs / WindowEndMs bracket the timestamps included.
	WindowStartMs int64 `json:"window_start_ms"`
	WindowEndMs   int64 `json:"window_end_ms"`
	// Points are the per-turn rows in chronological order.
	Points []TimelinePoint `json:"points"`
	// TotalEffective is sum(EffectiveInput) across Points.
	TotalEffective int64 `json:"total_effective"`
	// PeakEffective is max(EffectiveInput) across Points.
	PeakEffective int64 `json:"peak_effective"`
	// CapEffective is the user's configured 5-hour cap. Zero when
	// no plan tier is set; renderers should hide cap-relative
	// metrics in that case.
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
	}
	return out
}

// PctUsed returns TotalEffective / CapEffective × 100, capped at
// 100. Returns 0 when CapEffective is 0 (the cap is not configured).
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
