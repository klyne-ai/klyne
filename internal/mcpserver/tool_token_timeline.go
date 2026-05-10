package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// tool_token_timeline.go — get_token_timeline MCP tool +
// renderer for the /klyne:tokens slash prompt.
//
// Returns the per-turn token usage for the active session over the
// last 5 hours, ready to consume in two surfaces:
//
//   - Slash prompt — the AI / user sees an inline ASCII sparkline,
//     a small Markdown table of the most recent turns, and a one-
//     line summary that includes the configured plan's percentage
//     used.
//   - Web cockpit — the structured Points slice powers a real
//     line chart on the session detail page.
//
// No AI calls in either path. Pure JSONL aggregation.

// TokenTimelineInput is the JSON-Schema input for the tool.
type TokenTimelineInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
	// Window is a Go duration string for the lookback ("30m", "1h",
	// "2h30m", "5h"). Takes precedence over WindowHours when set.
	// Min 1m, max 24h; values outside the range are clamped.
	Window string `json:"window,omitempty" jsonschema:"lookback as a Go duration string (e.g. \"30m\", \"5h\", \"2h30m\"); default 5h, min 1m, max 24h"`
	// WindowHours is the integer-hours convenience field kept so
	// callers that prefer "give me the last N hours" don't need to
	// build a duration string. Ignored when Window is set.
	WindowHours int `json:"window_hours,omitempty" jsonschema:"convenience integer hours (default 5; max 24); ignored when window is set"`
}

// TokenTimelineOutput mirrors contexthealth.TokenTimeline plus the
// surface metadata an MCP caller needs.
type TokenTimelineOutput struct {
	SessionID      string                       `json:"session_id,omitempty" jsonschema:"resolved session id"`
	Path           string                       `json:"path,omitempty" jsonschema:"absolute path of the analysed transcript"`
	WindowStartMs  int64                        `json:"window_start_ms" jsonschema:"left edge of the displayed window (epoch ms)"`
	WindowEndMs    int64                        `json:"window_end_ms" jsonschema:"right edge (epoch ms; typically now)"`
	Points         []contexthealth.TimelinePoint `json:"points" jsonschema:"per-assistant-turn token rows in chronological order"`
	TotalEffective int64                        `json:"total_effective" jsonschema:"sum of effective input across the window"`
	PeakEffective  int64                        `json:"peak_effective" jsonschema:"max effective input on a single turn"`
	CapEffective   int64                        `json:"cap_effective,omitempty" jsonschema:"5-hour cap from the user's plan tier; 0 when unset"`
	PctUsed        float64                      `json:"pct_used,omitempty" jsonschema:"TotalEffective/CapEffective × 100, capped at 100; 0 when CapEffective is 0"`
	PlanTier       string                       `json:"plan_tier,omitempty" jsonschema:"the user's configured plan tier; empty when unset"`
	Ambiguous      bool                         `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates     []CandidateRow               `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// HandleGetTokenTimeline is the MCP entry point. Resolves the
// session via the same disambiguation logic as the other tools,
// loads the snapshot, and computes the timeline against the
// configured plan cap.
func HandleGetTokenTimeline(ctx context.Context, _ *mcp.CallToolRequest, in TokenTimelineInput) (*mcp.CallToolResult, TokenTimelineOutput, error) {
	path, ambiguous, cands, err := resolveSession(GetContextHealthInput{
		SessionID: in.SessionID,
		CWD:       in.CWD,
	})
	if err != nil {
		return nil, TokenTimelineOutput{}, err
	}
	if ambiguous {
		rows := make([]CandidateRow, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, CandidateRow{
				SessionID: c.SessionID,
				Preview:   c.Preview,
				IsActive:  c.IsActive,
				ModTime:   c.ModTime.UTC().Format(timeRFC3339),
				MsgCount:  c.MsgCount,
			})
		}
		const reason = "Multiple Claude Code sessions in this project. Pick one and call get_token_timeline again with session_id."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, TokenTimelineOutput{Ambiguous: true, Candidates: rows}, nil
	}
	if path == "" {
		const reason = "No Claude Code session found for this working directory."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, TokenTimelineOutput{}, nil
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		return nil, TokenTimelineOutput{}, fmt.Errorf("load snapshot: %w", err)
	}

	cfg, _ := config.Load()
	cap := int64(0)
	tier := ""
	if cfg != nil {
		cap = cfg.Plan.FiveHourCap()
		tier = string(cfg.Plan.Tier)
	}

	windowMs := resolveWindowMs(in.Window, in.WindowHours)
	now := time.Now().UnixMilli()

	tl := contexthealth.ComputeTimeline(snap.Messages, now, windowMs, cap)
	if tl.SessionID == "" {
		tl.SessionID = snap.SessionID
	}

	out := TokenTimelineOutput{
		SessionID:      tl.SessionID,
		Path:           snap.Path,
		WindowStartMs:  tl.WindowStartMs,
		WindowEndMs:    tl.WindowEndMs,
		Points:         tl.Points,
		TotalEffective: tl.TotalEffective,
		PeakEffective:  tl.PeakEffective,
		CapEffective:   tl.CapEffective,
		PctUsed:        tl.PctUsed(),
		PlanTier:       tier,
	}
	// The Content text is the AI-facing summary; keep it terse.
	summary := fmt.Sprintf(
		"Token timeline for `%s`: %d turns, ~%s effective, peak ~%s.",
		short(out.SessionID), len(out.Points),
		humanTokens(out.TotalEffective), humanTokens(out.PeakEffective),
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// formatTokenTimelineAsMarkdown renders the timeline for the slash
// prompt: ASCII sparkline + per-turn table + summary line. When
// the user's plan tier is configured, the summary also reports the
// percentage of the 5-hour cap consumed.
func formatTokenTimelineAsMarkdown(out TokenTimelineOutput) string {
	if out.Ambiguous {
		return formatAmbiguousAsMarkdown("get_token_timeline", out.Candidates)
	}
	if len(out.Points) == 0 {
		if out.SessionID == "" {
			return "No Claude Code session found for this working directory."
		}
		return fmt.Sprintf("Session `%s` has no assistant turns within the last %s.",
			short(out.SessionID), formatWindow(out.WindowStartMs, out.WindowEndMs))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Token timeline — `%s`\n\n", short(out.SessionID))
	fmt.Fprintf(&b, "Window: last %s · %d assistant turns.\n\n",
		formatWindow(out.WindowStartMs, out.WindowEndMs), len(out.Points))

	// ASCII sparkline. Render the entire window down-sampled to
	// `sparklineCols` columns so the chat width stays predictable.
	// Adds a small time axis underneath ("Xh ago … now") so the
	// reader can place the curve in time without scanning the
	// table.
	values := effectiveSeries(out.Points)
	b.WriteString("```\n")
	b.WriteString(renderSparkline(values))
	b.WriteString("\n")
	b.WriteString(renderTimeAxis(out.WindowStartMs, out.WindowEndMs, sparklineCols))
	b.WriteString("\n")
	fmt.Fprintf(&b, "total ~%s · peak ~%s · min ~%s\n",
		humanTokens(out.TotalEffective), humanTokens(out.PeakEffective), humanTokens(minInt64(values)))
	b.WriteString("```\n\n")

	// Recent-turns table (last 10 or fewer).
	limit := 10
	if len(out.Points) < limit {
		limit = len(out.Points)
	}
	tail := out.Points[len(out.Points)-limit:]
	b.WriteString("| time | uncached in | cached read | cached write | output |\n")
	b.WriteString("|------|------------:|------------:|-------------:|-------:|\n")
	for _, p := range tail {
		when := time.UnixMilli(p.TsMs).UTC().Format("15:04:05")
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			when,
			humanTokens(p.EffectiveInput),
			humanTokens(p.CachedReadTokens),
			humanTokens(p.CachedWriteTokens),
			humanTokens(p.OutputTokens),
		)
	}
	b.WriteString("\n")

	// Summary line. Honest framing when the plan is unset.
	if out.CapEffective > 0 {
		fmt.Fprintf(&b,
			"**Total uncached input this window: ~%s** (~%.0f%% of your %s plan, estimated).\n",
			humanTokens(out.TotalEffective), out.PctUsed, out.PlanTier,
		)
	} else {
		fmt.Fprintf(&b,
			"**Total uncached input this window: ~%s.** Run `klyne config set plan <tier>` to see the percentage of your 5-hour cap.\n",
			humanTokens(out.TotalEffective),
		)
	}
	return b.String()
}

// effectiveSeries pulls the EffectiveInput slice out of the points.
func effectiveSeries(pts []contexthealth.TimelinePoint) []int64 {
	out := make([]int64, len(pts))
	for i, p := range pts {
		out[i] = p.EffectiveInput
	}
	return out
}

// sparklineCols is the maximum width of the rendered sparkline.
// 40 columns fits comfortably inside a typical chat width without
// wrapping on most terminals.
const sparklineCols = 40

// sparkBlocks is the eight Unicode block-element characters used
// to render a sparkline. The empty/space slot is intentionally
// included as the lowest level so a turn whose effective input is
// zero is visually distinguishable from "no data."
var sparkBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// renderSparkline returns a single-line block-character chart of
// values. When len(values) > sparklineCols, the input is bucketed
// down to sparklineCols by averaging adjacent buckets.
func renderSparkline(values []int64) string {
	if len(values) == 0 {
		return ""
	}
	buckets := bucketValues(values, sparklineCols)
	maxV := int64(0)
	for _, v := range buckets {
		if v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		return strings.Repeat(string(sparkBlocks[0]), len(buckets))
	}
	var b strings.Builder
	step := len(sparkBlocks) - 1
	for _, v := range buckets {
		idx := int(float64(v) / float64(maxV) * float64(step))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkBlocks) {
			idx = len(sparkBlocks) - 1
		}
		b.WriteRune(sparkBlocks[idx])
	}
	return b.String()
}

// bucketValues averages adjacent values down to at most cols
// buckets. When len(values) ≤ cols, returns values unchanged.
func bucketValues(values []int64, cols int) []int64 {
	if cols <= 0 || len(values) <= cols {
		return values
	}
	out := make([]int64, cols)
	bucketSize := float64(len(values)) / float64(cols)
	for i := 0; i < cols; i++ {
		lo := int(float64(i) * bucketSize)
		hi := int(float64(i+1) * bucketSize)
		if hi > len(values) {
			hi = len(values)
		}
		if hi <= lo {
			continue
		}
		var sum int64
		for j := lo; j < hi; j++ {
			sum += values[j]
		}
		out[i] = sum / int64(hi-lo)
	}
	return out
}

// renderTimeAxis prints "Xh ago" / "Xm ago" left-aligned under
// the sparkline's first column and "now" right-aligned under its
// last column. Width matches sparklineCols so the labels line up
// visually with the chart above.
func renderTimeAxis(startMs, endMs int64, cols int) string {
	d := time.Duration(endMs-startMs) * time.Millisecond
	left := durationLabel(d) + " ago"
	right := "now"
	if cols < len(left)+len(right)+1 {
		// Sparkline is too narrow for both labels — drop the right
		// edge rather than truncating the left.
		return left
	}
	pad := cols - len(left) - len(right)
	return left + strings.Repeat(" ", pad) + right
}

// durationLabel renders a time.Duration as "30m" / "2h" / "5h" —
// minute resolution under an hour, hour resolution otherwise.
// Mirrors what users typed in if they passed a window argument.
func durationLabel(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%dm", h, m)
}

// minInt64 returns the smallest value in xs, or 0 for empty.
func minInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// humanTokens renders a token count like 220_000_000 as "220M" so
// the inline output stays compact. Mirrors cmd/klyne/config.go's
// helper; duplicated to keep the package free of cmd dependencies.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmtTrim(float64(n)/1e9) + "B"
	case n >= 1_000_000:
		return fmtTrim(float64(n)/1e6) + "M"
	case n >= 1_000:
		return fmtTrim(float64(n)/1e3) + "K"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// fmtTrim prints f with one decimal, dropping trailing zeros so
// "5.0" becomes "5" but "5.5" stays "5.5".
func fmtTrim(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// formatWindow renders the lookback span as "5h0m" / "20m30s" so
// the slash output is honest about how big the window is.
func formatWindow(startMs, endMs int64) string {
	d := time.Duration(endMs-startMs) * time.Millisecond
	if d <= 0 {
		return "0s"
	}
	// Sub-hour windows are most useful at minute resolution; longer
	// ones clamp to minutes too so "5h0m" reads cleanly.
	return d.Truncate(time.Minute).String()
}

// resolveWindowMs decides the lookback duration in milliseconds.
// Resolution order: explicit Go duration string → integer hours
// convenience field → 5h default. Values outside [1m, 24h] are
// clamped — sub-minute windows produce empty timelines on real
// sessions and silently surprise the user; >24h windows would
// pull in too many sessions for the slash output to stay readable.
func resolveWindowMs(window string, hours int) int64 {
	const (
		minWindow = time.Minute
		maxWindow = 24 * time.Hour
		defWindow = 5 * time.Hour
	)
	clamp := func(d time.Duration) time.Duration {
		if d < minWindow {
			return minWindow
		}
		if d > maxWindow {
			return maxWindow
		}
		return d
	}
	if window != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(window)); err == nil && d > 0 {
			return int64(clamp(d) / time.Millisecond)
		}
	}
	if hours > 0 {
		return int64(clamp(time.Duration(hours)*time.Hour) / time.Millisecond)
	}
	return int64(defWindow / time.Millisecond)
}
