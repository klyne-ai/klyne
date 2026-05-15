package contexthealth

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// assistantTurn builds a minimal assistant message with the given token counts.
// cached is CachedReadTokens; total is TokensIn (must be ≥ cached).
func assistantTurn(idx int, total, cached int64) *connectors.Message {
	return &connectors.Message{
		ID:               "a" + itoa(int(idx)),
		Role:             connectors.RoleAssistant,
		Ts:               int64(idx) * 1000,
		TokensIn:         total,
		CachedReadTokens: cached,
	}
}

func TestComputeCacheTrajectory_Empty(t *testing.T) {
	t.Parallel()
	got := ComputeCacheTrajectory(nil)
	if got.Direction != CacheDirectionFlat {
		t.Errorf("empty msgs: Direction = %q, want %q", got.Direction, CacheDirectionFlat)
	}
	if len(got.Rates) != 0 {
		t.Errorf("empty msgs: Rates = %v, want empty", got.Rates)
	}
}

func TestComputeCacheTrajectory_SingleTurn(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 50), // 50% hit rate
	}
	got := ComputeCacheTrajectory(msgs)
	if got.Direction != CacheDirectionFlat {
		t.Errorf("single turn: Direction = %q, want flat", got.Direction)
	}
	if len(got.Rates) != 1 {
		t.Fatalf("single turn: Rates len = %d, want 1", len(got.Rates))
	}
	if got.Rates[0] != 50.0 {
		t.Errorf("single turn: Rates[0] = %v, want 50.0", got.Rates[0])
	}
}

func TestComputeCacheTrajectory_Rising(t *testing.T) {
	t.Parallel()
	// Cache hit rate 20% → 40% → 60% → 80%: clearly rising.
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 20),
		assistantTurn(1, 100, 40),
		assistantTurn(2, 100, 60),
		assistantTurn(3, 100, 80),
	}
	got := ComputeCacheTrajectory(msgs)
	if got.Direction != CacheDirectionRising {
		t.Errorf("Direction = %q, want rising", got.Direction)
	}
	if got.First != 20.0 || got.Last != 80.0 {
		t.Errorf("First/Last = %v/%v, want 20/80", got.First, got.Last)
	}
}

func TestComputeCacheTrajectory_Flat(t *testing.T) {
	t.Parallel()
	// Cache hit rate oscillates within ±5 pp of 70%.
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 70),
		assistantTurn(1, 100, 72),
		assistantTurn(2, 100, 68),
		assistantTurn(3, 100, 71),
	}
	got := ComputeCacheTrajectory(msgs)
	if got.Direction != CacheDirectionFlat {
		t.Errorf("Direction = %q, want flat", got.Direction)
	}
}

func TestComputeCacheTrajectory_Falling(t *testing.T) {
	t.Parallel()
	// Cache hit rate 50% → 40% → 35% → 25%: falling by 25 pp (below 30 pp churn
	// threshold) so it should be classified as falling, not churning.
	// Average of last two turns: (35+25)/2 = 30. Peak=50. Drop = 50-30 = 20 pp < 30 pp.
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 50),
		assistantTurn(1, 100, 40),
		assistantTurn(2, 100, 35),
		assistantTurn(3, 100, 25),
	}
	got := ComputeCacheTrajectory(msgs)
	if got.Direction != CacheDirectionFalling {
		t.Errorf("Direction = %q, want falling", got.Direction)
	}
}

func TestComputeCacheTrajectory_Churning(t *testing.T) {
	t.Parallel()
	// Spec example: 84% → 47% over 5 turns. The peak-to-recent drop is >30 pp.
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 84),
		assistantTurn(1, 100, 75),
		assistantTurn(2, 100, 65),
		assistantTurn(3, 100, 50),
		assistantTurn(4, 100, 47),
	}
	got := ComputeCacheTrajectory(msgs)
	if got.Direction != CacheDirectionChurning {
		t.Errorf("Direction = %q, want churning", got.Direction)
	}
	if got.First != 84.0 {
		t.Errorf("First = %v, want 84", got.First)
	}
	if got.Last != 47.0 {
		t.Errorf("Last = %v, want 47", got.Last)
	}
}

func TestComputeCacheTrajectory_SkipsNonAssistantAndZeroTokens(t *testing.T) {
	t.Parallel()
	// Interleave user, tool, and zero-token assistant messages; only the
	// assistant messages with TokensIn > 0 should contribute to the rates.
	msgs := []*connectors.Message{
		userMsg(0, "hello"),
		{ID: "a1", Role: connectors.RoleAssistant, Ts: 1000, TokensIn: 0}, // skip — zero tokens
		{ID: "t2", Role: connectors.RoleTool, Ts: 2000, TokensIn: 100, CachedReadTokens: 50},
		assistantTurn(3, 100, 60),
		assistantTurn(4, 100, 80),
	}
	got := ComputeCacheTrajectory(msgs)
	// Only turns 3 and 4 are valid. 60%→80% is rising.
	if got.Direction != CacheDirectionRising {
		t.Errorf("Direction = %q, want rising", got.Direction)
	}
	if len(got.Rates) != 2 {
		t.Errorf("Rates len = %d, want 2", len(got.Rates))
	}
}

func TestComputeCacheTrajectory_CapsAtWindowSize(t *testing.T) {
	t.Parallel()
	// 8 assistant turns — only the last TrajectoryWindow (5) should appear.
	msgs := make([]*connectors.Message, 8)
	for i := range msgs {
		msgs[i] = assistantTurn(i, 100, int64(10+i*5))
	}
	got := ComputeCacheTrajectory(msgs)
	if len(got.Rates) != TrajectoryWindow {
		t.Errorf("Rates len = %d, want %d", len(got.Rates), TrajectoryWindow)
	}
	// The last TrajectoryWindow entries are turns 3..7; turn 3 has 10+3*5=25% cache.
	if got.First != 25.0 {
		t.Errorf("First = %v, want 25.0 (turn 3)", got.First)
	}
}

func TestComputeCacheTrajectory_Deterministic(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		assistantTurn(0, 100, 84),
		assistantTurn(1, 100, 47),
	}
	a := ComputeCacheTrajectory(msgs)
	b := ComputeCacheTrajectory(msgs)
	if a.Direction != b.Direction || a.First != b.First || a.Last != b.Last {
		t.Errorf("non-deterministic: %+v vs %+v", a, b)
	}
}
