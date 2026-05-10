package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// timelineLine renders one Claude Code assistant JSONL line at ts
// with the given input/cached-read counts. Mirrors the shape used
// by existing tests so the parser path is exercised end-to-end.
func timelineLine(sessionID string, ts time.Time, inputTokens, cacheRead int64) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"u-%d","sessionId":%q,"timestamp":%q,"cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"text","text":"reply"}],"id":"msg-%d","model":"claude-sonnet-4.5","usage":{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0}}}`,
		ts.UnixMilli(), sessionID, ts.UTC().Format(time.RFC3339Nano), ts.UnixMilli(), inputTokens, cacheRead,
	)
}

func TestHandleGetTokenTimeline_HappyPath(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/timeline-ok"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	now := time.Now()
	writeJSONL(t, dir, "tl.jsonl",
		timelineLine("sess-tl", now.Add(-30*time.Minute), 5_000, 1_000),
		timelineLine("sess-tl", now.Add(-20*time.Minute), 9_000, 1_000),
		timelineLine("sess-tl", now.Add(-10*time.Minute), 25_000, 1_000),
	)

	_, out, err := HandleGetTokenTimeline(context.Background(), nil, TokenTimelineInput{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleGetTokenTimeline: %v", err)
	}
	if out.Ambiguous {
		t.Fatalf("unexpected ambiguous result")
	}
	if out.SessionID != "sess-tl" {
		t.Fatalf("SessionID=%q, want sess-tl", out.SessionID)
	}
	if len(out.Points) != 3 {
		t.Fatalf("Points=%d, want 3", len(out.Points))
	}
	// Effective per turn: input (since cache_creation=0).
	wantPeak := int64(25_000)
	if out.PeakEffective != wantPeak {
		t.Fatalf("PeakEffective=%d, want %d", out.PeakEffective, wantPeak)
	}
	wantTotal := int64(5_000 + 9_000 + 25_000)
	if out.TotalEffective != wantTotal {
		t.Fatalf("TotalEffective=%d, want %d", out.TotalEffective, wantTotal)
	}
}

func TestFormatTokenTimelineAsMarkdown_RendersSparklineAndTable(t *testing.T) {
	now := time.Now().UnixMilli()
	out := TokenTimelineOutput{
		SessionID:     "sess-fmt",
		WindowStartMs: now - int64(5*time.Hour/time.Millisecond),
		WindowEndMs:   now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 30*60_000, EffectiveInput: 5_000, TotalInput: 6_000, CachedReadTokens: 1_000, OutputTokens: 200},
			{TsMs: now - 20*60_000, EffectiveInput: 9_000, TotalInput: 10_000, CachedReadTokens: 1_000, OutputTokens: 250},
			{TsMs: now - 10*60_000, EffectiveInput: 25_000, TotalInput: 26_000, CachedReadTokens: 1_000, OutputTokens: 300},
		},
		TotalEffective: 39_000,
		PeakEffective:  25_000,
		CapEffective:   100_000,
		PctUsed:        39,
		PlanTier:       "max-5x",
	}
	md := formatTokenTimelineAsMarkdown(out)
	// Sparkline block exists.
	if !strings.Contains(md, "```") {
		t.Fatalf("expected fenced block for sparkline\n%s", md)
	}
	// Table header present.
	if !strings.Contains(md, "uncached in") {
		t.Fatalf("expected 'uncached in' in table header\n%s", md)
	}
	// Summary line includes percentage.
	if !strings.Contains(md, "39%") {
		t.Fatalf("expected '39%%' in summary\n%s", md)
	}
	if !strings.Contains(md, "max-5x") {
		t.Fatalf("expected plan tier in summary\n%s", md)
	}
	// Peak token count rendered.
	if !strings.Contains(md, "25K") {
		t.Fatalf("expected '25K' for peak in sparkline footer\n%s", md)
	}
}

func TestFormatTokenTimelineAsMarkdown_NoPlanTierHidesPercentage(t *testing.T) {
	now := time.Now().UnixMilli()
	out := TokenTimelineOutput{
		SessionID:     "sess-noplan",
		WindowStartMs: now - 60_000,
		WindowEndMs:   now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 30_000, EffectiveInput: 5_000, TotalInput: 5_000},
		},
		TotalEffective: 5_000,
		PeakEffective:  5_000,
	}
	md := formatTokenTimelineAsMarkdown(out)
	if strings.Contains(md, "%") && !strings.Contains(md, "klyne config set plan") {
		t.Fatalf("unset plan should not surface a percentage; got\n%s", md)
	}
	if !strings.Contains(md, "klyne config set plan") {
		t.Fatalf("expected the plan-tier nudge in the summary\n%s", md)
	}
}

func TestRenderSparkline_AllZeroIsBlank(t *testing.T) {
	got := renderSparkline([]int64{0, 0, 0, 0})
	if got != "    " {
		t.Fatalf("got %q, want all-spaces", got)
	}
}

func TestRenderSparkline_RisingShape(t *testing.T) {
	got := renderSparkline([]int64{1, 2, 3, 8})
	// Must end with the highest block.
	runes := []rune(got)
	if runes[len(runes)-1] != '█' {
		t.Fatalf("expected last rune to be full block; got %q", got)
	}
}

func TestBucketValues_DownsamplesWhenLonger(t *testing.T) {
	in := []int64{1, 1, 1, 1, 9, 9, 9, 9}
	out := bucketValues(in, 2)
	if len(out) != 2 {
		t.Fatalf("len=%d, want 2", len(out))
	}
	if out[0] != 1 || out[1] != 9 {
		t.Fatalf("out=%v, want [1 9]", out)
	}
}

func TestBucketValues_UnchangedWhenShorter(t *testing.T) {
	in := []int64{1, 2, 3}
	out := bucketValues(in, 10)
	if len(out) != 3 || out[0] != 1 || out[2] != 3 {
		t.Fatalf("out=%v, want unchanged", out)
	}
}

func TestResolveWindowMs(t *testing.T) {
	const (
		oneMin  = int64(60_000)
		oneHour = int64(60 * 60_000)
		thirty  = int64(30 * 60_000)
		def5h   = int64(5 * oneHour)
		max24h  = int64(24 * oneHour)
	)
	cases := []struct {
		name   string
		win    string
		hours  int
		wantMs int64
	}{
		{"default", "", 0, def5h},
		{"hours field", "", 2, 2 * oneHour},
		{"duration string 30m", "30m", 0, thirty},
		{"duration string 1h30m", "1h30m", 0, oneHour + thirty},
		{"duration string preferred over hours", "30m", 99, thirty},
		{"clamp to 24h", "100h", 0, max24h},
		{"clamp to 1m", "1s", 0, oneMin},
		{"invalid string falls back to hours", "weird", 3, 3 * oneHour},
		{"invalid string and no hours falls back to default", "weird", 0, def5h},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWindowMs(tc.win, tc.hours); got != tc.wantMs {
				t.Fatalf("got %d, want %d", got, tc.wantMs)
			}
		})
	}
}

func TestRenderTimeAxis_AlignsToColumns(t *testing.T) {
	end := time.Now().UnixMilli()
	start := end - int64(30*time.Minute/time.Millisecond)
	got := renderTimeAxis(start, end, 40)
	if len(got) != 40 {
		t.Fatalf("len=%d, want 40 (got %q)", len(got), got)
	}
	if !strings.HasPrefix(got, "30m ago") {
		t.Fatalf("expected '30m ago' prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "now") {
		t.Fatalf("expected 'now' suffix, got %q", got)
	}
}

func TestDurationLabel(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{0, "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := durationLabel(tc.d); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleGetTokenTimeline_HonorsWindowDuration(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/timeline-window"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	now := time.Now()
	writeJSONL(t, dir, "tlw.jsonl",
		// Outside a 30-minute window — older than 30m.
		timelineLine("sess-tlw", now.Add(-90*time.Minute), 5_000, 0),
		// Inside a 30-minute window.
		timelineLine("sess-tlw", now.Add(-15*time.Minute), 9_000, 0),
		timelineLine("sess-tlw", now.Add(-5*time.Minute), 25_000, 0),
	)

	// Default 5h window: includes all three.
	_, full, err := HandleGetTokenTimeline(context.Background(), nil, TokenTimelineInput{CWD: cwd})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(full.Points) != 3 {
		t.Fatalf("default window points=%d, want 3", len(full.Points))
	}

	// 30-minute window: drops the oldest.
	_, scoped, err := HandleGetTokenTimeline(context.Background(), nil, TokenTimelineInput{CWD: cwd, Window: "30m"})
	if err != nil {
		t.Fatalf("30m: %v", err)
	}
	if len(scoped.Points) != 2 {
		t.Fatalf("30m window points=%d, want 2 (got %+v)", len(scoped.Points), scoped.Points)
	}
}
