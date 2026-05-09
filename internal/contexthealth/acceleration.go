package contexthealth

import "github.com/klyne-ai/klyne/internal/connectors"

// acceleration.go — per-turn cost acceleration detector.
//
// Each assistant turn carries a TokensIn value that includes fresh
// input plus cache-read plus cache-write portions. The cache-read
// portion is heavily discounted by the provider, so the meaningful
// "rate-limit burn" per turn is approximately TokensIn -
// CachedReadTokens — we call this the "effective input" for that
// turn.
//
// A session is "accelerating" when the effective input per turn is
// rising fast enough that a few more turns will exhaust the user's
// 5-hour window. The detector compares the mean of the most recent
// few turns against the mean of the preceding window. When the
// recent mean is more than 2× the preceding mean AND the latest
// turn alone is non-trivial (≥ 5K effective tokens), the trigger
// fires.
//
// We deliberately do NOT extrapolate "you'll exhaust in N turns" —
// that projection requires knowing the user's plan cap and the
// trend reliability, neither of which is good enough for an 80-90%
// accuracy bar. Direction-only is what we ship in v1.

const (
	// recentTurns is the size of the "now" window. The detector takes
	// the mean of the effective-input values across the last
	// recentTurns assistant turns. Three is the smallest window
	// that smooths out a single-turn spike caused by one big tool
	// output — anything tighter would false-positive on noise.
	recentTurns = 3

	// priorTurns is the size of the "baseline" window immediately
	// preceding the recent window. Five gives the baseline enough
	// data points to be stable without reaching back to the very
	// start of the session, where the system-prompt warm-up turns
	// are not representative of the steady state.
	priorTurns = 5

	// minAccelerationTurns is the minimum number of qualifying
	// assistant turns (TokensIn > 0) the session must have before
	// the acceleration detector is willing to speak. Equals
	// recentTurns + priorTurns.
	minAccelerationTurns = recentTurns + priorTurns

	// accelerationRatio is how much the recent-window mean must
	// exceed the prior-window mean before the trigger fires. Two-fold
	// growth per turn-window is unambiguous; smaller multipliers
	// pick up natural fluctuations caused by tool-result variance.
	accelerationRatio = 2.0

	// minLatestEffectiveInput floors the trigger so that we never
	// fire on tiny absolute deltas even if the ratio looks dramatic.
	// A turn that adds <5K effective tokens is not going to blow
	// anyone's 5-hour cap on its own, regardless of the doubling.
	minLatestEffectiveInput int64 = 5_000
)

// AccelerationVerdict is the result of EvaluateAcceleration. Ready
// to drop into an advisor message.
type AccelerationVerdict struct {
	// ShouldFire is true when the recent window mean exceeds the
	// prior window mean by accelerationRatio AND the most recent
	// turn's effective input is ≥ minLatestEffectiveInput AND the
	// session has at least minAccelerationTurns qualifying turns.
	ShouldFire bool
	// RecentMean is the mean effective input across the last
	// recentTurns qualifying assistant turns. Zero when not enough
	// turns are available.
	RecentMean float64
	// PriorMean is the mean effective input across the priorTurns
	// qualifying assistant turns immediately preceding the recent
	// window. Zero when not enough turns are available.
	PriorMean float64
	// LatestEffectiveInput is the most recent qualifying assistant
	// turn's TokensIn - CachedReadTokens. Used by the advisor copy
	// to give the user a concrete number ("25K this turn").
	LatestEffectiveInput int64
	// SampledTurns is the count of qualifying assistant turns the
	// detector saw. Below minAccelerationTurns, the verdict is
	// silent regardless of the means.
	SampledTurns int
}

// EvaluateAcceleration walks the message slice in chronological
// order, extracts per-turn effective-input values from qualifying
// assistant messages, and produces an AccelerationVerdict.
//
// "Qualifying" means an assistant message with TokensIn > 0. Earlier
// assistant messages (system warmup, etc.) often have TokensIn == 0
// in some connectors; we skip those rather than pollute the means.
//
// Pure function; no I/O.
func EvaluateAcceleration(msgs []*connectors.Message) AccelerationVerdict {
	deltas := effectiveInputs(msgs)
	v := AccelerationVerdict{SampledTurns: len(deltas)}
	if len(deltas) > 0 {
		v.LatestEffectiveInput = deltas[len(deltas)-1]
	}
	if len(deltas) < minAccelerationTurns {
		return v
	}
	recent := deltas[len(deltas)-recentTurns:]
	prior := deltas[len(deltas)-recentTurns-priorTurns : len(deltas)-recentTurns]
	v.RecentMean = meanInt64(recent)
	v.PriorMean = meanInt64(prior)
	if v.PriorMean <= 0 {
		// Avoid div-by-zero. When the baseline is genuinely zero,
		// the ratio is undefined; we don't fire.
		return v
	}
	if v.RecentMean/v.PriorMean >= accelerationRatio &&
		v.LatestEffectiveInput >= minLatestEffectiveInput {
		v.ShouldFire = true
	}
	return v
}

// effectiveInputs extracts the per-turn effective-input value
// (TokensIn - CachedReadTokens) for every qualifying assistant
// message in chronological order.
func effectiveInputs(msgs []*connectors.Message) []int64 {
	out := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != connectors.RoleAssistant {
			continue
		}
		if m.TokensIn <= 0 {
			continue
		}
		eff := m.TokensIn - m.CachedReadTokens
		if eff < 0 {
			// Defensive: a malformed connector value should not flip
			// the trigger. Pin to zero rather than negative.
			eff = 0
		}
		out = append(out, eff)
	}
	return out
}

// meanInt64 averages a slice of int64 values, returning a float64.
// Returns 0 for an empty slice.
func meanInt64(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum int64
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}
