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

func TestScoreFiles_RecentTouchOverridesVocabDrift(t *testing.T) {
	// Long iterative coding session: the user types ONE initial task
	// framing, then a series of short follow-up nudges whose
	// vocabulary does NOT overlap the original task. Throughout,
	// Claude continues to Edit the same file. Each Edit sits deep
	// in an assistant-only chain (3 asst turns between user and
	// edit), so the 3-message backward window captures no fresh
	// user vocabulary.
	//
	// Under a pure-vocab algorithm the file's anchor never grows
	// past the original seed, the "current direction" bag has
	// disjoint terms, and the Jaccard score collapses to 0 — so
	// the file gets marked stale and the advisor recommends a
	// handoff. But the user is ACTIVELY editing the file: recency
	// of touch must override the vocab signal.
	//
	// This is the dominant real-world failure mode (an iterative
	// edit session where the user types one big task framing and
	// then short incremental nudges). The clean topic-pivot test
	// alone is not enough to catch it.
	const payload = 12_000

	msgs := []*connectors.Message{
		userMsg(0, "build the pharmacy bill generation feature"),
		readCall(1, "/repo/billing/bill.tsx"),
		readResult(2, 1, payload),
	}
	nudges := []string{
		"now adjust the line item totals",
		"now replace the table header styles",
		"now update the discount calculation",
		"now wire the print preview button",
		"now fix the page total computation",
	}
	idx := 3
	for _, n := range nudges {
		msgs = append(msgs, userMsg(idx, n))
		idx++
		msgs = append(msgs, asstMsg(idx, "okay"))
		idx++
		msgs = append(msgs, asstMsg(idx, "thinking"))
		idx++
		msgs = append(msgs, asstMsg(idx, "let me try"))
		idx++
		msgs = append(msgs, editCall(idx, "/repo/billing/bill.tsx"))
		idx++
	}

	v := ScoreFiles(msgs)
	if v.ShouldFire {
		t.Fatalf("expected ShouldFire=false on iterative editing of a single file; "+
			"got StaleShare=%.2f StaleBytes=%d TotalBytes=%d",
			v.StaleShare, v.StaleBytes, v.TotalBytes)
	}
	for _, f := range v.Files {
		if f.Path == "/repo/billing/bill.tsx" && f.Stale {
			t.Fatalf("bill.tsx is being actively edited but marked stale "+
				"(Score=%.2f Bytes=%d)", f.Score, f.Bytes)
		}
	}
}

func TestScoreFiles_OldFileNotRecentlyTouched_StaysStale(t *testing.T) {
	// Regression guard for the recency override: a file Read at
	// the very start of the session and never touched again must
	// still be flagged stale when the user pivots topics. The
	// override only protects files actively in use, not files
	// merely loaded once long ago.
	const (
		stalePayload = 24_000
		freshPayload = 12_000
	)

	msgs := []*connectors.Message{
		userMsg(0, "fix the authentication middleware login bug"),
		readCall(1, "/repo/auth/login.go"),
		readResult(2, 1, stalePayload),
		// Pivot to billing with five fresh user messages and a new file.
		userMsg(3, "now switch to billing refund logic"),
		userMsg(4, "the subscription charge isn't applying"),
		userMsg(5, "billing invoice for the refund flow"),
		userMsg(6, "credit the customer for the failed charge"),
		userMsg(7, "make sure the refund subscription path is right"),
		readCall(8, "/repo/billing/refund.go"),
		readResult(9, 8, freshPayload),
	}

	v := ScoreFiles(msgs)
	if !v.ShouldFire {
		t.Fatalf("expected ShouldFire=true; login.go is old AND off-topic. "+
			"StaleShare=%.2f files=%+v", v.StaleShare, v.Files)
	}
	var sawLogin bool
	for _, f := range v.Files {
		if f.Path == "/repo/auth/login.go" {
			sawLogin = true
			if !f.Stale {
				t.Fatalf("login.go is old and off-topic but not stale (Score=%.2f)", f.Score)
			}
		}
		if f.Path == "/repo/billing/refund.go" && f.Stale {
			t.Fatalf("refund.go is recent and on-topic but marked stale (Score=%.2f)", f.Score)
		}
	}
	if !sawLogin {
		t.Fatalf("login.go missing from verdict files: %+v", v.Files)
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
