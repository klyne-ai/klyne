package contexthealth

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// asstWithUsage builds an assistant message with the given input
// token usage figures. Used to exercise EvaluateAcceleration.
func asstWithUsage(idx int, tokensIn, cachedRead int64) *connectors.Message {
	return &connectors.Message{
		ID:               "a" + itoa(idx),
		Role:             connectors.RoleAssistant,
		Content:          "(reply)",
		Ts:               int64(idx) * 1000,
		TokensIn:         tokensIn,
		CachedReadTokens: cachedRead,
	}
}

func TestEvaluateAcceleration_FiresOnDoubling(t *testing.T) {
	// 5 prior turns at ~5K effective; 3 recent turns rising 9K, 15K,
	// 25K — recent mean ≈16.3K, prior mean ≈5K, ratio ≈3.3, latest ≥5K.
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
	v := EvaluateAcceleration(msgs)
	if !v.ShouldFire {
		t.Fatalf("expected ShouldFire=true, got false; verdict=%+v", v)
	}
	if v.LatestEffectiveInput != 25_000 {
		t.Fatalf("latest=%d, want 25000", v.LatestEffectiveInput)
	}
	if v.RecentMean <= v.PriorMean*accelerationRatio-1e-9 {
		t.Fatalf("expected RecentMean (%v) >= %.1f×PriorMean (%v)", v.RecentMean, accelerationRatio, v.PriorMean)
	}
}

func TestEvaluateAcceleration_FlatNoFire(t *testing.T) {
	// All turns ~5K — no acceleration.
	msgs := make([]*connectors.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, asstWithUsage(i, 5_000, 0))
	}
	v := EvaluateAcceleration(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false on flat session, got true; verdict=%+v", v)
	}
}

func TestEvaluateAcceleration_BelowFloorNoFire(t *testing.T) {
	// Recent window doubles vs prior, but absolute latest is tiny.
	msgs := []*connectors.Message{
		asstWithUsage(0, 800, 0),
		asstWithUsage(1, 900, 0),
		asstWithUsage(2, 850, 0),
		asstWithUsage(3, 1_000, 0),
		asstWithUsage(4, 950, 0),
		asstWithUsage(5, 2_000, 0),
		asstWithUsage(6, 2_500, 0),
		asstWithUsage(7, 3_000, 0),
	}
	v := EvaluateAcceleration(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false (below %dK floor); verdict=%+v", minLatestEffectiveInput/1000, v)
	}
}

func TestEvaluateAcceleration_TooFewTurnsSilent(t *testing.T) {
	// Only 4 qualifying turns — below the minimum.
	msgs := []*connectors.Message{
		asstWithUsage(0, 5_000, 0),
		asstWithUsage(1, 9_000, 0),
		asstWithUsage(2, 15_000, 0),
		asstWithUsage(3, 25_000, 0),
	}
	v := EvaluateAcceleration(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false below minAccelerationTurns; verdict=%+v", v)
	}
}

func TestEvaluateAcceleration_CachedReadDiscount(t *testing.T) {
	// All turns have very high TokensIn (200K cached prefix), but the
	// fresh portion stays flat. Should NOT fire.
	msgs := make([]*connectors.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, asstWithUsage(i, 205_000, 200_000))
	}
	v := EvaluateAcceleration(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false when cache discount keeps fresh portion flat; verdict=%+v", v)
	}
}

func TestEvaluateAcceleration_SkipsZeroTokens(t *testing.T) {
	// The session interleaves zero-token assistant messages (e.g.
	// non-billed turns). Detector skips those.
	msgs := []*connectors.Message{
		asstWithUsage(0, 0, 0), // skipped
		asstWithUsage(1, 5_000, 0),
		asstWithUsage(2, 0, 0), // skipped
		asstWithUsage(3, 5_500, 0),
		asstWithUsage(4, 4_800, 0),
		asstWithUsage(5, 5_200, 0),
		asstWithUsage(6, 5_000, 0),
		asstWithUsage(7, 9_000, 0),
		asstWithUsage(8, 15_000, 0),
		asstWithUsage(9, 25_000, 0),
	}
	v := EvaluateAcceleration(msgs)
	if !v.ShouldFire {
		t.Fatalf("expected ShouldFire=true with zero-token rows skipped; verdict=%+v", v)
	}
}
