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

// MergeDayAll renders the report-level "What was done" — every
// admitted rich entry across all projects on `day` merged into one
// block. Each bullet's renderItem includes the source repo prefix
// (see renderItem) so a multi-service day reads cleanly as one
// timeline. Returns ("", nil) on a day with no admitted entries.
func MergeDayAll(ctx context.Context, db *store.DB, day time.Time) (string, error) {
	rows, err := store.ListWorklogEntriesForDayAll(ctx, db, day)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return MergeMarkdown(rows), nil
}

// categoryBulletCap caps how many bullets render per category — under
// per-turn cardinality (one rich entry per Stop hook turn, 200+ turns
// in a busy day) a naive concatenation produces 1000+ bullets per
// category and reads like a commit log dump, not a summary. After
// dedup by (repo, normalized summary) we keep the most recent N and
// add a "…and X more" overflow line. 25 is high enough to never hide
// a deliberate distinct item, low enough to read in one screen.
const categoryBulletCap = 25

// MergeMarkdown is the pure renderer — takes the day's loaded entries
// and emits the section/bullet markdown. Exported so tests can drive
// it without a real DB.
//
// Two-stage rendering:
//  1. dedupCategoryItems collapses (repo, normalized-summary) duplicates
//     across all turns into one bullet, merging their refs.
//  2. If a category still has more than `categoryBulletCap` distinct
//     bullets, the head N (most-recent first) is rendered and an
//     "…and X more (collapsed)" line appended so the count is honest.
func MergeMarkdown(rows []store.DayEntry) string {
	if len(rows) == 0 {
		return ""
	}
	// Aggregate items per category across all rows. Items remember
	// their source row's Ts so dedup can keep the most recent
	// occurrence's metadata and the cap can order chronologically.
	rawBucket := map[string][]tsItem{}
	for _, r := range rows {
		for cat, items := range r.Entry.Categories {
			for _, it := range items {
				rawBucket[cat] = append(rawBucket[cat], tsItem{Ts: r.Ts, Item: it})
			}
		}
	}

	var sb strings.Builder
	for _, cat := range categoryDisplayOrder {
		items := rawBucket[cat.Key]
		if len(items) == 0 {
			continue
		}
		writeCategorySection(&sb, cat.Title, dedupCategoryItems(items))
	}
	// Catch-all for any non-canonical category the writer emitted —
	// rendered last so the canonical order isn't disturbed but no
	// content silently disappears.
	extras := extraCategoriesFromRaw(rawBucket)
	for _, cat := range extras {
		writeCategorySection(&sb, cat, dedupCategoryItems(rawBucket[cat]))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// dedupCategoryItems collapses items inside one category by
// (repo, normalized-summary). The first occurrence's ticket is kept;
// refs are unioned across duplicates (preserving first-seen order).
// Returns items sorted newest-first by the latest Ts among each
// dedup group's contributors so the top-of-list is the most recent
// stuff.
func dedupCategoryItems(in []tsItem) []store.WorklogItem {
	type acc struct {
		latestTs int64
		earliest int // insertion order — stable tiebreaker
		item     store.WorklogItem
		seenRefs map[string]bool
	}
	byKey := map[string]*acc{}
	order := []string{}
	for i, ti := range in {
		key := dedupKey(ti.Item)
		a, ok := byKey[key]
		if !ok {
			a = &acc{latestTs: ti.Ts, earliest: i, seenRefs: map[string]bool{}}
			a.item = store.WorklogItem{
				Summary: strings.TrimSpace(ti.Item.Summary),
				Repo:    strings.TrimSpace(ti.Item.Repo),
				Ticket:  strings.TrimSpace(ti.Item.Ticket),
			}
			for _, ref := range ti.Item.Refs {
				r := strings.TrimSpace(ref)
				if r != "" && !a.seenRefs[r] {
					a.seenRefs[r] = true
					a.item.Refs = append(a.item.Refs, r)
				}
			}
			byKey[key] = a
			order = append(order, key)
			continue
		}
		// Duplicate — merge metadata into the existing acc.
		if ti.Ts > a.latestTs {
			a.latestTs = ti.Ts
		}
		if a.item.Ticket == "" {
			a.item.Ticket = strings.TrimSpace(ti.Item.Ticket)
		}
		for _, ref := range ti.Item.Refs {
			r := strings.TrimSpace(ref)
			if r != "" && !a.seenRefs[r] {
				a.seenRefs[r] = true
				a.item.Refs = append(a.item.Refs, r)
			}
		}
	}
	out := make([]*acc, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	// Newest first (by latest contributing Ts), stable on insertion
	// order so two items with no Ts info keep deterministic ordering.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].latestTs != out[j].latestTs {
			return out[i].latestTs > out[j].latestTs
		}
		return out[i].earliest < out[j].earliest
	})
	final := make([]store.WorklogItem, 0, len(out))
	for _, a := range out {
		final = append(final, a.item)
	}
	return final
}

// dedupKey is the equality predicate for collapsing duplicate
// bullets. Same repo + summary (lowercased, whitespace-normalized) =
// same bullet. Refs and ticket are NOT part of the key — they
// differ across turns describing the same work and should be merged,
// not split.
func dedupKey(it store.WorklogItem) string {
	return strings.ToLower(strings.TrimSpace(it.Repo)) + "\x1f" + normalizeSpaces(strings.ToLower(it.Summary))
}

// normalizeSpaces collapses any run of whitespace to a single space,
// so trivial wording variations like "two  spaces" vs "two spaces"
// dedup together.
func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// writeCategorySection renders one category's deduped+capped bullets
// plus the overflow line when the cap was hit. Empty categories
// don't reach here — caller filters first.
func writeCategorySection(sb *strings.Builder, title string, items []store.WorklogItem) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "## %s\n\n", title)
	limit := len(items)
	if limit > categoryBulletCap {
		limit = categoryBulletCap
	}
	for i := 0; i < limit; i++ {
		sb.WriteString(renderItem(items[i]))
	}
	if len(items) > limit {
		fmt.Fprintf(sb, "- _…and %d more (collapsed — distinct bullets after dedup exceeded the per-category cap)_\n", len(items)-limit)
	}
	sb.WriteString("\n")
}

// extraCategoriesFromRaw mirrors extraCategories but reads the
// ts-tagged raw bucket so callers can pass the same data shape
// MergeMarkdown uses internally.
func extraCategoriesFromRaw(bucket map[string][]tsItem) []string {
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

// renderItem turns one WorklogItem into a markdown bullet.
// Shape: "- **{repo}** — {summary} [{ticket}] (`{refs}`)" — pieces
// that are empty are omitted. The repo prefix is the writer's
// `repo` field on the item; when entries from multiple services
// merge into one day's "What was done" the prefix is what tells
// the reader which codebase a bullet belongs to. Refs are joined
// by ", " inside one inline-code span.
func renderItem(it store.WorklogItem) string {
	var sb strings.Builder
	sb.WriteString("- ")
	if r := strings.TrimSpace(it.Repo); r != "" {
		fmt.Fprintf(&sb, "**%s** — ", r)
	}
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

// tsItem pairs a WorklogItem with the timestamp of the
// stop_summaries row it came from so dedupCategoryItems can rank
// duplicates by recency.
type tsItem struct {
	Ts   int64
	Item store.WorklogItem
}
