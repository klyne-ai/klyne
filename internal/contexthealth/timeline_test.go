package contexthealth

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func asstAt(idx int, tsMs, tokensIn, cachedRead int64) *connectors.Message {
	return &connectors.Message{
		ID:               idStrTL("a", idx),
		SessionID:        "sess-tl",
		Role:             connectors.RoleAssistant,
		Ts:               tsMs,
		TokensIn:         tokensIn,
		CachedReadTokens: cachedRead,
	}
}

func idStrTL(prefix string, i int) string {
	const digits = "0123456789"
	if i == 0 {
		return prefix + "0"
	}
	out := []byte{}
	n := i
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return prefix + string(out)
}

func TestComputeTimeline_FiltersOutsideWindow(t *testing.T) {
	const now int64 = 1_000_000_000
	const window int64 = 60 * 1000 // 60 seconds for test brevity

	msgs := []*connectors.Message{
		asstAt(0, now-90_000, 5_000, 0), // outside (older than window)
		asstAt(1, now-30_000, 5_000, 1_000),
		asstAt(2, now-10_000, 9_000, 0),
		asstAt(3, now+5_000, 25_000, 0), // outside (in the future)
	}
	tl := ComputeTimeline(msgs, now, window, 100_000)
	if len(tl.Points) != 2 {
		t.Fatalf("Points=%d, want 2; got %+v", len(tl.Points), tl.Points)
	}
	// Effective: (5000-1000) + (9000-0) = 13_000
	if tl.TotalEffective != 13_000 {
		t.Fatalf("TotalEffective=%d, want 13000", tl.TotalEffective)
	}
	if tl.PeakEffective != 9_000 {
		t.Fatalf("PeakEffective=%d, want 9000", tl.PeakEffective)
	}
	if tl.SessionID != "sess-tl" {
		t.Fatalf("SessionID=%q, want sess-tl", tl.SessionID)
	}
	// Single-session axis: PeakInput tracks raw TokensIn,
	// FirstInput is the oldest in-window value, LatestInput is
	// the newest.
	if tl.PeakInput != 9_000 {
		t.Fatalf("PeakInput=%d, want 9000", tl.PeakInput)
	}
	if tl.FirstInput != 5_000 {
		t.Fatalf("FirstInput=%d, want 5000", tl.FirstInput)
	}
	if tl.LatestInput != 9_000 {
		t.Fatalf("LatestInput=%d, want 9000", tl.LatestInput)
	}
}

func TestComputeTimeline_PctOfContext(t *testing.T) {
	cases := []struct {
		name              string
		latest, ctxWindow int64
		want              float64
	}{
		{"50%", 100_000, 200_000, 50},
		{"clamped over 100", 300_000, 200_000, 100},
		{"unknown ctx", 100_000, 0, 0},
		{"empty session", 0, 200_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := TokenTimeline{LatestInput: tc.latest, ContextWindow: tc.ctxWindow}
			if got := tl.PctOfContext(); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeTimeline_PctUsed(t *testing.T) {
	cases := []struct {
		name        string
		total, cap  int64
		wantPctUsed float64
	}{
		{"50%", 50_000, 100_000, 50},
		{"over 100% capped", 200_000, 100_000, 100},
		{"no cap", 50_000, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := TokenTimeline{TotalEffective: tc.total, CapEffective: tc.cap}
			if got := tl.PctUsed(); got != tc.wantPctUsed {
				t.Fatalf("PctUsed=%v, want %v", got, tc.wantPctUsed)
			}
		})
	}
}

func TestComputeTimeline_SkipsZeroTokenMessages(t *testing.T) {
	const now int64 = 100_000
	const window int64 = 60_000
	msgs := []*connectors.Message{
		asstAt(0, now-30_000, 0, 0),     // skipped
		asstAt(1, now-20_000, 5_000, 0), // counted
	}
	tl := ComputeTimeline(msgs, now, window, 0)
	if len(tl.Points) != 1 {
		t.Fatalf("Points=%d, want 1", len(tl.Points))
	}
}

func TestComputeTimeline_EmptyInput(t *testing.T) {
	tl := ComputeTimeline(nil, 100, 60, 0)
	if len(tl.Points) != 0 || tl.TotalEffective != 0 {
		t.Fatalf("expected empty timeline on empty input, got %+v", tl)
	}
}
