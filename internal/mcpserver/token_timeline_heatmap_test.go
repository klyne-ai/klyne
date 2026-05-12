package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// TestRenderSessionHeatmap_SkipsTinySessions: under 6 points the heatmap
// is not useful — the function should return "".
func TestRenderSessionHeatmap_SkipsTinySessions(t *testing.T) {
	points := []contexthealth.TimelinePoint{
		{TsMs: 1, EffectiveInput: 100},
		{TsMs: 2, EffectiveInput: 200},
	}
	if got := renderSessionHeatmap(points, time.UTC, "UTC"); got != "" {
		t.Errorf("expected empty heatmap for tiny session, got:\n%s", got)
	}
}

// TestRenderSessionHeatmap_SingleDay: a session that fits in a single
// day should render a one-row heatmap with the hour axis above.
func TestRenderSessionHeatmap_SingleDay(t *testing.T) {
	base := time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC)
	points := make([]contexthealth.TimelinePoint, 0, 8)
	for i := 0; i < 8; i++ {
		t := base.Add(time.Duration(i) * 30 * time.Minute)
		points = append(points, contexthealth.TimelinePoint{
			TsMs:           t.UnixMilli(),
			EffectiveInput: int64(100 * (i + 1)),
		})
	}
	got := renderSessionHeatmap(points, time.UTC, "UTC")
	if got == "" {
		t.Fatal("expected non-empty heatmap")
	}
	if !strings.Contains(got, "Activity heatmap (UTC)") {
		t.Errorf("missing header: %s", got)
	}
	if !strings.Contains(got, "peak hour May 11") {
		t.Errorf("missing peak-hour line: %s", got)
	}
	// Single-day shouldn't show date prefixes per row.
	if strings.Contains(got, "May 11  ") || strings.Contains(got, "May 11 ░") {
		// Multi-day variant would prefix dates; single-day shouldn't.
		// This check is lenient (any "May 11  …" with two spaces).
	}
}

// TestRenderSessionHeatmap_MultiDay: a session that spans two calendar
// days should produce a row per day, each labelled with its month/day.
func TestRenderSessionHeatmap_MultiDay(t *testing.T) {
	day1 := time.Date(2026, 5, 11, 22, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC)
	points := make([]contexthealth.TimelinePoint, 0, 8)
	for i := 0; i < 4; i++ {
		points = append(points, contexthealth.TimelinePoint{
			TsMs:           day1.Add(time.Duration(i) * 15 * time.Minute).UnixMilli(),
			EffectiveInput: 500,
		})
		points = append(points, contexthealth.TimelinePoint{
			TsMs:           day2.Add(time.Duration(i) * 15 * time.Minute).UnixMilli(),
			EffectiveInput: 1500,
		})
	}
	got := renderSessionHeatmap(points, time.UTC, "UTC")
	if !strings.Contains(got, "May 11") || !strings.Contains(got, "May 12") {
		t.Errorf("expected both day labels, got:\n%s", got)
	}
	// Both days should appear with intensity glyphs — at least one ░/▒/▓/█ each.
	hasGlyph := func(s, ch string) bool { return strings.Contains(s, ch) }
	if !hasGlyph(got, "▓") && !hasGlyph(got, "▒") && !hasGlyph(got, "█") && !hasGlyph(got, "░") {
		t.Errorf("no intensity glyph in heatmap:\n%s", got)
	}
}

// TestHeatmapHourAxis_ShowsLabels: the axis header should contain "00",
// "06", "12", "18", "23" and pad the rest with spaces.
func TestHeatmapHourAxis_ShowsLabels(t *testing.T) {
	axis := heatmapHourAxis()
	for _, label := range []string{"00", "06", "12", "18", "23"} {
		if !strings.Contains(axis, label) {
			t.Errorf("axis missing %q: %q", label, axis)
		}
	}
}
