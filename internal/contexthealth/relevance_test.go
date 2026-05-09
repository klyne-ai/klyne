package contexthealth

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func TestScoreFiles_StaleAfterTopicShift(t *testing.T) {
	// Opening: user discusses authentication. Two auth files get read,
	// each producing a sizable tool_result. Then the user pivots to
	// billing and the most recent N user messages are billing-only.
	const bigPayload = 12_000

	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login bug"),
		readCall(1, "/repo/auth/login.go"),
		readResult(2, 1, bigPayload),
		userMsg(3, "the session cookie issuer is wrong"),
		readCall(4, "/repo/auth/session.go"),
		readResult(5, 4, bigPayload),
		// Topic pivot — billing only in the last 5 user messages.
		userMsg(6, "now switch to billing refund logic"),
		userMsg(7, "the subscription charge isn't applying"),
		userMsg(8, "billing invoice for the refund flow"),
		userMsg(9, "credit the customer for the failed charge"),
		userMsg(10, "make sure the refund subscription path is right"),
		readCall(11, "/repo/billing/refund.go"),
		readResult(12, 11, bigPayload),
	}

	v := ScoreFiles(msgs)
	if !v.ShouldFire {
		t.Fatalf("expected ShouldFire=true after topic shift; StaleShare=%.2f, TotalBytes=%d, files=%+v",
			v.StaleShare, v.TotalBytes, v.Files)
	}
	if v.StaleShare <= staleShareThreshold {
		t.Fatalf("expected StaleShare > %.2f, got %.2f", staleShareThreshold, v.StaleShare)
	}
	if got := v.RelevantBasenames(); !strings.Contains(got, "refund.go") {
		t.Fatalf("expected RelevantBasenames to include refund.go, got %q", got)
	}
	if strings.Contains(v.RelevantBasenames(), "login.go") || strings.Contains(v.RelevantBasenames(), "session.go") {
		t.Fatalf("auth files leaked into relevant subset: %q", v.RelevantBasenames())
	}
}

func TestScoreFiles_SingleTopicNoFire(t *testing.T) {
	// All user messages and file reads are about the same topic — no
	// shift, no stale share.
	const payload = 12_000
	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login flow"),
		readCall(1, "/repo/auth/login.go"),
		readResult(2, 1, payload),
		userMsg(3, "session cookie token logic in middleware"),
		readCall(4, "/repo/auth/session.go"),
		readResult(5, 4, payload),
		userMsg(6, "auth middleware login flow correctness"),
	}
	v := ScoreFiles(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false on coherent session, got true (StaleShare=%.2f)", v.StaleShare)
	}
}

func TestScoreFiles_BelowMinBytesNoFire(t *testing.T) {
	// Topic shifts but the loaded payload is tiny — gate the trigger
	// on minimum loaded bytes so we don't pester users on small
	// sessions.
	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware"),
		readCall(1, "/repo/auth/login.go"),
		readResult(2, 1, 100),
		userMsg(3, "switch to billing"),
		userMsg(4, "billing refund logic"),
		userMsg(5, "billing invoice charge"),
		userMsg(6, "subscription billing"),
		userMsg(7, "credit billing customer"),
	}
	v := ScoreFiles(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false below min-bytes gate, got true (TotalBytes=%d)", v.TotalBytes)
	}
}

func TestScoreFiles_EmptyInput(t *testing.T) {
	v := ScoreFiles(nil)
	if v.ShouldFire || v.TotalBytes != 0 {
		t.Fatalf("expected zero verdict on empty input, got %+v", v)
	}
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want float64
	}{
		{"empty both", nil, nil, 0},
		{"empty one", []string{"x"}, nil, 0},
		{"identical", []string{"abc", "def", "ghi"}, []string{"abc", "def", "ghi"}, 1.0},
		{"disjoint", []string{"abc", "def"}, []string{"ghi", "jkl"}, 0.0},
		{"half overlap", []string{"abc", "def"}, []string{"def", "ghi"}, 1.0 / 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := bagFromStrings(tc.a)
			b := bagFromStrings(tc.b)
			got := jaccard(a, b)
			if absf(got-tc.want) > 1e-9 {
				t.Fatalf("jaccard(%v,%v)=%v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func bagFromStrings(xs []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range xs {
		out[x] = true
	}
	return out
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
