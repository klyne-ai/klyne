package richentry

import (
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// --- helper construction --------------------------------------------------

// allowlistWith builds a minimal Allowlist where only the requested
// shapes are populated — lets each test isolate one rule.
func allowlistWith(shas, prs, tickets, paths, durs, clocks, sessionIDs []string, prLive bool) Allowlist {
	toSet := func(xs []string) map[string]bool {
		out := map[string]bool{}
		for _, x := range xs {
			out[x] = true
		}
		return out
	}
	return Allowlist{
		ShortSHAs:   toSet(shas),
		PRNumbers:   toSet(prs),
		PRCacheLive: prLive,
		TicketIDs:   toSet(tickets),
		FilePaths:   toSet(paths),
		Durations:   toSet(durs),
		Clocks:      toSet(clocks),
		SessionIDs:  toSet(sessionIDs),
	}
}

func entryWith(category string, refs ...string) store.WorklogEntryJSON {
	return store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			category: {{Summary: "x", Refs: refs}},
		},
	}
}

// --- SHA -------------------------------------------------------------------

func TestValidator_AllowsRealSHA(t *testing.T) {
	al := allowlistWith([]string{"c5c97a86"}, nil, nil, nil, nil, nil, nil, true)
	errs := Validate(entryWith("shipped", "c5c97a86"), al)
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %+v", errs)
	}
}

func TestValidator_RejectsFakeSHA(t *testing.T) {
	al := allowlistWith([]string{"c5c97a86"}, nil, nil, nil, nil, nil, nil, true)
	errs := Validate(entryWith("shipped", "aaaaaaaa"), al)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %+v", errs)
	}
	if errs[0].Reason != "SHA not in commit list" {
		t.Errorf("reason: %q", errs[0].Reason)
	}
}

// --- UUID (the regression-class fix) ---------------------------------------

func TestValidator_RejectsUUID_NotInAllowlist(t *testing.T) {
	// e4696ed8-... is the literal regression UUID. Without it in
	// SessionIDs, validator must reject.
	al := allowlistWith(nil, nil, nil, nil, nil, nil, nil, true)
	errs := Validate(entryWith("must_remember", "e4696ed8-c2e9-416e-9a2d-a6e4860e649d"), al)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %+v", errs)
	}
}

func TestValidator_AcceptsUUID_InAllowlistAndUnique(t *testing.T) {
	id := "e4696ed8-c2e9-416e-9a2d-a6e4860e649d"
	al := allowlistWith(nil, nil, nil, nil, nil, nil, []string{id}, true)
	errs := Validate(entryWith("must_remember", id), al)
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %+v", errs)
	}
}

func TestValidator_RejectsUUID_ReusedAcrossBullets(t *testing.T) {
	// THE actual regression pattern: same valid session_id cited under
	// every bullet. Must reject all occurrences after the first uses.
	id := "e4696ed8-c2e9-416e-9a2d-a6e4860e649d"
	al := allowlistWith(nil, nil, nil, nil, nil, nil, []string{id}, true)
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"shipped":       {{Summary: "a", Refs: []string{id}}},
			"investigations": {{Summary: "b", Refs: []string{id}}},
		},
	}
	errs := Validate(entry, al)
	if len(errs) != 2 {
		t.Fatalf("expected 2 reuse errors (one per bullet); got %+v", errs)
	}
	for _, e := range errs {
		if e.Reason != "uuid reused across multiple bullets (regression class)" {
			t.Errorf("wrong reason: %q", e.Reason)
		}
	}
}

// --- PR --------------------------------------------------------------------

func TestValidator_AllowsKnownPR(t *testing.T) {
	al := allowlistWith(nil, []string{"#400"}, nil, nil, nil, nil, nil, true)
	if errs := Validate(entryWith("shipped", "#400"), al); len(errs) != 0 {
		t.Errorf("got %+v", errs)
	}
}

func TestValidator_RejectsUnknownPR_WhenCacheLive(t *testing.T) {
	al := allowlistWith(nil, []string{"#400"}, nil, nil, nil, nil, nil, true)
	errs := Validate(entryWith("shipped", "#999"), al)
	if len(errs) != 1 || errs[0].Reason != "PR not in allowlist" {
		t.Errorf("got %+v", errs)
	}
}

func TestValidator_SkipsPRCheck_WhenCacheStale(t *testing.T) {
	// Fresh repo / cache miss — validator passes any PR ref structurally
	// rather than reject (the Phase 4 writer logs this so we can detect
	// "passing because cache missing" downstream).
	al := allowlistWith(nil, nil, nil, nil, nil, nil, nil, false)
	if errs := Validate(entryWith("shipped", "#999"), al); len(errs) != 0 {
		t.Errorf("expected no errors with stale cache; got %+v", errs)
	}
}

// --- ticket ----------------------------------------------------------------

func TestValidator_AllowsKnownTicket(t *testing.T) {
	al := allowlistWith(nil, nil, []string{"CLI-1325"}, nil, nil, nil, nil, true)
	if errs := Validate(entryWith("features_worked_on", "CLI-1325"), al); len(errs) != 0 {
		t.Errorf("got %+v", errs)
	}
}

func TestValidator_RejectsUnknownTicket(t *testing.T) {
	al := allowlistWith(nil, nil, []string{"CLI-1325"}, nil, nil, nil, nil, true)
	if errs := Validate(entryWith("features_worked_on", "CLI-9999"), al); len(errs) != 1 {
		t.Errorf("got %+v", errs)
	}
}

// --- file path -------------------------------------------------------------

func TestValidator_AllowsKnownPath(t *testing.T) {
	al := allowlistWith(nil, nil, nil, []string{"src/foo.ts"}, nil, nil, nil, true)
	if errs := Validate(entryWith("features_worked_on", "src/foo.ts"), al); len(errs) != 0 {
		t.Errorf("got %+v", errs)
	}
}

func TestValidator_RejectsInventedPath(t *testing.T) {
	al := allowlistWith(nil, nil, nil, []string{"src/foo.ts"}, nil, nil, nil, true)
	errs := Validate(entryWith("features_worked_on", "nonsense/path.go"), al)
	if len(errs) != 1 || errs[0].Reason != "path not in session's touched-files" {
		t.Errorf("got %+v", errs)
	}
}

// --- duration --------------------------------------------------------------

func TestValidator_AllowsKnownDuration(t *testing.T) {
	al := allowlistWith(nil, nil, nil, nil, []string{"~2h 14m"}, nil, nil, true)
	if errs := Validate(entryWith("investigations", "~2h 14m"), al); len(errs) != 0 {
		t.Errorf("got %+v", errs)
	}
}

// --- HH:MM clock -----------------------------------------------------------

func TestValidator_AllowsKnownClock_HHMM(t *testing.T) {
	al := allowlistWith(nil, nil, nil, nil, nil, []string{"13:50"}, nil, true)
	if errs := Validate(entryWith("shipped", "13:50"), al); len(errs) != 0 {
		t.Errorf("got %+v", errs)
	}
}

func TestValidator_RejectsClockNotInSession(t *testing.T) {
	al := allowlistWith(nil, nil, nil, nil, nil, []string{"13:50"}, nil, true)
	errs := Validate(entryWith("shipped", "09:00"), al)
	if len(errs) != 1 || errs[0].Reason != "clock time not at any commit or interval endpoint" {
		t.Errorf("got %+v", errs)
	}
}

// --- BuildAllowlist --------------------------------------------------------

func TestBuildAllowlist_FromSessionInputs(t *testing.T) {
	cmts := []CommitRef{
		{SHA: "c5c97a86", Files: []string{"src/foo.ts"}, CommittedAt: time.Date(2026, 5, 19, 13, 50, 0, 0, time.UTC)},
		{SHA: "e2850e29"},
	}
	prs := []int{400, 142}
	branches := []string{"feature/CLI-1325-labstack", "main"}
	files := []string{"package.json"}
	intervals := []Interval{
		{Start: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 5, 19, 12, 14, 0, 0, time.UTC)},
		{Start: time.Date(2026, 5, 19, 13, 0, 0, 0, time.UTC), End: time.Date(2026, 5, 19, 13, 30, 0, 0, time.UTC)},
	}
	priorSessionIDs := []string{"e4696ed8-c2e9-416e-9a2d-a6e4860e649d"}

	al := BuildAllowlist(cmts, prs, branches, files, intervals, priorSessionIDs)

	if !al.ShortSHAs["c5c97a86"] {
		t.Errorf("missing sha c5c97a86: %v", al.ShortSHAs)
	}
	if !al.ShortSHAs["e2850e29"] {
		t.Errorf("missing sha e2850e29")
	}
	if !al.PRNumbers["#400"] {
		t.Errorf("missing pr #400: %v", al.PRNumbers)
	}
	if !al.PRCacheLive {
		t.Errorf("expected PRCacheLive=true when prs non-nil")
	}
	if !al.TicketIDs["CLI-1325"] {
		t.Errorf("missing ticket CLI-1325: %v", al.TicketIDs)
	}
	if !al.FilePaths["src/foo.ts"] {
		t.Errorf("missing path from commit")
	}
	if !al.FilePaths["package.json"] {
		t.Errorf("missing path from session files")
	}
	if !al.Durations["~2h 14m"] {
		t.Errorf("missing duration ~2h 14m: %v", al.Durations)
	}
	if !al.Durations["~30m"] {
		t.Errorf("missing duration ~30m: %v", al.Durations)
	}
	if !al.Clocks["13:50"] {
		t.Errorf("missing clock 13:50 (commit time): %v", al.Clocks)
	}
	if !al.Clocks["10:00"] {
		t.Errorf("missing clock 10:00 (interval start)")
	}
	if !al.SessionIDs["e4696ed8-c2e9-416e-9a2d-a6e4860e649d"] {
		t.Errorf("missing session id")
	}
}

func TestBuildAllowlist_PRCacheLive_FalseWhenPRsNil(t *testing.T) {
	al := BuildAllowlist(nil, nil, nil, nil, nil, nil)
	if al.PRCacheLive {
		t.Errorf("PRCacheLive should be false when prs is nil")
	}
}

func TestBuildAllowlist_PRCacheLive_TrueWhenEmptyList(t *testing.T) {
	// Empty slice (gh cache returned zero PRs but cache is live) vs nil
	// (no cache lookup happened) — explicit []int{} distinguishes them.
	al := BuildAllowlist(nil, []int{}, nil, nil, nil, nil)
	if !al.PRCacheLive {
		t.Errorf("PRCacheLive should be true when prs is empty slice (cache hit, zero PRs)")
	}
}
