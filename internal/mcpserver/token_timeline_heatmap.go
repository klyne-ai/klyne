package mcpserver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// renderSessionHeatmap renders a tokscale-inspired hour-of-day heatmap
// for the per-turn points of a single session.
//
// Layout:
//
//	┌─ Single-day session ────────────────────────────────────────┐
//	│ Activity (UTC+5:30)                                         │
//	│ 00      06      12      18      23                          │
//	│ ░░░░ ░░▒▒▓██▓▒░░░░░ ▒█████▓░░░░░                            │
//	│ peak hour 14:00 (54 turns / 1.2M tokens) · 8 active hours   │
//	└─────────────────────────────────────────────────────────────┘
//
// Multi-day session: one row per day, each row 24 cells.
//
// The function returns an empty string for tiny sessions (< 6 turns)
// where a heatmap is not informative.
func renderSessionHeatmap(points []contexthealth.TimelinePoint, loc *time.Location, tzName string) string {
	if len(points) < 6 {
		return ""
	}

	// Bucket: per (date, hour). Each bucket records turn count + total
	// effective-input tokens so the peak can be reported with both axes.
	cells := map[string]*bucket24{} // key: "YYYY-MM-DD H"
	dates := map[string]struct{}{}
	maxTokens := int64(0)
	var peakKey string

	for _, p := range points {
		t := time.UnixMilli(p.TsMs).In(loc)
		date := t.Format("2006-01-02")
		dates[date] = struct{}{}
		key := fmt.Sprintf("%s %d", date, t.Hour())
		b := cells[key]
		if b == nil {
			b = &bucket24{}
			cells[key] = b
		}
		b.turns++
		b.tokens += p.EffectiveInput
		if b.tokens > maxTokens {
			maxTokens = b.tokens
			peakKey = key
		}
	}
	if maxTokens == 0 {
		return ""
	}

	// Compute quantile thresholds from non-zero buckets so the colour
	// scale tracks the session's own intensity range, not absolute
	// token counts.
	nonZero := make([]int64, 0, len(cells))
	for _, b := range cells {
		if b.tokens > 0 {
			nonZero = append(nonZero, b.tokens)
		}
	}
	sort.Slice(nonZero, func(i, j int) bool { return nonZero[i] < nonZero[j] })
	q := func(p float64) int64 {
		if len(nonZero) == 0 {
			return 0
		}
		return nonZero[int(float64(len(nonZero)-1)*p)]
	}
	t1, t2, t3 := q(0.25), q(0.50), q(0.85)

	// Sort dates ascending.
	sortedDates := make([]string, 0, len(dates))
	for d := range dates {
		sortedDates = append(sortedDates, d)
	}
	sort.Strings(sortedDates)

	var b strings.Builder
	fmt.Fprintf(&b, "Activity heatmap (%s)\n", tzName)

	if len(sortedDates) == 1 {
		// Single-day session: render axis header + one 24-hour row.
		b.WriteString("```\n")
		b.WriteString(heatmapHourAxis())
		b.WriteString("\n")
		b.WriteString(heatmapRow(cells, sortedDates[0], t1, t2, t3))
		b.WriteString("\n")
		b.WriteString("```\n")
	} else {
		// Multi-day session: prefix each row with the date.
		b.WriteString("```\n")
		// header
		b.WriteString("        ")
		b.WriteString(heatmapHourAxis())
		b.WriteString("\n")
		for _, d := range sortedDates {
			// Month-day prefix in 6 char width: "May 12"
			t, _ := time.Parse("2006-01-02", d)
			label := t.Format("Jan 02")
			fmt.Fprintf(&b, "%-7s ", label)
			b.WriteString(heatmapRow(cells, d, t1, t2, t3))
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}

	// Peak hour line.
	if peakKey != "" {
		pk := cells[peakKey]
		parts := strings.SplitN(peakKey, " ", 2)
		if len(parts) == 2 {
			when, _ := time.Parse("2006-01-02", parts[0])
			fmt.Fprintf(&b, "peak hour %s %s:00 — %d turn(s), %s effective tokens · %d active hour(s) total\n",
				when.Format("Jan 02"),
				parts[1],
				pk.turns,
				humanTokens(pk.tokens),
				len(nonZero),
			)
		}
	}
	return b.String()
}

// heatmapHourAxis returns the 24-column hour axis header. Three labels
// drawn at hours 0, 6, 12, 18, 23 for readability.
//
//	00      06      12      18      23
func heatmapHourAxis() string {
	// Each hour cell is 2 chars wide + 1 space between groups of 6.
	// To keep alignment simple we render hours as label-or-spaces in
	// a fixed 24-cell row.
	var b strings.Builder
	for h := 0; h < 24; h++ {
		switch h {
		case 0, 6, 12, 18:
			b.WriteString(fmt.Sprintf("%02d", h))
		case 23:
			b.WriteString("23")
		default:
			b.WriteString("  ")
		}
	}
	return b.String()
}

// heatmapRow renders a 24-cell row for a single date. Each cell is two
// Unicode-block chars wide so the row aligns visually with the axis.
func heatmapRow(cells map[string]*bucket24, date string, t1, t2, t3 int64) string {
	var b strings.Builder
	for h := 0; h < 24; h++ {
		key := fmt.Sprintf("%s %d", date, h)
		c, ok := cells[key]
		var glyph string
		switch {
		case !ok || c.tokens == 0:
			glyph = "··"
		case c.tokens <= t1:
			glyph = "░░"
		case c.tokens <= t2:
			glyph = "▒▒"
		case c.tokens <= t3:
			glyph = "▓▓"
		default:
			glyph = "██"
		}
		b.WriteString(glyph)
	}
	return b.String()
}

// bucket24 is the shared (date, hour) accumulator used while building
// the heatmap. One row per turn-bearing hour in the session.
type bucket24 struct {
	turns  int
	tokens int64
}
