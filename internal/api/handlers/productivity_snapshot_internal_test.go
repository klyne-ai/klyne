package handlers

import (
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// TestSingleProjectReport_RoundTripWithOrphans guards the orphan-
// session attribution fix (release-blocker the review caught): a
// session whose Repo doesn't match any Service was being silently
// dropped from every per-project snapshot, so re-aggregating N per-
// project payloads under-counted by the orphan minutes. The fix attaches
// orphans to the first service's snapshot; this test asserts that the
// round-trip (split → AggregateReports) recovers TotalActiveMinutes
// exactly.
func TestSingleProjectReport_RoundTripWithOrphans(t *testing.T) {
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	mkInterval := func(off, dur int) productivity.ActiveInterval {
		return productivity.ActiveInterval{
			Start: base.Add(time.Duration(off) * time.Minute),
			End:   base.Add(time.Duration(off+dur) * time.Minute),
		}
	}
	mkSession := func(id, repo string, off, dur int) productivity.SessionStat {
		iv := mkInterval(off, dur)
		return productivity.SessionStat{
			SessionID: id, CLI: "claude", Repo: repo,
			StartedAt: iv.Start, EndedAt: iv.End,
			ActiveIntervals: []productivity.ActiveInterval{iv},
			ActiveMinutes:   dur, MessageCount: 1,
		}
	}

	rep := productivity.Report{
		Day: "2026-05-24",
		Services: []productivity.Service{
			{Repo: "alpha", ProjectPath: "/a"},
			{Repo: "beta", ProjectPath: "/b"},
		},
		Sessions: []productivity.SessionStat{
			mkSession("s1", "alpha", 0, 30),    // 30m in alpha
			mkSession("s2", "beta", 60, 20),    // 20m in beta
			mkSession("s3", "orphan", 120, 10), // 10m in NO service
		},
		MinutesByCLI:       map[string]int{"claude": 60},
		TotalActiveMinutes: 60, // 30 + 20 + 10, disjoint intervals
	}

	// Match the persist path: orphans attach to the FIRST service.
	orphans := []productivity.SessionStat{rep.Sessions[2]}
	slice0 := singleProjectReport(rep, rep.Services[0], "2026-05-24", orphans)
	slice1 := singleProjectReport(rep, rep.Services[1], "2026-05-24", nil)

	if got := slice0.TotalActiveMinutes; got != 40 {
		t.Fatalf("first slice should carry alpha(30) + orphan(10) = 40, got %d", got)
	}
	if got := slice1.TotalActiveMinutes; got != 20 {
		t.Fatalf("second slice should be beta(20), got %d", got)
	}

	merged := productivity.AggregateReports([]productivity.Report{slice0, slice1}, "2026-05-24")
	if merged.TotalActiveMinutes != rep.TotalActiveMinutes {
		t.Fatalf("orphan round-trip: merged TotalActiveMinutes = %d, want %d (lost orphan)",
			merged.TotalActiveMinutes, rep.TotalActiveMinutes)
	}
	if got := len(merged.Sessions); got != 3 {
		t.Fatalf("expected all 3 sessions to survive round-trip, got %d", got)
	}
}

// TestLocalDaysInRange_DSTSafe — exercises the +1-day arithmetic that
// replaced +24h. We can't easily fabricate a DST timezone here, but we
// can assert basic correctness: 3-day window yields 3 day-buckets, no
// drift, capped at 31.
func TestLocalDaysInRange_Bounds(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 5, 24, 10, 0, 0, 0, loc)
	end := time.Date(2026, 5, 26, 23, 0, 0, 0, loc)
	got := localDaysInRange(start, end)
	if len(got) != 3 {
		t.Fatalf("3-day window: got %d buckets, want 3", len(got))
	}
	for i, want := range []string{"2026-05-24", "2026-05-25", "2026-05-26"} {
		if got[i].Format("2006-01-02") != want {
			t.Fatalf("day[%d] = %s, want %s", i, got[i].Format("2006-01-02"), want)
		}
	}

	// 60-day window must cap at 31.
	farEnd := start.AddDate(0, 0, 60)
	if n := len(localDaysInRange(start, farEnd)); n != 31 {
		t.Fatalf("60-day window: got %d buckets, want 31 cap", n)
	}

	// until before since → empty.
	if n := len(localDaysInRange(end, start)); n != 0 {
		t.Fatalf("inverted window should be empty, got %d", n)
	}
}
