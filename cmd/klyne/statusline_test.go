package main

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

func mkOut(latest, ctxSize, totalEff, capEff int64) mcpserver.TokenTimelineOutput {
	out := mcpserver.TokenTimelineOutput{
		SessionID:      "abcd1234abcd",
		Model:          "claude-sonnet-4-5",
		LatestInput:    latest,
		ContextWindow:  ctxSize,
		TotalEffective: totalEff,
		CapEffective:   capEff,
		Points:         []contexthealth.TimelinePoint{{TsMs: 1}},
	}
	if ctxSize > 0 {
		out.PctOfContext = float64(latest) / float64(ctxSize) * 100
	}
	if capEff > 0 {
		out.PctUsed = float64(totalEff) / float64(capEff) * 100
	}
	return out
}

func TestRenderStatusline_Short(t *testing.T) {
	// 50% ctx, 25% burn
	out := mkOut(80_000, 160_000, 50_000, 200_000)
	got := renderStatusline(out, "short")
	if !strings.HasPrefix(got, "klyne ▸ 50% ctx · 80k/160k · 5h 25%") {
		t.Errorf("short = %q", got)
	}
}

func TestRenderStatusline_Mini(t *testing.T) {
	out := mkOut(80_000, 160_000, 50_000, 200_000)
	got := renderStatusline(out, "mini")
	if got != "50% · 25%" {
		t.Errorf("mini = %q; want '50%% · 25%%'", got)
	}
}

func TestRenderStatusline_PlainHasNoIcon(t *testing.T) {
	out := mkOut(80_000, 160_000, 50_000, 200_000)
	got := renderStatusline(out, "plain")
	if strings.Contains(got, "▸") {
		t.Errorf("plain should not contain the icon: %q", got)
	}
	if !strings.HasPrefix(got, "klyne ") {
		t.Errorf("plain should start with 'klyne ': %q", got)
	}
}

func TestIdleStatusline(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{"short", "klyne ▸ idle"},
		{"plain", "klyne idle"},
		{"mini", "—"},
		{"BOGUS", "klyne ▸ idle"},
	}
	for _, c := range cases {
		if got := idleStatusline(c.format); got != c.want {
			t.Errorf("idleStatusline(%q) = %q; want %q", c.format, got, c.want)
		}
	}
}

func TestHumanTokensShort(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1234, "1k"},
		{12_345, "12k"},
		{120_000, "120k"},
		{1_500_000, "1M"},
		{-1234, "1k"},
	}
	for _, c := range cases {
		if got := humanTokensShort(c.in); got != c.want {
			t.Errorf("humanTokensShort(%d) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestFmtPct(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1.4, "1"},
		{1.5, "2"},
		{99.7, "100"},
		{-5, "0"},
		{1000, "999"},
	}
	for _, c := range cases {
		if got := fmtPct(c.in); got != c.want {
			t.Errorf("fmtPct(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}
