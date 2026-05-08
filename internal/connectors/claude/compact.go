// Package claude — compact detection heuristic.
//
// Claude Code does not emit a clean compaction event line. This file
// detects compaction by combining two signals:
//
//  1. A synthetic "summary" type line — parsed by W4's parseSummary as a
//     RoleSystem message with non-empty Content (the summary text) and a
//     non-empty ParentUUID (the leafUuid before compaction).
//
//  2. A subsequent assistant message whose TokensIn is significantly lower
//     than the last assistant's TokensIn immediately before the marker.
//     Threshold: new_tokens_in < prior_tokens_in * tokenDropThreshold.
//
// If no prior assistant message exists when the drop is observed, the
// heuristic requires only the summary marker (single-signal mode) — this
// is safe because summary lines never appear outside compaction events.
//
// Callers feed messages in order through Observe. The detector is not
// goroutine-safe; callers must synchronize externally if feeding from
// multiple goroutines.
package claude

import (
	"github.com/klyne-ai/klyne/internal/connectors"
)

// tokenDropThreshold is the fraction of prior TokensIn below which a
// subsequent assistant message is considered to confirm compaction.
// 0.20 means "new context is less than 20% of the old context".
const tokenDropThreshold = 0.20

// CompactDetector inspects a stream of *connectors.Message and emits
// CompactDetected signals when the heuristic fires.
//
// Usage:
//
//	det := claude.NewCompactDetector()
//	for _, msg := range messages {
//	    if detected, ts := det.Observe(msg); detected {
//	        // handle compact event at timestamp ts
//	    }
//	}
type CompactDetector struct {
	// pendingSummary is set when we see a summary/system message with
	// non-empty content (the compaction marker). It holds the ts of that
	// marker so we can emit the right timestamp.
	pendingSummary bool
	summaryTs      int64

	// lastAssistantTokensIn is the TokensIn of the most recent assistant
	// message seen before the pending summary marker. Zero means none yet.
	lastAssistantTokensIn int64

	// preSummaryTokensIn captures lastAssistantTokensIn at the moment we
	// see the summary marker, so we can compare against the next assistant.
	preSummaryTokensIn int64
}

// NewCompactDetector allocates and returns a ready-to-use CompactDetector.
func NewCompactDetector() *CompactDetector {
	return &CompactDetector{}
}

// Observe processes one message. Returns (true, ts) if a compact event was
// detected at this message; otherwise returns (false, 0).
//
// Detection triggers when:
//  1. A RoleSystem message with a non-empty ParentUUID is seen (summary marker).
//     W4's parseSummary sets ParentUUID=leafUUID for summary lines; system init
//     lines (type="system") leave ParentUUID empty and are ignored.
//  2. The next RoleAssistant message has TokensIn < preSummaryTokensIn * threshold.
//     If there was no prior assistant message (preSummaryTokensIn == 0),
//     detection fires on the summary marker itself (single-signal mode).
func (d *CompactDetector) Observe(m *connectors.Message) (detected bool, ts int64) {
	switch m.Role {
	case connectors.RoleSystem:
		// A RoleSystem message with a non-empty ParentUUID is the summary/compaction
		// marker. W4's parseSummary sets ParentUUID = leafUUID (the last assistant
		// UUID before compaction). Plain "system" init lines have ParentUUID="".
		if m.ParentUUID != "" && m.Content != "" {
			d.pendingSummary = true
			d.summaryTs = m.Ts
			d.preSummaryTokensIn = d.lastAssistantTokensIn

			// Single-signal mode: if we have no prior assistant context, fire now.
			if d.lastAssistantTokensIn == 0 {
				d.pendingSummary = false
				return true, m.Ts
			}
		}

	case connectors.RoleAssistant:
		if d.pendingSummary {
			// Two-signal mode: check token drop.
			if d.preSummaryTokensIn > 0 && m.TokensIn > 0 {
				ratio := float64(m.TokensIn) / float64(d.preSummaryTokensIn)
				if ratio < tokenDropThreshold {
					d.pendingSummary = false
					fireTs := d.summaryTs
					// Update lastAssistantTokensIn to the new message.
					d.lastAssistantTokensIn = m.TokensIn
					return true, fireTs
				}
			}
			// Token count not available or no drop — still fire on summary alone.
			// This handles cases where TokensIn is 0 (not reported).
			if m.TokensIn == 0 {
				d.pendingSummary = false
				fireTs := d.summaryTs
				return true, fireTs
			}
			// Token drop insufficient — cancel pending, do not fire.
			d.pendingSummary = false
		}
		// Track last assistant token count for future comparisons.
		if m.TokensIn > 0 {
			d.lastAssistantTokensIn = m.TokensIn
		}
	}

	return false, 0
}
