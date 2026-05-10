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

func TestFormatTokenTimelineAsMarkdown_LeadsOnContextWindow(t *testing.T) {
	now := time.Now().UnixMilli()
	out := TokenTimelineOutput{
		SessionID:     "sess-fmt",
		Model:         "claude-sonnet-4-5",
		ContextWindow: 1_000_000,
		WindowStartMs: now - int64(5*time.Hour/time.Millisecond),
		WindowEndMs:   now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 30*60_000, EffectiveInput: 5_000, TotalInput: 36_000, CachedReadTokens: 30_000, OutputTokens: 200},
			{TsMs: now - 20*60_000, EffectiveInput: 9_000, TotalInput: 100_000, CachedReadTokens: 91_000, OutputTokens: 250},
			{TsMs: now - 10*60_000, EffectiveInput: 25_000, TotalInput: 500_000, CachedReadTokens: 475_000, OutputTokens: 300},
		},
		FirstInput:   36_000,
		PeakInput:    500_000,
		LatestInput:  500_000,
		PctOfContext: 50,
	}
	md := formatTokenTimelineAsMarkdown(out)

	// Headline names the model and context window.
	if !strings.Contains(md, "claude-sonnet-4-5") {
		t.Fatalf("missing model in header\n%s", md)
	}
	if !strings.Contains(md, "1M context") {
		t.Fatalf("missing context-window size in header\n%s", md)
	}
	// Trajectory sentence is present (start → peak → now).
	if !strings.Contains(md, "Started at 36K") {
		t.Fatalf("missing trajectory start\n%s", md)
	}
	if !strings.Contains(md, "now at 500K") {
		t.Fatalf("missing trajectory now\n%s", md)
	}
	// Per-turn table uses the new % of context column.
	if !strings.Contains(md, "% of context") {
		t.Fatalf("table missing 'percent of context' column\n%s", md)
	}
	// Bottom line names the % of context window AND anchors to a
	// timestamp ("as of HH:MM TZ (X ago)") so the reader knows
	// whether "now" means right now or hours ago.
	if !strings.Contains(md, "50% of the 1M context window") {
		t.Fatalf("bottom line missing '50%% of 1M context'\n%s", md)
	}
	if !strings.Contains(md, "As of ") {
		t.Fatalf("bottom line missing 'As of <time>' anchor\n%s", md)
	}
	// Uncached column header AND footnote must be present so the
	// user sees the rate-limit cost separately from prefix size.
	if !strings.Contains(md, "uncached") {
		t.Fatalf("table missing 'uncached' column\n%s", md)
	}
	if !strings.Contains(md, "10× discounted") {
		t.Fatalf("missing footnote explaining the uncached column\n%s", md)
	}
	// Plan tier must not appear in this surface — that's an
	// advisor concern, not a session-fill concern. The string
	// "5-hour" IS allowed inside the footnote that explains the
	// uncached column, but it must NOT appear in the headline /
	// trajectory / per-row data.
	if strings.Contains(md, "max-5x") || strings.Contains(md, "plan tier") {
		t.Fatalf("timeline output leaked plan-tier content:\n%s", md)
	}
	headlineEnd := strings.Index(md, "_Uncached column")
	if headlineEnd > 0 {
		headline := md[:headlineEnd]
		if strings.Contains(headline, "5-hour cap") || strings.Contains(headline, "plan") {
			t.Fatalf("timeline headline (above the footnote) leaked rate-limit content:\n%s", headline)
		}
	}
}

func TestFormatTokenTimelineAsMarkdown_NoContextWindowFallback(t *testing.T) {
	now := time.Now().UnixMilli()
	out := TokenTimelineOutput{
		SessionID:     "sess-nomodel",
		WindowStartMs: now - 60_000,
		WindowEndMs:   now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 30_000, EffectiveInput: 5_000, TotalInput: 5_000},
		},
		FirstInput:  5_000,
		PeakInput:   5_000,
		LatestInput: 5_000,
	}
	md := formatTokenTimelineAsMarkdown(out)
	// No context-window data → bottom line drops the percentage.
	if strings.Contains(md, "% of") {
		t.Fatalf("unknown model should not name a percentage; got\n%s", md)
	}
	// Trajectory still renders without percentages.
	if !strings.Contains(md, "Started at 5K") {
		t.Fatalf("trajectory missing\n%s", md)
	}
}

func TestEvenlySamplePoints_LongSessionSpansFullWindow(t *testing.T) {
	pts := make([]contexthealth.TimelinePoint, 165)
	for i := range pts {
		pts[i] = contexthealth.TimelinePoint{
			TsMs:       int64(i) * 60_000, // one minute apart
			TotalInput: int64(50_000 + i*1_000),
		}
	}
	samples := evenlySamplePoints(pts, 10)
	if len(samples) != 10 {
		t.Fatalf("len=%d, want 10", len(samples))
	}
	// First and last points must always be included so the reader
	// sees both ends of the trajectory.
	if samples[0].TotalInput != pts[0].TotalInput {
		t.Fatalf("first sample is not the first point")
	}
	if samples[len(samples)-1].TotalInput != pts[len(pts)-1].TotalInput {
		t.Fatalf("last sample is not the last point")
	}
	// Times must be strictly increasing — we should NEVER see two
	// rows with the same timestamp on a long session.
	for i := 1; i < len(samples); i++ {
		if samples[i].TsMs <= samples[i-1].TsMs {
			t.Fatalf("samples[%d].TsMs (%d) <= samples[%d].TsMs (%d)",
				i, samples[i].TsMs, i-1, samples[i-1].TsMs)
		}
	}
	// Input growth must be visible — last sample > first sample.
	if samples[len(samples)-1].TotalInput <= samples[0].TotalInput {
		t.Fatalf("growth invisible: last %d <= first %d",
			samples[len(samples)-1].TotalInput, samples[0].TotalInput)
	}
}

func TestEvenlySamplePoints_ShortSessionReturnsAll(t *testing.T) {
	pts := []contexthealth.TimelinePoint{
		{TsMs: 1, TotalInput: 100},
		{TsMs: 2, TotalInput: 200},
		{TsMs: 3, TotalInput: 300},
	}
	got := evenlySamplePoints(pts, 10)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
}

func TestFormatDelta(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{125_000, "+125K"},
		{-3_500, "-3.5K"},
	}
	for _, tc := range cases {
		if got := formatDelta(tc.in); got != tc.want {
			t.Fatalf("formatDelta(%d)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatTokenTimeline_LongSessionShowsGrowthInTable(t *testing.T) {
	// Synthesise a 50-turn session whose prefix grows from 50K to
	// 240K. The table must not collapse to all-same-time rows the
	// way the v1 (last-10-only) output did.
	pts := make([]contexthealth.TimelinePoint, 50)
	for i := range pts {
		pts[i] = contexthealth.TimelinePoint{
			TsMs:       int64(i) * 6 * 60_000, // 6-minute spacing
			TotalInput: int64(50_000 + i*4_000),
		}
	}
	out := TokenTimelineOutput{
		SessionID:     "sess-long",
		Model:         "claude-opus-4-7",
		ContextWindow: 1_000_000,
		WindowStartMs: 0,
		WindowEndMs:   pts[len(pts)-1].TsMs,
		Points:        pts,
		FirstInput:    pts[0].TotalInput,
		PeakInput:     pts[len(pts)-1].TotalInput,
		LatestInput:   pts[len(pts)-1].TotalInput,
		PctOfContext:  float64(pts[len(pts)-1].TotalInput) / 1_000_000 * 100,
	}
	md := formatTokenTimelineAsMarkdown(out)
	// Δ column must be present and the growth visible.
	if !strings.Contains(md, "Δ vs start") {
		t.Fatalf("table missing delta column\n%s", md)
	}
	// The first row's delta is 0; somewhere later we MUST see a
	// non-zero positive delta proving the table renders growth.
	if !strings.Contains(md, "+") {
		t.Fatalf("table shows no positive deltas — growth is invisible\n%s", md)
	}
}

func TestFormatTokenTimelineCompact_OneLine(t *testing.T) {
	now := time.Now().UnixMilli()
	out := TokenTimelineOutput{
		SessionID:     "abcd1234-0000-0000-0000-000000000000",
		Model:         "claude-opus-4-7",
		ContextWindow: 1_000_000,
		WindowStartMs: now - int64(5*time.Hour/time.Millisecond),
		WindowEndMs:   now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 60_000, TotalInput: 53_600, EffectiveInput: 33_900},
			{TsMs: now, TotalInput: 470_400, EffectiveInput: 359},
		},
		FirstInput:   53_600,
		LatestInput:  470_400,
		PeakInput:    470_400,
		PctOfContext: 47,
	}
	got := FormatTokenTimelineCompact(out)
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("compact output must be a single line; got\n%s", got)
	}
	mustHave := []string{"abcd1234", "470.4K", "1M", "47%", "53.6K", "as of"}
	for _, frag := range mustHave {
		if !strings.Contains(got, frag) {
			t.Fatalf("compact output missing %q\n%s", frag, got)
		}
	}
	// Compact form must NOT include the table or sparkline noise.
	for _, banned := range []string{"|", "▁", "▂", "uncached", "10×"} {
		if strings.Contains(got, banned) {
			t.Fatalf("compact output should not contain %q\n%s", banned, got)
		}
	}
}

func TestFormatTokenTimelineCompact_NoSession(t *testing.T) {
	got := FormatTokenTimelineCompact(TokenTimelineOutput{})
	if !strings.Contains(got, "no Claude Code session") {
		t.Fatalf("expected no-session message; got %q", got)
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("must stay single line; got\n%s", got)
	}
}

func TestFormatTokenTimelineCompact_Ambiguous(t *testing.T) {
	got := FormatTokenTimelineCompact(TokenTimelineOutput{
		Ambiguous:  true,
		Candidates: []CandidateRow{{}, {}, {}},
	})
	if !strings.Contains(got, "3 candidate sessions") {
		t.Fatalf("expected ambiguity message with count; got %q", got)
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("must stay single line; got\n%s", got)
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "0s"},
		{30_000, "30s"},
		{5 * 60_000, "5m"},
		{60 * 60_000, "1h"},
		{90 * 60_000, "1h30m"},
		{-5, "0s"},
	}
	for _, tc := range cases {
		if got := formatAge(tc.ms); got != tc.want {
			t.Fatalf("formatAge(%d)=%q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFormatPct(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0%"},
		{0.5, "<1%"},
		{12, "12%"},
		{99.4, "99%"},
	}
	for _, tc := range cases {
		if got := formatPct(tc.in); got != tc.want {
			t.Fatalf("formatPct(%v)=%q, want %q", tc.in, got, tc.want)
		}
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

// codexAssistantLine renders a single Codex `response_item` assistant
// message at ts. Mirrors the wire shape of real Codex JSONL.
func codexAssistantLine(ts time.Time, text string) string {
	return fmt.Sprintf(
		`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`,
		ts.UTC().Format(time.RFC3339Nano), text,
	)
}

// codexTokenCountLine renders a single Codex `event_msg/token_count`
// line carrying both `last_token_usage` (per-call) and `total_token_usage`
// (cumulative). totalIn/totalCached/totalOut are session-cumulative; the
// per-call delta is computed by the parser.
func codexTokenCountLine(ts time.Time, lastIn, lastCached, lastOut, totalIn, totalCached, totalOut int64) string {
	return fmt.Sprintf(
		`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d},"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d},"model_context_window":272000}}}`,
		ts.UTC().Format(time.RFC3339Nano),
		lastIn, lastCached, lastOut, lastIn+lastOut,
		totalIn, totalCached, totalOut, totalIn+totalOut,
	)
}

// TestHandleGetTokenTimeline_CodexSession asserts that a Codex JSONL
// containing assistant `response_item.message` lines paired with
// `event_msg/token_count` events produces a non-empty timeline whose
// Points carry the per-turn input/cached/output numbers projected from
// each token_count onto the preceding assistant message.
//
// This is the regression test for the 2026-05-10 CLI review finding
// that `klyne tokens --session=<codex-id>` returned "no assistant turns"
// because Codex's per-turn token data lives in standalone token_count
// events, not on the assistant message itself.
func TestHandleGetTokenTimeline_CodexSession(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/codex-tokens"
	sessionID := "codex-tt-001"

	// Build a synthetic rollout file under ~/.codex/sessions/YYYY/MM/DD.
	now := time.Now()
	yyyy := now.Format("2006")
	mm := now.Format("01")
	dd := now.Format("02")
	dir := filepath.Join(home, ".codex", "sessions", yyyy, mm, dd)

	// Three assistant turns, each followed by a token_count whose
	// last_token_usage reports the per-call usage.
	t1 := now.Add(-30 * time.Minute)
	t2 := now.Add(-20 * time.Minute)
	t3 := now.Add(-10 * time.Minute)

	lines := []string{
		// session_meta carries the id + cwd; required so the parser emits.
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":%q,"model_provider":"openai"}}`,
			t1.Add(-1*time.Second).UTC().Format(time.RFC3339Nano), sessionID, cwd),
		// Initial heartbeat with info=null (real Codex shape; must be tolerated).
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":null}}`,
			t1.Add(-500*time.Millisecond).UTC().Format(time.RFC3339Nano)),
		// turn_context names the model.
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5","cwd":%q}}`,
			t1.Add(-100*time.Millisecond).UTC().Format(time.RFC3339Nano), cwd),

		// Turn 1: assistant message → token_count with per-call usage.
		codexAssistantLine(t1, "First turn answer."),
		// last (per-call) = total (cumulative): 21990 in / 3456 cached / 199 out.
		codexTokenCountLine(t1.Add(500*time.Millisecond),
			21990, 3456, 199,
			21990, 3456, 199),

		// Turn 2: assistant message → token_count.
		codexAssistantLine(t2, "Second turn answer."),
		// last per-call: 22520 in / 21376 cached / 315 out
		// total cumulative: 44510 / 24832 / 514
		codexTokenCountLine(t2.Add(500*time.Millisecond),
			22520, 21376, 315,
			44510, 24832, 514),

		// Turn 3: assistant message → token_count.
		codexAssistantLine(t3, "Third turn answer."),
		// last per-call: 25169 in / 21888 cached / 825 out
		// total cumulative: 69679 / 46720 / 1339
		codexTokenCountLine(t3.Add(500*time.Millisecond),
			25169, 21888, 825,
			69679, 46720, 1339),
	}

	rolloutName := "rollout-" + sessionID + ".jsonl"
	writeJSONL(t, dir, rolloutName, lines...)

	// Resolve via explicit session_id since the cwd-based resolver only
	// indexes Claude project paths.
	_, out, err := HandleGetTokenTimeline(context.Background(), nil,
		TokenTimelineInput{SessionID: sessionID})
	if err != nil {
		t.Fatalf("HandleGetTokenTimeline: %v", err)
	}
	if out.Ambiguous {
		t.Fatalf("unexpected ambiguous result: %+v", out)
	}
	if out.SessionID != sessionID {
		t.Fatalf("SessionID = %q, want %q", out.SessionID, sessionID)
	}
	// The headline regression: Points must be non-empty for Codex sessions.
	if len(out.Points) == 0 {
		t.Fatalf("expected non-empty Points for Codex session; got 0 (the original bug)")
	}
	// All three assistant turns must appear (chronological order).
	if len(out.Points) != 3 {
		t.Fatalf("Points = %d, want 3 (one per assistant turn); got %+v", len(out.Points), out.Points)
	}
	// Per-turn TokensIn must match each turn's last_token_usage.input_tokens.
	wantInPerTurn := []int64{21990, 22520, 25169}
	wantCachedPerTurn := []int64{3456, 21376, 21888}
	for i, p := range out.Points {
		if p.TotalInput != wantInPerTurn[i] {
			t.Errorf("Points[%d].TotalInput = %d; want %d", i, p.TotalInput, wantInPerTurn[i])
		}
		if p.CachedReadTokens != wantCachedPerTurn[i] {
			t.Errorf("Points[%d].CachedReadTokens = %d; want %d", i, p.CachedReadTokens, wantCachedPerTurn[i])
		}
		// EffectiveInput is TotalInput - CachedReadTokens — the rate-limit
		// burn axis (uncached portion); must be the math the renderer
		// shows in the "uncached" column.
		wantEff := wantInPerTurn[i] - wantCachedPerTurn[i]
		if p.EffectiveInput != wantEff {
			t.Errorf("Points[%d].EffectiveInput = %d; want %d", i, p.EffectiveInput, wantEff)
		}
	}
	// Headline rollups should be the latest turn's prefix.
	if out.LatestInput != 25169 {
		t.Errorf("LatestInput = %d; want 25169", out.LatestInput)
	}
	if out.PeakInput != 25169 {
		t.Errorf("PeakInput = %d; want 25169 (largest single-turn prefix)", out.PeakInput)
	}
}

// TestHandleGetTokenTimeline_CodexSilencesUnknownTypes asserts that
// Codex transcripts containing the newer `custom_tool_call` /
// `custom_tool_call_output` response_item types and array-shaped
// function_call_output payloads parse without spamming stderr. The
// regression: every walk of these transcripts (klyne tokens, klyne
// audit-sessions, klyne advise) was logging once per occurrence per
// file.
//
// We don't capture stderr directly here — the gate is that the parser
// must produce a non-empty timeline (i.e. didn't blow up parsing the
// custom shapes) and the warnOnce gate prevents floods. The
// unknown-type-decode behaviour is exercised at the parser-test level
// in the codex package; this test is a smoke check that integration
// works end-to-end.
func TestHandleGetTokenTimeline_CodexToleratesCustomToolShapes(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/codex-tools"
	sessionID := "codex-custom-001"

	now := time.Now()
	yyyy := now.Format("2006")
	mm := now.Format("01")
	dd := now.Format("02")
	dir := filepath.Join(home, ".codex", "sessions", yyyy, mm, dd)

	t1 := now.Add(-15 * time.Minute)

	lines := []string{
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":%q,"model_provider":"openai"}}`,
			t1.Add(-1*time.Second).UTC().Format(time.RFC3339Nano), sessionID, cwd),
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5","cwd":%q}}`,
			t1.Add(-500*time.Millisecond).UTC().Format(time.RFC3339Nano), cwd),
		// Newer Codex shapes — must not error or noisily log.
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_abc","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}}`,
			t1.Add(-300*time.Millisecond).UTC().Format(time.RFC3339Nano)),
		// custom_tool_call_output with a JSON-string output (legacy shape).
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_abc","output":"{\"output\":\"Success\"}"}}`,
			t1.Add(-200*time.Millisecond).UTC().Format(time.RFC3339Nano)),
		// function_call_output with an ARRAY output (newer Codex flavour
		// returning an image attachment) — tolerate the dual shape.
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_def","output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}}`,
			t1.Add(-100*time.Millisecond).UTC().Format(time.RFC3339Nano)),
		// One assistant message + token_count so the timeline path has
		// at least one point to show.
		codexAssistantLine(t1, "Done."),
		codexTokenCountLine(t1.Add(500*time.Millisecond),
			1000, 200, 50,
			1000, 200, 50),
	}
	rolloutName := "rollout-" + sessionID + ".jsonl"
	writeJSONL(t, dir, rolloutName, lines...)

	_, out, err := HandleGetTokenTimeline(context.Background(), nil,
		TokenTimelineInput{SessionID: sessionID})
	if err != nil {
		t.Fatalf("HandleGetTokenTimeline: %v", err)
	}
	if len(out.Points) == 0 {
		t.Fatalf("expected at least one timeline point even with custom_tool_call shapes; got 0")
	}
}
