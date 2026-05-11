package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/usage"
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
// surface metadata an MCP caller needs. The headline fields (LatestInput,
// PeakInput, FirstInput, ContextWindow, PctOfContext) describe the
// single-session prefix-size axis. The legacy fields (TotalEffective,
// CapEffective, PctUsed, PlanTier) describe the 5-hour rate-limit
// axis and are kept for the advisor's separate use.
type TokenTimelineOutput struct {
	SessionID     string                        `json:"session_id,omitempty" jsonschema:"resolved session id"`
	Path          string                        `json:"path,omitempty" jsonschema:"absolute path of the analysed transcript"`
	Model         string                        `json:"model,omitempty" jsonschema:"model id on the most recent qualifying turn"`
	ContextWindow int64                         `json:"context_window,omitempty" jsonschema:"model's context window in tokens; 0 when unknown"`
	WindowStartMs int64                         `json:"window_start_ms" jsonschema:"left edge of the displayed window (epoch ms)"`
	WindowEndMs   int64                         `json:"window_end_ms" jsonschema:"right edge (epoch ms; typically now)"`
	Points        []contexthealth.TimelinePoint `json:"points" jsonschema:"per-assistant-turn token rows in chronological order"`
	FirstInput    int64                         `json:"first_input,omitempty" jsonschema:"oldest qualifying turn's TokensIn — where this session started"`
	LatestInput   int64                         `json:"latest_input,omitempty" jsonschema:"most recent qualifying turn's TokensIn — current prefix size"`
	PeakInput     int64                         `json:"peak_input,omitempty" jsonschema:"largest single-turn TokensIn in the window"`
	PctOfContext  float64                       `json:"pct_of_context,omitempty" jsonschema:"LatestInput/ContextWindow × 100, capped at 100"`
	// Legacy / advisor fields — describe rate-limit consumption,
	// not single-session context fill. Surfaced here for callers
	// that want both axes; the /klyne:tokens renderer ignores them.
	TotalEffective int64          `json:"total_effective" jsonschema:"sum of effective (uncached) input across the window"`
	PeakEffective  int64          `json:"peak_effective" jsonschema:"max effective input on a single turn"`
	CapEffective   int64          `json:"cap_effective,omitempty" jsonschema:"5-hour cap from the user's plan tier; 0 when unset"`
	PctUsed        float64        `json:"pct_used,omitempty" jsonschema:"TotalEffective/CapEffective × 100, capped at 100; 0 when CapEffective is 0"`
	PlanTier       string         `json:"plan_tier,omitempty" jsonschema:"the user's configured plan tier; empty when unset"`
	Ambiguous      bool           `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates     []CandidateRow `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
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
	if tl.Model == "" {
		tl.Model = snap.Model
	}
	tl.ContextWindow = usage.ContextWindowForModel(tl.Model)

	out := TokenTimelineOutput{
		SessionID:      tl.SessionID,
		Path:           snap.Path,
		Model:          tl.Model,
		ContextWindow:  tl.ContextWindow,
		WindowStartMs:  tl.WindowStartMs,
		WindowEndMs:    tl.WindowEndMs,
		Points:         tl.Points,
		FirstInput:     tl.FirstInput,
		LatestInput:    tl.LatestInput,
		PeakInput:      tl.PeakInput,
		PctOfContext:   tl.PctOfContext(),
		TotalEffective: tl.TotalEffective,
		PeakEffective:  tl.PeakEffective,
		CapEffective:   tl.CapEffective,
		PctUsed:        tl.PctUsed(),
		PlanTier:       tier,
	}
	// The Content text is the AI-facing summary; keep it terse and
	// led by the single-session axis the user actually asked for.
	summary := fmt.Sprintf(
		"Session `%s`: prefix at ~%s (%s of %s context).",
		short(out.SessionID),
		humanTokens(out.LatestInput),
		formatPct(out.PctOfContext),
		humanTokens(out.ContextWindow),
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// FormatTokenTimelineAsMarkdown is the exported alias used by the
// `klyne tokens` CLI subcommand for the full table-rendered view.
// Returns the verbose Markdown form: headline, sparkline, per-turn
// table, footnote.
func FormatTokenTimelineAsMarkdown(out TokenTimelineOutput) string {
	return formatTokenTimelineAsMarkdown(out)
}

// FormatTokenTimelineCompact returns a single-line summary suitable
// for the slash-prompt surface. Pasting a multi-row table into the
// chat would burn input tokens for content the user can read more
// cleanly via the CLI; one line is enough for the AI to respond
// to and gives the user a glance-able status.
//
// Format: "klyne tokens [abcd1234]: 470K / 1M (47%) · grew 53K → 470K
// over 504 turns · as of 13:23 IST"
//
// Falls back to a similarly-shaped line when ContextWindow is
// unknown, ambiguous results, or the session is empty.
func FormatTokenTimelineCompact(out TokenTimelineOutput) string {
	if out.Ambiguous {
		return fmt.Sprintf("klyne tokens: %d candidate sessions in this cwd. Re-run with --session=<id>.",
			len(out.Candidates))
	}
	if len(out.Points) == 0 {
		if out.SessionID == "" {
			return "klyne tokens: no Claude Code session found for this working directory."
		}
		return fmt.Sprintf("klyne tokens [%s]: no assistant turns within the last %s.",
			short(out.SessionID), formatWindow(out.WindowStartMs, out.WindowEndMs))
	}

	loc := time.Local
	tzName, _ := time.Now().In(loc).Zone()
	if tzName == "" {
		tzName = "local"
	}
	latestPt := out.Points[len(out.Points)-1]
	latestWhen := time.UnixMilli(latestPt.TsMs).In(loc).Format("15:04")

	var prefix string
	if out.ContextWindow > 0 {
		prefix = fmt.Sprintf("%s / %s (%s)",
			humanTokens(out.LatestInput),
			humanTokens(out.ContextWindow),
			formatPct(out.PctOfContext))
	} else {
		prefix = humanTokens(out.LatestInput)
	}

	growth := fmt.Sprintf("grew %s → %s over %d turns",
		humanTokens(out.FirstInput), humanTokens(out.LatestInput), len(out.Points))

	return fmt.Sprintf("klyne tokens [%s]: %s · %s · as of %s %s.",
		short(out.SessionID), prefix, growth, latestWhen, tzName)
}

// formatTokenTimelineAsMarkdown renders the timeline for the slash
// prompt and the CLI subcommand. Leads with the single-session
// axis the user cares about: "how full is my prefix right now,
// and how did it grow over time?"
//
// Deliberately does NOT mention the 5-hour rate-limit cap or the
// plan tier — those are advisor concerns, surfaced separately by
// `klyne advise`. Mixing the two axes (per-session prefix size vs
// cross-session rate-limit consumption) confused the v1 output.
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

	// Resolve a single timezone for every time string in this
	// render. Defaults to the process's local zone (the user's
	// system timezone) so a user in Asia/Kolkata sees IST instead
	// of having to mentally translate UTC.
	loc := time.Local
	tzName, _ := time.Now().In(loc).Zone()
	if tzName == "" {
		tzName = "local"
	}

	// Header phrasing depends on whether the caller asked for a
	// fixed lookback ("last 5h") or the default full-session view.
	// In the default case, name the actual span the data covers
	// — "entire session" + a from→to range — so the user can see
	// at a glance whether the timeline includes their idle time
	// or just the recent activity.
	var b strings.Builder
	fmt.Fprintf(&b, "# Session token usage — `%s`\n\n", short(out.SessionID))
	windowDesc := windowDescription(out, loc, tzName)
	if out.Model != "" && out.ContextWindow > 0 {
		fmt.Fprintf(&b, "%s · %d turns · model `%s` (%s context) · times in %s.\n\n",
			windowDesc, len(out.Points),
			out.Model, humanTokens(out.ContextWindow), tzName)
	} else {
		fmt.Fprintf(&b, "%s · %d turns · times in %s.\n\n",
			windowDesc, len(out.Points), tzName)
	}

	// Trajectory sentence — the headline answer to "how is my
	// session going?". Names absolute prefix sizes plus % of
	// context window when known.
	b.WriteString(renderTrajectorySentence(out))
	b.WriteString("\n\n")

	// ASCII sparkline of TotalInput (the prefix size at each turn).
	// Per-turn TokensIn is what grows visibly to the user as a
	// session continues, so this is the curve the user is asking
	// to see.
	values := totalInputSeries(out.Points)
	b.WriteString("```\n")
	b.WriteString(renderSparkline(values))
	b.WriteString("\n")
	b.WriteString(renderTimeAxis(out.WindowStartMs, out.WindowEndMs, sparklineCols))
	b.WriteString("\n")
	fmt.Fprintf(&b, "first ~%s · peak ~%s · now ~%s\n",
		humanTokens(out.FirstInput), humanTokens(out.PeakInput), humanTokens(out.LatestInput))
	b.WriteString("```\n\n")

	// Per-turn table. Samples 10 points evenly across the timeline
	// so growth is visible even on a long session whose last 10
	// turns happened within minutes of each other.
	//
	// Columns make the cache breakdown explicit:
	//   input tokens = prefix size at that turn (TokensIn).
	//   % of context = how much of the model's window is consumed.
	//   cached       = portion served from prompt cache at ~10×
	//                  discount (CachedReadTokens).
	//   uncached     = portion that bills against the 5-hour
	//                  rate-limit at full rate (TokensIn -
	//                  CachedReadTokens).
	//
	// input == cached + uncached for every row. Δ-vs-start is
	// dropped — the trajectory sentence already names start /
	// peak / latest, so duplicating it in the table costs width
	// without adding signal.
	const tableRows = 10
	samples := evenlySamplePoints(out.Points, tableRows)
	if out.ContextWindow > 0 {
		b.WriteString("| time     | input tokens | % of context | cached | uncached |\n")
		b.WriteString("|----------|-------------:|-------------:|-------:|---------:|\n")
		for _, p := range samples {
			when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
			pct := float64(p.TotalInput) / float64(out.ContextWindow) * 100
			if pct > 100 {
				pct = 100
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				when, humanTokens(p.TotalInput), formatPct(pct),
				humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
		}
	} else {
		b.WriteString("| time     | input tokens | cached | uncached |\n")
		b.WriteString("|----------|-------------:|-------:|---------:|\n")
		for _, p := range samples {
			when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				when, humanTokens(p.TotalInput),
				humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
		}
	}
	b.WriteString("\n")

	// Bottom line. Final amount + context-window percentage,
	// anchored to the timestamp of the latest data point so the
	// reader knows whether "now" means right now or two hours ago.
	if len(out.Points) > 0 {
		latestPt := out.Points[len(out.Points)-1]
		latestWhen := time.UnixMilli(latestPt.TsMs).In(loc).Format("15:04 " + tzName)
		ageStr := formatAge(out.WindowEndMs - latestPt.TsMs)
		if out.ContextWindow > 0 {
			fmt.Fprintf(&b,
				"**As of %s (%s ago), this session is at ~%s of input — %s of the %s context window.**\n",
				latestWhen, ageStr,
				humanTokens(out.LatestInput), formatPct(out.PctOfContext), humanTokens(out.ContextWindow),
			)
		} else {
			fmt.Fprintf(&b,
				"**As of %s (%s ago), this session is at ~%s of input.**\n",
				latestWhen, ageStr, humanTokens(out.LatestInput),
			)
		}
		// Honest footnote so users understand the two columns
		// answer different questions: prefix size (the "input
		// tokens" / "% of context" columns) is what fills the
		// model's context window, while uncached input is what
		// bills against the 5-hour rate-limit at full rate
		// (cached prefix re-reads are ~10× discounted).
		b.WriteString("\n_Uncached column = portion of each turn that bills against the 5-hour rate-limit at full rate (cached prefix is ~10× discounted by the provider)._\n")
	}
	return b.String()
}

// renderTrajectorySentence produces a one-line growth summary like
// "Started at 36K (4%) → peaked at 540K (54%) → now at 500K (50%)."
// Falls back to a context-window-free version when ContextWindow
// is unknown.
func renderTrajectorySentence(out TokenTimelineOutput) string {
	if out.ContextWindow > 0 {
		firstPct := float64(out.FirstInput) / float64(out.ContextWindow) * 100
		peakPct := float64(out.PeakInput) / float64(out.ContextWindow) * 100
		nowPct := out.PctOfContext
		return fmt.Sprintf(
			"Started at %s (%s of context) → peaked at %s (%s) → now at %s (%s).",
			humanTokens(out.FirstInput), formatPct(firstPct),
			humanTokens(out.PeakInput), formatPct(peakPct),
			humanTokens(out.LatestInput), formatPct(nowPct),
		)
	}
	return fmt.Sprintf(
		"Started at %s → peaked at %s → now at %s.",
		humanTokens(out.FirstInput), humanTokens(out.PeakInput), humanTokens(out.LatestInput),
	)
}

// totalInputSeries returns the per-turn TokensIn series — the
// sparkline values the renderer plots on the single-session axis.
func totalInputSeries(pts []contexthealth.TimelinePoint) []int64 {
	out := make([]int64, len(pts))
	for i, p := range pts {
		out[i] = p.TotalInput
	}
	return out
}

// evenlySamplePoints returns at most n points from pts at evenly
// spaced indices. Always includes the first and last point so the
// reader sees both ends of the trajectory.
//
// On a 165-turn session sampled to 10 rows, this returns indices
// 0, 18, 36, 54, 73, 91, 109, 127, 146, 164 — about one row per
// 30 minutes for a 5-hour session, which lets the user see the
// growth across the whole window instead of staring at the most
// recent burst.
func evenlySamplePoints(pts []contexthealth.TimelinePoint, n int) []contexthealth.TimelinePoint {
	if n <= 0 || len(pts) == 0 {
		return nil
	}
	if len(pts) <= n {
		return pts
	}
	out := make([]contexthealth.TimelinePoint, 0, n)
	last := -1
	for i := 0; i < n; i++ {
		idx := int(float64(i) * float64(len(pts)-1) / float64(n-1))
		if idx == last {
			// Defensive: when n approaches len(pts) we can land
			// on the same index twice. Skip duplicates so the
			// table doesn't show identical adjacent rows.
			continue
		}
		out = append(out, pts[idx])
		last = idx
	}
	return out
}

// formatDelta renders a per-turn growth delta vs the first turn
// as "+125K" / "-3K" / "0". Positive deltas get a leading "+" so
// the column reads at a glance.
func formatDelta(delta int64) string {
	switch {
	case delta == 0:
		return "0"
	case delta > 0:
		return "+" + humanTokens(delta)
	default:
		return "-" + humanTokens(-delta)
	}
}

// windowDescription renders the leading phrase of the timeline
// header. When the caller passed an explicit window, name it
// ("last 5h0m0s"). Otherwise describe the actual span the points
// cover ("entire session, 18:29 → 23:14") so the user sees the
// full timeline they asked for.
func windowDescription(out TokenTimelineOutput, loc *time.Location, tzName string) string {
	if len(out.Points) == 0 {
		return fmt.Sprintf("Window: last %s", formatWindow(out.WindowStartMs, out.WindowEndMs))
	}
	span := out.WindowEndMs - out.WindowStartMs
	// Heuristic: a "fixed window" view is one whose span lines up
	// closely with what `formatWindow` renders. The full-session
	// view's WindowStartMs equals the first point's TsMs, which
	// almost certainly does not match a clean "5h" or "1h" round
	// number — there is always a multi-second gap.
	first := time.UnixMilli(out.Points[0].TsMs).In(loc)
	last := time.UnixMilli(out.Points[len(out.Points)-1].TsMs).In(loc)
	if out.WindowStartMs == first.UnixMilli() {
		// Full-session view — we set WindowStartMs to the first
		// point's timestamp.
		return fmt.Sprintf("Window: entire session, %s → %s %s (span %s)",
			first.Format("Mon 15:04"), last.Format("Mon 15:04"), tzName,
			formatAge(span))
	}
	return fmt.Sprintf("Window: last %s", formatWindow(out.WindowStartMs, out.WindowEndMs))
}

// formatAge renders a duration as "30s" / "5m" / "1h" / "1h30m"
// for use in the "as of HH:MM (X ago)" bottom-line annotation.
// Negative or sub-second values render as "0s" so a freshly-
// updated session reads naturally.
func formatAge(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
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

// formatPct renders 0..100 as "12%" / "0.5%" / "<1%". Sub-1% and
// 0 cases are surfaced honestly so the user does not see "0%" on
// a session that has burned a few thousand tokens of a 1M window.
func formatPct(pct float64) string {
	switch {
	case pct <= 0:
		return "0%"
	case pct < 1:
		return "<1%"
	default:
		return fmt.Sprintf("%.0f%%", pct)
	}
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
//
// Returns 0 when neither a duration string nor an hours value is
// set, signalling "show the entire session" to ComputeTimeline.
// Idle sessions and long-paused sessions then surface their full
// history instead of being truncated to the rate-limit window.
//
// Explicit values are clamped to [1m, 24h]: sub-minute windows
// return near-empty timelines on real sessions; >24h windows
// extend smoothly via the no-window path anyway.
func resolveWindowMs(window string, hours int) int64 {
	const (
		minWindow = time.Minute
		maxWindow = 24 * time.Hour
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
	return 0
}
