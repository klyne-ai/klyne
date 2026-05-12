package api

import "testing"

// TestAllRoutesUnique guards the W0 contract that every Route* constant
// is a unique HTTP path. A duplicate would silently shadow a handler at
// runtime when the chi router is wired in W7.
func TestAllRoutesUnique(t *testing.T) {
	t.Parallel()

	routes := AllRoutes()
	seen := make(map[string]int, len(routes))
	for i, r := range routes {
		if r == "" {
			t.Errorf("AllRoutes()[%d] is empty", i)
			continue
		}
		if prev, dup := seen[r]; dup {
			t.Errorf("duplicate route %q at indices %d and %d", r, prev, i)
		}
		seen[r] = i
	}
}

// TestAllRoutesContainsExpected guards the route table against accidental
// removals: every endpoint promised in docs/plan/04-shared-contracts.md
// §5 must appear in AllRoutes(). If a route is intentionally retired,
// that requires a `contract-change` PR which will update this list.
func TestAllRoutesContainsExpected(t *testing.T) {
	t.Parallel()

	expected := []string{
		RouteSessions,
		RouteSession,
		RouteSessionMessages,
		RouteSessionRestore,
		RouteSessionSummary,
		RouteSessionUsage,
		RouteSessionBreakAdvice,
		RouteSearch,
		RouteCostSummary,
		RouteUsage,
		RouteUsageStats,
		RouteCockpitThreads,
		RouteAdvisories,
		RouteSessionAdvisorDetail,
		RouteSettings,
		RouteWizardDetect,
		RouteWizardComplete,
		RouteEvents,
		RouteHealthz,
	}
	got := AllRoutes()
	if len(got) != len(expected) {
		t.Fatalf("AllRoutes len = %d, want %d", len(got), len(expected))
	}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("AllRoutes()[%d] = %q, want %q", i, got[i], want)
		}
	}
}
