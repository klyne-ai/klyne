package richentry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// categoryDisplayOrder defines the section ordering in the rendered
// daily markdown. Outcomes first (shipped / fixed / found), then
// in-flight work, then process/knowledge, then forward-looking. The
// reflection consumer iterates in this order so the most important
// "what landed" surfaces top-of-fold.
var categoryDisplayOrder = []struct {
	Key   string
	Title string
}{
	{"shipped", "Shipped"},
	{"bugs_fixed", "Bugs fixed"},
	{"bugs_found", "Bugs found"},
	{"features_worked_on", "Features worked on"},
	{"features_picked", "Features picked up"},
	{"investigations", "Investigations"},
	{"decisions", "Decisions"},
	{"config_changes", "Config / infra changes"},
	{"reviews_given", "Reviews given"},
	{"blockers", "Blockers"},
	{"blocked_on", "Blocked on"},
	{"pending", "Pending (tomorrow)"},
	{"followups_for_others", "Follow-ups for others"},
	{"must_remember", "Must remember"},
	{"mistakes_or_dead_ends", "Dead ends"},
}

// MergeDay reads the day's admitted rich entries for projectPath and
// renders them as a "What was done" markdown block. Returns ("", nil)
// when the day has no admitted entries — the caller falls back to
// the legacy LLM reflection path on that empty signal.
//
// Pure mechanical merge — no LLM, no opportunity to re-hallucinate.
// Items inside a category are kept in chronological order (rows are
// queried ts ASC); cross-bullet dedupe is a future polish (see plan).
func MergeDay(ctx context.Context, db *store.DB, projectPath string, day time.Time) (string, error) {
	rows, err := store.ListWorklogEntriesForDay(ctx, db, projectPath, day)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return MergeMarkdown(rows), nil
}

// MergeMarkdown is the pure renderer — takes the day's loaded entries
// and emits the section/bullet markdown. Exported so tests can drive
// it without a real DB.
func MergeMarkdown(rows []store.DayEntry) string {
	if len(rows) == 0 {
		return ""
	}
	// Aggregate items per category across all rows.
	bucket := map[string][]store.WorklogItem{}
	for _, r := range rows {
		for cat, items := range r.Entry.Categories {
			bucket[cat] = append(bucket[cat], items...)
		}
	}

	var sb strings.Builder
	for _, cat := range categoryDisplayOrder {
		items := bucket[cat.Key]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n\n", cat.Title)
		for _, it := range items {
			sb.WriteString(renderItem(it))
		}
		sb.WriteString("\n")
	}
	// Catch-all for any non-canonical category the writer emitted —
	// rendered last so the canonical order isn't disturbed but no
	// content silently disappears.
	extras := extraCategories(bucket)
	for _, cat := range extras {
		fmt.Fprintf(&sb, "## %s\n\n", cat)
		for _, it := range bucket[cat] {
			sb.WriteString(renderItem(it))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderItem turns one WorklogItem into a markdown bullet.
// Shape: "- {summary} [{ticket}] (`{refs}`)" — pieces that are empty
// are omitted. Refs are joined by ", " inside one inline-code span.
func renderItem(it store.WorklogItem) string {
	var sb strings.Builder
	sb.WriteString("- ")
	sb.WriteString(strings.TrimSpace(it.Summary))
	if t := strings.TrimSpace(it.Ticket); t != "" {
		fmt.Fprintf(&sb, " [%s]", t)
	}
	if len(it.Refs) > 0 {
		fmt.Fprintf(&sb, " (`%s`)", strings.Join(it.Refs, ", "))
	}
	sb.WriteString("\n")
	return sb.String()
}

// extraCategories returns category keys present in bucket but not in
// categoryDisplayOrder, sorted alphabetically. Used as a catch-all
// so a writer that emits a (rare) unknown category doesn't lose
// content silently.
func extraCategories(bucket map[string][]store.WorklogItem) []string {
	known := map[string]bool{}
	for _, c := range categoryDisplayOrder {
		known[c.Key] = true
	}
	var out []string
	for k, items := range bucket {
		if known[k] || len(items) == 0 {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
