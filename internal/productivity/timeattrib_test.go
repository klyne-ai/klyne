package productivity

import (
	"testing"
	"time"
)

func ts(base time.Time, mins ...int) []time.Time {
	out := make([]time.Time, len(mins))
	for i, m := range mins {
		out[i] = base.Add(time.Duration(m) * time.Minute)
	}
	return out
}

func TestAttributeMinutes_IdleCapExcludesGaps(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	// Messages at 0,5,10 min, then a 90-min gap, then 100,105.
	// With a 30-min idle cap: active = (10-0) + (105-100) = 15 min.
	sess := []SessionActivity{{
		SessionID:    "s1",
		ProjectPath:  "/repo/a",
		MessageTimes: ts(base, 0, 5, 10, 100, 105),
	}}
	res := AttributeMinutes(sess, map[string]int{"/repo/a": 3}, 30)
	if got := res["/repo/a"].AIMinutes; got != 15 {
		t.Errorf("AIMinutes = %d; want 15 (gap > cap excluded)", got)
	}
	if res["/repo/a"].ManualOnly {
		t.Errorf("ManualOnly = true; want false (session present)")
	}
}

func TestAttributeMinutes_MultiRepoSplitByCommitCount(t *testing.T) {
	base := time.Date(2026, 5, 19, 14, 0, 0, 0, time.UTC)
	// One session, 60 contiguous active minutes (msgs every 10m, under cap).
	// Window has commits in 2 repos: 3 in /a, 1 in /b → split 45 / 15.
	sess := []SessionActivity{{
		SessionID:    "s2",
		ProjectPath:  "/multi",
		MessageTimes: ts(base, 0, 10, 20, 30, 40, 50, 60),
		Repos:        []string{"/a", "/b"},
	}}
	res := AttributeMinutes(sess, map[string]int{"/a": 3, "/b": 1}, 30)
	if res["/a"].AIMinutes != 45 {
		t.Errorf("/a AIMinutes = %d; want 45 (3/4 of 60)", res["/a"].AIMinutes)
	}
	if res["/b"].AIMinutes != 15 {
		t.Errorf("/b AIMinutes = %d; want 15 (1/4 of 60)", res["/b"].AIMinutes)
	}
	if !res["/a"].Split || !res["/b"].Split {
		t.Errorf("Split flag not set on multi-repo attribution")
	}
}

func TestAttributeMinutes_ManualOnlyWhenNoSession(t *testing.T) {
	// Repo /manual has in-window commits but NO session active-time (D4:
	// manual time is never folded into AI time).
	res := AttributeMinutes(nil, map[string]int{"/manual": 2}, 30)
	r := res["/manual"]
	if r.AIMinutes != 0 {
		t.Errorf("AIMinutes = %d; want 0 (no session)", r.AIMinutes)
	}
	if !r.ManualOnly {
		t.Errorf("ManualOnly = false; want true (commits but no AI session)")
	}
}
