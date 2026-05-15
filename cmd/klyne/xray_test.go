package main

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// fixtureDir returns the testdata directory path relative to this file.
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata")
}

func TestRunXray_FixtureProducesScorecard(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join(fixtureDir(t), "xray-fixture.jsonl")

	snap, err := mcpserver.LoadSnapshot(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	res := contexthealth.Classify(contexthealth.Input{
		SessionID:      snap.SessionID,
		CLI:            connectors.CLIClaude,
		Model:          snap.Model,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Messages:       snap.Messages,
	})
	traj := contexthealth.ComputeCacheTrajectory(snap.Messages)
	sources := contexthealth.AttributeSources(snap.Messages)

	out := renderXrayScorecard(snap, res, traj, sources)

	// Structural checks — not byte-exact so the test doesn't break on
	// minor formatting tweaks, but verifies that every section is present.
	checks := []struct {
		label string
		want  string
	}{
		{"header prefix", "klyne audit — session"},
		{"composite line", "Composite:"},
		{"cache line", "Cache:"},
		{"pre-prompt section", "Pre-prompt context:"},
		{"session id short", "7c9e1234"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("scorecard missing %q\n--- output ---\n%s", c.label, out)
		}
	}
}

func TestRenderXrayScorecard_CompositeFields(t *testing.T) {
	t.Parallel()
	// Minimal inputs — verify the composite line format.
	snap := &mcpserver.SessionSnapshot{
		SessionID:      "deadbeef-1234-0000-0000-000000000001",
		Model:          "claude-sonnet-4-6",
		ContextFillPct: 71.0,
		MsgCount:       10,
		Messages:       nil,
	}
	res := contexthealth.Result{
		State:  contexthealth.StateRisky,
		Action: contexthealth.ActionCompact,
		Reason: "Context is 71% full.",
		Signals: contexthealth.Signals{
			ContextFillPct: 71.0,
			HiddenRatio:    4.2,
		},
	}
	traj := contexthealth.CacheTrajectoryResult{
		Direction: contexthealth.CacheDirectionChurning,
		Rates:     []float64{84.0, 65.0, 47.0},
		First:     84.0,
		Last:      47.0,
	}
	var sources []contexthealth.SourceRow

	out := renderXrayScorecard(snap, res, traj, sources)

	if !strings.Contains(out, "Risky") {
		t.Errorf("expected 'Risky' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "71%") {
		t.Errorf("expected '71%%' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "84%") {
		t.Errorf("expected '84%%' cache first in output; got:\n%s", out)
	}
	if !strings.Contains(out, "47%") {
		t.Errorf("expected '47%%' cache last in output; got:\n%s", out)
	}
	if !strings.Contains(out, "churning") {
		t.Errorf("expected 'churning' in output; got:\n%s", out)
	}
}

func TestRenderXrayScorecard_SourceAttribution(t *testing.T) {
	t.Parallel()
	snap := &mcpserver.SessionSnapshot{
		SessionID:      "aabbccdd",
		Model:          "claude-sonnet-4-6",
		ContextFillPct: 30.0,
		MsgCount:       4,
	}
	res := contexthealth.Result{
		State:  contexthealth.StateHealthy,
		Action: contexthealth.ActionContinue,
		Reason: "Session is healthy.",
		Signals: contexthealth.Signals{
			ContextFillPct: 30.0,
		},
	}
	traj := contexthealth.CacheTrajectoryResult{Direction: contexthealth.CacheDirectionFlat}
	sources := []contexthealth.SourceRow{
		{Name: "serena MCP", Kind: contexthealth.SourceKindMCP, Tokens: 18000},
		{Name: "klyne hooks", Kind: contexthealth.SourceKindHook, Tokens: 7000},
	}

	out := renderXrayScorecard(snap, res, traj, sources)

	if !strings.Contains(out, "serena MCP") {
		t.Errorf("expected 'serena MCP' in source section; got:\n%s", out)
	}
	if !strings.Contains(out, "klyne hooks") {
		t.Errorf("expected 'klyne hooks' in source section; got:\n%s", out)
	}
	if !strings.Contains(out, "18K") {
		t.Errorf("expected '18K' tokens in source section; got:\n%s", out)
	}
}

func TestXrayCmd_NoSessionGraceful(t *testing.T) {
	t.Parallel()
	// When no session is resolvable (empty result from resolver), the command
	// must print a friendly message and return nil (no error exit).
	cmd := newXrayCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// Override the run logic by pointing at an explicit nonexistent session id.
	// The FindSessionByID call returns "" gracefully, so runXray should print
	// the "no session" message.
	_ = cmd.Flags().Set("session-id", "nonexistent-session-id-that-does-not-exist")
	// We can't call cmd.Execute() easily in unit tests without cobra wiring,
	// so call runXray directly with a context.
	err := runXray(cmd, "nonexistent-session-id-that-does-not-exist")
	if err != nil {
		t.Errorf("expected nil err for missing session; got: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "No") && !strings.Contains(got, "no") {
		t.Errorf("expected 'no session' message; got: %q", got)
	}
}

func TestFormatKTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{18000, "18K"},
		{47000, "47K"},
		{2400, "2.4K"},
	}
	for _, c := range cases {
		got := formatKTokens(c.n)
		if got != c.want {
			t.Errorf("formatKTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestSessionAgeStr(t *testing.T) {
	t.Parallel()
	make2 := func(first, last int64) []*connectors.Message {
		return []*connectors.Message{
			{ID: "a", Ts: first},
			{ID: "b", Ts: last},
		}
	}
	cases := []struct {
		first, last int64
		want        string
	}{
		{0, 0, "0m"},
		{0, 30_000, "0m"},        // 30s → 0m
		{0, 60_000, "1m"},        // 1 min
		{0, 2_400_000, "40m"},    // 40 min
		{0, 3_600_000, "1h"},     // exactly 1h
		{0, 8_640_000, "2.4h"},   // 2.4h
	}
	for _, c := range cases {
		got := sessionAgeStr(make2(c.first, c.last))
		if got != c.want {
			t.Errorf("sessionAgeStr(first=%d, last=%d) = %q, want %q", c.first, c.last, got, c.want)
		}
	}
}

func TestShortSessionID(t *testing.T) {
	t.Parallel()
	if got := shortSessionID("7c9e1234-abcd-0000-0001-000000000001"); got != "7c9e1234" {
		t.Errorf("got %q, want 7c9e1234", got)
	}
	if got := shortSessionID("short"); got != "short" {
		t.Errorf("got %q, want short", got)
	}
}
