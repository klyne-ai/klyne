package contexthealth

import "github.com/klyne-ai/klyne/internal/connectors"

// CacheTrajectory classifies the direction of the cache-hit rate over the
// last N assistant turns. It is a pure function: same inputs → same output.
//
// "Cache hit rate" per turn = CachedReadTokens / max(1, TokensIn). This
// captures how many prompt tokens were served from the provider's KV cache
// rather than re-computed. A sharp drop means the prompt changed enough to
// bust the cache (context churning). Rising means the prompt is stabilising
// (normal for focused sessions).
//
// The spec defines four verdicts (rising, flat, falling, churning). "Churning"
// is the actionable one — it signals that repeated /compact or large context
// rewrites are destroying cache efficiency faster than the work adds value.

// CacheDirection is the trajectory verdict for the cache-hit rate.
type CacheDirection string

const (
	// CacheDirectionRising means the cache-hit rate improved over the window.
	CacheDirectionRising CacheDirection = "rising"
	// CacheDirectionFlat means the cache-hit rate was stable (±5 pp).
	CacheDirectionFlat CacheDirection = "flat"
	// CacheDirectionFalling means the cache-hit rate declined steadily.
	CacheDirectionFalling CacheDirection = "falling"
	// CacheDirectionChurning means the cache-hit rate dropped sharply
	// (≥30 percentage-points from peak to recent average). This is the
	// signal worth surfacing to the user.
	CacheDirectionChurning CacheDirection = "churning"
)

// CacheTrajectoryResult is the output of ComputeCacheTrajectory.
type CacheTrajectoryResult struct {
	// Rates is the per-turn cache-hit rate, ordered oldest-first. Each value
	// is in the range [0, 100]. At most TrajectoryWindow entries.
	Rates []float64
	// Direction summarises the trend across Rates.
	Direction CacheDirection
	// First is Rates[0] (or 0 when Rates is empty). Convenience for rendering
	// "84% → 47%" without re-indexing.
	First float64
	// Last is Rates[len-1] (or 0 when Rates is empty).
	Last float64
}

// TrajectoryWindow is the number of assistant turns the trajectory looks back.
// Five turns gives a stable-enough signal without requiring a long session.
const TrajectoryWindow = 5

// churnDropThreshold is the drop (in percentage points, from max to most-recent
// turn) that triggers the "churning" verdict. 30 pp is large enough to avoid
// flagging normal fill variance while still catching the cliff-edge drop that
// happens when a huge /compact rewrites the system prompt mid-session.
const churnDropThreshold = 30.0

// flatBand is the ±pp band within which the rate is considered "flat".
const flatBand = 5.0

// ComputeCacheTrajectory extracts the cache-hit rate for the last
// TrajectoryWindow assistant turns from msgs and returns a classified result.
//
// Only assistant messages with TokensIn > 0 are considered; turns with zero
// token counts (Codex messages before the token-projection post-pass, or
// messages from old-format transcripts) are skipped. When fewer than 2 turns
// have valid data the direction is set to CacheDirectionFlat so callers can
// still render whatever rates exist without an error branch.
func ComputeCacheTrajectory(msgs []*connectors.Message) CacheTrajectoryResult {
	rates := extractCacheRates(msgs, TrajectoryWindow)
	return classifyRates(rates)
}

// extractCacheRates walks msgs newest-to-oldest, collects up to n assistant
// turns with TokensIn > 0, computes the cache-hit rate per turn, then reverses
// the slice to chronological order before returning.
func extractCacheRates(msgs []*connectors.Message, n int) []float64 {
	rates := make([]float64, 0, n)
	for i := len(msgs) - 1; i >= 0 && len(rates) < n; i-- {
		m := msgs[i]
		if m.Role != connectors.RoleAssistant || m.TokensIn <= 0 {
			continue
		}
		hitRate := float64(m.CachedReadTokens) / float64(m.TokensIn) * 100.0
		rates = append(rates, hitRate)
	}
	// Reverse to chronological (oldest first).
	for lo, hi := 0, len(rates)-1; lo < hi; lo, hi = lo+1, hi-1 {
		rates[lo], rates[hi] = rates[hi], rates[lo]
	}
	return rates
}

// classifyRates converts a chronological []float64 cache-hit-rate slice into a
// CacheTrajectoryResult with a Direction verdict.
func classifyRates(rates []float64) CacheTrajectoryResult {
	if len(rates) == 0 {
		return CacheTrajectoryResult{Direction: CacheDirectionFlat}
	}
	first := rates[0]
	last := rates[len(rates)-1]

	res := CacheTrajectoryResult{
		Rates: rates,
		First: first,
		Last:  last,
	}

	if len(rates) < 2 {
		res.Direction = CacheDirectionFlat
		return res
	}

	// Churning: peak anywhere in the window dropped to a low recent value.
	peak := rates[0]
	for _, r := range rates[1:] {
		if r > peak {
			peak = r
		}
	}
	// Use the average of the last two turns as "recent" to dampen single-turn noise.
	recent := last
	if len(rates) >= 2 {
		recent = (rates[len(rates)-2] + rates[len(rates)-1]) / 2.0
	}
	if peak-recent >= churnDropThreshold {
		res.Direction = CacheDirectionChurning
		return res
	}

	// Net change from first to last.
	delta := last - first
	switch {
	case delta > flatBand:
		res.Direction = CacheDirectionRising
	case delta < -flatBand:
		res.Direction = CacheDirectionFalling
	default:
		res.Direction = CacheDirectionFlat
	}
	return res
}
