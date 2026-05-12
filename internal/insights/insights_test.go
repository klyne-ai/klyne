package insights

import (
	"testing"
)

func makeStats(id string, toolRuns []string, tokensIn, cachedRead int64, cost float64) SessionStats {
	s := SessionStats{
		ID:               id,
		CLI:              "claude",
		ProjectPath:      "/tmp/p",
		TokensIn:         tokensIn,
		CachedReadTokens: cachedRead,
		CostUSD:          cost,
		ToolCounts:       map[string]int{},
		ToolErrors:       map[string]int{},
	}
	if tokensIn > 0 {
		s.CacheReadRatio = float64(cachedRead) / float64(tokensIn)
	}
	var prev string
	var run int
	for _, t := range toolRuns {
		s.ToolCounts[t]++
		s.TotalToolCalls++
		if t == prev {
			run++
		} else {
			run = 1
			prev = t
		}
		if run > s.LongestRun {
			s.LongestRun = run
			s.LongestRunTool = t
		}
	}
	return s
}

func TestAggregateTools(t *testing.T) {
	a := makeStats("a", []string{"Read", "Bash", "Bash", "Edit"}, 0, 0, 0)
	b := makeStats("b", []string{"Bash", "Bash", "Read"}, 0, 0, 0)
	out := AggregateTools([]SessionStats{a, b})

	if len(out) != 3 {
		t.Fatalf("expected 3 tools, got %d: %+v", len(out), out)
	}
	if out[0].Name != "Bash" || out[0].Count != 4 {
		t.Errorf("expected Bash=4 first, got %+v", out[0])
	}
	if out[0].SessionCount != 2 {
		t.Errorf("expected Bash in 2 sessions, got %d", out[0].SessionCount)
	}
	// deterministic tie-break: Edit and Read both count 2 — alphabetical
	if out[1].Name != "Read" {
		t.Errorf("expected Read second (Count=2, deterministic), got %s", out[1].Name)
	}
}

func TestDetectPatterns_TightLoop(t *testing.T) {
	stats := []SessionStats{
		makeStats("loopy", []string{"Bash", "Bash", "Bash", "Bash", "Bash", "Bash"}, 100_000, 60_000, 1.0),
	}
	ps := DetectPatterns(stats, DefaultThresholds())
	found := false
	for _, p := range ps {
		if p.Kind == KindTightLoop && p.SessionID == "loopy" {
			found = true
			if p.Metric != 6 {
				t.Errorf("tight_loop metric = %v, want 6", p.Metric)
			}
		}
	}
	if !found {
		t.Errorf("expected tight_loop pattern, got %+v", ps)
	}
}

func TestDetectPatterns_BashOveruse(t *testing.T) {
	// 11 tool calls, 8 Bash = 72% — well over 40% threshold and above MinToolCalls.
	calls := []string{"Bash", "Bash", "Bash", "Bash", "Read", "Bash", "Bash", "Edit", "Bash", "Glob", "Bash"}
	stats := []SessionStats{
		makeStats("bashy", calls, 100_000, 60_000, 1.0),
	}
	ps := DetectPatterns(stats, DefaultThresholds())
	found := false
	for _, p := range ps {
		if p.Kind == KindBashOveruse {
			found = true
			if p.Metric < 0.5 {
				t.Errorf("bash ratio = %.2f, want >= 0.5", p.Metric)
			}
		}
	}
	if !found {
		t.Errorf("expected bash_overuse pattern, got %+v", ps)
	}
}

func TestDetectPatterns_LowCacheReuse(t *testing.T) {
	// Big input, almost no cache.
	stats := []SessionStats{
		makeStats("cold", []string{"Read", "Read"}, 200_000, 10_000, 2.0),
	}
	ps := DetectPatterns(stats, DefaultThresholds())
	found := false
	for _, p := range ps {
		if p.Kind == KindLowCacheReuse {
			found = true
			if p.Metric > 0.1 {
				t.Errorf("cache ratio = %.2f, want <= 0.1", p.Metric)
			}
		}
	}
	if !found {
		t.Errorf("expected low_cache_reuse, got %+v", ps)
	}
}

func TestDetectPatterns_SkipsTinySessions(t *testing.T) {
	// 5 tool calls — below MinToolCalls (10) — no bash overuse should fire.
	calls := []string{"Bash", "Bash", "Bash", "Bash", "Bash"}
	stats := []SessionStats{
		makeStats("tiny", calls, 1_000, 0, 0.0),
	}
	ps := DetectPatterns(stats, DefaultThresholds())
	for _, p := range ps {
		if p.Kind == KindBashOveruse {
			t.Errorf("bash overuse should not fire on tiny session: %+v", p)
		}
		if p.Kind == KindLowCacheReuse {
			t.Errorf("cache check should not fire when TokensIn < MinTokens: %+v", p)
		}
	}
}

func TestDetectPatterns_DeterministicOrder(t *testing.T) {
	s1 := makeStats("z", []string{"Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash"}, 100_000, 10_000, 1.0)
	s2 := makeStats("a", []string{"Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash"}, 100_000, 10_000, 1.0)
	a := DetectPatterns([]SessionStats{s1, s2}, DefaultThresholds())
	b := DetectPatterns([]SessionStats{s2, s1}, DefaultThresholds())
	if len(a) != len(b) {
		t.Fatalf("non-deterministic length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].SessionID != b[i].SessionID {
			t.Errorf("non-deterministic order at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestTotalCostAndTokens(t *testing.T) {
	stats := []SessionStats{
		makeStats("a", nil, 1000, 200, 0.50),
		makeStats("b", nil, 2000, 800, 0.75),
	}
	if got := TotalCost(stats); got != 1.25 {
		t.Errorf("TotalCost = %v, want 1.25", got)
	}
	in, _, cr, _ := TotalTokens(stats)
	if in != 3000 || cr != 1000 {
		t.Errorf("TotalTokens(in=%d, cachedRead=%d), want 3000 / 1000", in, cr)
	}
}

func TestGenerateRoasts_EmptyInput(t *testing.T) {
	r := GenerateRoasts(RoastInput{}, 5)
	if len(r) != 1 || r[0].Kind != "empty" {
		t.Errorf("expected single 'empty' roast on no sessions, got %+v", r)
	}
}

func TestGenerateRoasts_HighSpend(t *testing.T) {
	s := makeStats("a", []string{"Bash"}, 100, 50, 150.0)
	in := BuildRoastInput([]SessionStats{s}, AggregateTools([]SessionStats{s}))
	rs := GenerateRoasts(in, 10)
	found := false
	for _, r := range rs {
		if r.Kind == "spend" && r.Metric >= 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected high-spend roast, got %+v", rs)
	}
}

func TestBuildRoastInput_TopProject(t *testing.T) {
	a := makeStats("1", nil, 0, 0, 0)
	a.ProjectPath = "/proj/alpha"
	b := makeStats("2", nil, 0, 0, 0)
	b.ProjectPath = "/proj/alpha"
	c := makeStats("3", nil, 0, 0, 0)
	c.ProjectPath = "/proj/alpha"
	d := makeStats("4", nil, 0, 0, 0)
	d.ProjectPath = "/proj/beta"
	in := BuildRoastInput([]SessionStats{a, b, c, d}, nil)
	if in.TopProject != "/proj/alpha" {
		t.Errorf("TopProject = %q, want /proj/alpha", in.TopProject)
	}
	if in.TopProjectShare != 0.75 {
		t.Errorf("TopProjectShare = %v, want 0.75", in.TopProjectShare)
	}
}
