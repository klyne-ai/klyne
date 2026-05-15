package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// TestXaggParseDuration covers common time-window strings.
func TestXaggParseDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7d", 7 * 24 * time.Hour, true},
		{"30d", 30 * 24 * time.Hour, true},
		{"24h", 24 * time.Hour, true},
		{"1h", time.Hour, true},
		{"bad", 0, false},
	}
	for _, c := range cases {
		got, err := xaggParseDuration(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("xaggParseDuration(%q) error = %v, want nil", c.in, err)
			}
			if got != c.want {
				t.Errorf("xaggParseDuration(%q) = %v, want %v", c.in, got, c.want)
			}
		} else if err == nil {
			t.Errorf("xaggParseDuration(%q) expected error, got nil (result=%v)", c.in, got)
		}
	}
}

// TestXaggSinceWindow verifies flag-to-time-window translation.
func TestXaggSinceWindow(t *testing.T) {
	t.Parallel()
	now := time.Now()

	// --week → approximately 7 days ago.
	got := xaggSinceWindow(xaggFlags{week: true})
	diff := now.Add(-7 * 24 * time.Hour).Sub(got)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("--week window off by %v", diff)
	}

	// --since=30d → approximately 30 days ago.
	got = xaggSinceWindow(xaggFlags{since: "30d"})
	diff = now.Add(-30 * 24 * time.Hour).Sub(got)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("--since=30d window off by %v", diff)
	}

	// No flags → zero time (all-time).
	if !xaggSinceWindow(xaggFlags{}).IsZero() {
		t.Errorf("no flags: expected zero time, got non-zero")
	}
}

// TestXaggListAllSessionsFromDir exercises the session enumerator against a
// synthetic ~/.claude/projects/ directory tree.
func TestXaggListAllSessionsFromDir(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	proj1 := filepath.Join(tmp, "-Users-alice-proj1")
	proj2 := filepath.Join(tmp, "-Users-alice-proj2")
	xaggMust(t, os.MkdirAll(proj1, 0o755))
	xaggMust(t, os.MkdirAll(proj2, 0o755))

	recent := time.Now().Add(-1 * time.Hour)
	old := time.Now().Add(-10 * 24 * time.Hour)

	xaggWriteFile(t, filepath.Join(proj1, "aaa.jsonl"), `{"type":"user","sessionId":"aaa"}`)
	xaggMust(t, os.Chtimes(filepath.Join(proj1, "aaa.jsonl"), recent, recent))

	xaggWriteFile(t, filepath.Join(proj2, "bbb.jsonl"), `{"type":"user","sessionId":"bbb"}`)
	xaggMust(t, os.Chtimes(filepath.Join(proj2, "bbb.jsonl"), old, old))

	// All sessions, no filter.
	entries, err := xaggListAllSessionsFromDir(tmp, time.Time{}, "")
	if err != nil {
		t.Fatalf("no filter: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("len = %d, want 2", len(entries))
	}

	// 7-day filter → only the recent one.
	since7d := time.Now().Add(-7 * 24 * time.Hour)
	entries, err = xaggListAllSessionsFromDir(tmp, since7d, "")
	if err != nil {
		t.Fatalf("7d filter: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len = %d, want 1 (recent only)", len(entries))
	}
	if len(entries) > 0 && entries[0].projectSlug != "-Users-alice-proj1" {
		t.Errorf("slug = %q, want -Users-alice-proj1", entries[0].projectSlug)
	}

	// Project slug filter → only proj2.
	entries, err = xaggListAllSessionsFromDir(tmp, time.Time{}, "-Users-alice-proj2")
	if err != nil {
		t.Fatalf("slug filter: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len = %d, want 1 (proj2 only)", len(entries))
	}
}

// TestXaggToolCount_AddFromJSONL verifies tool call tallying from a JSONL file.
func TestXaggToolCount_AddFromJSONL(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	fixture := filepath.Join(tmp, "session.jsonl")

	content := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__klyne__recall"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"mcp__klyne__remember"}]}}
{"type":"user","message":{"content":"hello"}}
`
	xaggWriteFile(t, fixture, content)

	tc := newXaggToolCount()
	tc.addFromJSONL(fixture)

	if got := tc.bySource["klyne"]; got != 2 {
		t.Errorf("bySource[klyne] = %d, want 2", got)
	}
	if got := tc.bySource["anthropic-builtins"]; got != 1 {
		t.Errorf("bySource[anthropic-builtins] = %d, want 1", got)
	}
	if got := tc.byTool["mcp__klyne__recall"]; got != 1 {
		t.Errorf("byTool[mcp__klyne__recall] = %d, want 1", got)
	}
}

// TestXaggRenderScorecard_Structure checks that all expected sections and
// redundancy markers appear in the aggregate scorecard output.
func TestXaggRenderScorecard_Structure(t *testing.T) {
	t.Parallel()

	entries := []xaggSessionEntry{
		{projectSlug: "-proj1"},
		{projectSlug: "-proj1"},
		{projectSlug: "-proj2"},
	}
	rows := []contexthealth.AggregatedRow{
		{Name: "superpowers", Kind: contexthealth.SourceKindSkill, TotalTokens: 62000, SessionCount: 3, InvocationCount: 100},
		// serena: 3 sessions, 0 calls → hard redundant (✗).
		{Name: "serena", Kind: contexthealth.SourceKindMCP, TotalTokens: 18000, SessionCount: 3, InvocationCount: 0},
		// playwright: 2 sessions, 0 calls → NOT hard redundant (too few sessions).
		{Name: "playwright", Kind: contexthealth.SourceKindMCP, TotalTokens: 5000, SessionCount: 2, InvocationCount: 0},
		// linear: 10 sessions, 1 call → soft redundant (⚠).
		{Name: "linear", Kind: contexthealth.SourceKindMCP, TotalTokens: 3000, SessionCount: 10, InvocationCount: 1},
	}
	toolCounts := newXaggToolCount()
	toolCounts.bySource["anthropic-builtins"] = 412
	toolCounts.bySource["klyne"] = 94

	out := xaggRenderScorecard(xaggFlags{week: true}, entries, 2, rows, toolCounts)

	mustContain := []struct {
		label string
		s     string
	}{
		{"header", "klyne xray — last 7 days"},
		{"session count", "3 sessions"},
		{"project count", "2 projects"},
		{"superpowers row", "superpowers"},
		{"serena tokens", "18K tokens"},
		{"hard redundancy marker", "✗ never invoked"},
		{"soft redundancy marker", "⚠ redundant?"},
		{"tool section header", "Tool-invocation counts"},
		{"anthropic-builtins in tool section", "anthropic-builtins"},
		{"redundancy summary", "Top redundant sources:"},
		{"serena in summary", "serena"},
	}
	for _, c := range mustContain {
		if !strings.Contains(out, c.s) {
			t.Errorf("scorecard missing %s (%q)\n--- output ---\n%s", c.label, c.s, out)
		}
	}

	// playwright has only 2 sessions and must NOT appear in the redundancy
	// summary section (it only needs to appear in the source table above).
	summaryIdx := strings.Index(out, "Top redundant sources:")
	if summaryIdx >= 0 {
		summarySection := out[summaryIdx:]
		if strings.Contains(summarySection, "playwright") {
			t.Errorf("playwright (2 sessions) must not appear in redundancy summary; output:\n%s", out)
		}
	}
}

// TestXaggCmd_WeekFlag_NoSessions verifies graceful output when HOME has no sessions.
func TestXaggCmd_WeekFlag_NoSessions(t *testing.T) {
	t.Parallel()
	oldHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", oldHome) }()
	_ = os.Setenv("HOME", t.TempDir())

	cmd := newXrayCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := runXrayAggregate(cmd, xaggFlags{week: true})
	if err != nil {
		t.Errorf("expected nil err for empty session set; got: %v", err)
	}
	if !strings.Contains(out.String(), "no sessions") {
		t.Errorf("expected 'no sessions' message; got: %q", out.String())
	}
}

// TestXaggInvocationsForSource verifies fuzzy name matching.
func TestXaggInvocationsForSource(t *testing.T) {
	t.Parallel()
	bySource := map[string]int{
		"klyne":              42,
		"anthropic-builtins": 100,
	}
	cases := []struct {
		name string
		want int
	}{
		{"klyne hooks", 42},
		{"klyne MCP", 42},
		{"klyne", 42},
		{"playwright", 0},     // no match
		{"anthropic-builtins", 100},
	}
	for _, c := range cases {
		got := xaggInvocationsForSource(c.name, bySource)
		if got != c.want {
			t.Errorf("xaggInvocationsForSource(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestXaggBareXrayUnchanged ensures no-flag `klyne xray` still hits the
// per-session code path and is not accidentally routed to aggregate mode.
func TestXaggBareXrayUnchanged(t *testing.T) {
	t.Parallel()
	cmd := newXrayCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// per-session path handles missing session gracefully.
	err := runXray(cmd, "nonexistent-id-that-does-not-exist")
	if err != nil {
		t.Errorf("bare xray should return nil for missing session; got: %v", err)
	}
}

// TestXaggHeaderDesc verifies the header string for various flag combinations.
func TestXaggHeaderDesc(t *testing.T) {
	t.Parallel()
	entries := make([]xaggSessionEntry, 3)
	for i := range entries {
		entries[i] = xaggSessionEntry{projectSlug: "-proj1"}
	}
	got := xaggHeaderDesc(xaggFlags{week: true}, entries, 1)
	if !strings.Contains(got, "last 7 days") {
		t.Errorf("expected 'last 7 days'; got %q", got)
	}
	if !strings.Contains(got, "3 sessions") {
		t.Errorf("expected '3 sessions'; got %q", got)
	}
	if !strings.Contains(got, "1 project") {
		t.Errorf("expected '1 project'; got %q", got)
	}

	got = xaggHeaderDesc(xaggFlags{since: "30d"}, entries, 2)
	if !strings.Contains(got, "last 30d") {
		t.Errorf("expected 'last 30d'; got %q", got)
	}

	// --project flag → "this project" instead of a count.
	got = xaggHeaderDesc(xaggFlags{week: true, project: true}, entries, 1)
	if !strings.Contains(got, "this project") {
		t.Errorf("expected 'this project'; got %q", got)
	}
}

// --- helpers -----------------------------------------------------------

func xaggMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func xaggWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}

// xaggListAllSessionsFromDir is the testable variant of xaggListAllSessions
// that accepts an explicit projectsDir so tests can use t.TempDir() without
// overriding $HOME.
func xaggListAllSessionsFromDir(projectsDir string, since time.Time, projectSlugFilter string) ([]xaggSessionEntry, error) {
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []xaggSessionEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		slug := d.Name()
		if projectSlugFilter != "" && slug != projectSlugFilter {
			continue
		}
		projPath := filepath.Join(projectsDir, slug)
		files, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if !since.IsZero() && info.ModTime().Before(since) {
				continue
			}
			entries = append(entries, xaggSessionEntry{
				path:        filepath.Join(projPath, f.Name()),
				modTime:     info.ModTime(),
				projectSlug: slug,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})
	if len(entries) > xaggMaxSessions {
		entries = entries[:xaggMaxSessions]
	}
	return entries, nil
}
