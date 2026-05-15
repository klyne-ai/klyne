package contexthealth

import "sort"

// source_aggregate.go — cross-session aggregation of source attribution rows.
//
// AggregateSources is intentionally pure (no I/O) so the CLI layer can feed
// it one per-session []SourceRow slice at a time and inspect the result
// without coupling contexthealth to file I/O or the DB.

// AggregatedRow is the cross-session view of one named context source.
// It accumulates token loads and session appearances from many sessions.
type AggregatedRow struct {
	// Name is the human-readable source name (same as SourceRow.Name).
	Name string
	// Kind is the category of the source.
	Kind SourceKind
	// TotalTokens is the sum of per-session token estimates across all
	// sessions where this source appeared.
	TotalTokens int
	// SessionCount is how many sessions loaded this source at least once.
	SessionCount int
	// InvocationCount is the total number of tool calls attributed to this
	// source across all sessions. Zero means the source was loaded but never
	// called. Populated by the CLI layer after it tallies tool_calls_json.
	InvocationCount int
}

// RedundancyLevel classifies how often a source is used relative to how often
// it is loaded, so the renderer can apply the right marker.
type RedundancyLevel int

const (
	// RedundancyNone — the source is used regularly (not redundant).
	RedundancyNone RedundancyLevel = iota
	// RedundancySoft — source is used occasionally (≤ 1 call per 5 sessions).
	// Annotated with ⚠.
	RedundancySoft
	// RedundancyHard — source was never called across all sessions.
	// Annotated with ✗. Only applies when SessionCount ≥ 3 (one-off
	// experiments are excluded so the signal remains meaningful).
	RedundancyHard
)

// minSessionsForHardRedundancy is the minimum session count before we call a
// source "hard-redundant" (never invoked). A source that loaded in only 1–2
// sessions could simply be a one-time experiment, so we stay quiet.
const minSessionsForHardRedundancy = 3

// Redundancy returns the redundancy classification for r given its
// InvocationCount and SessionCount. The caller is responsible for setting
// InvocationCount before calling this.
func (r AggregatedRow) Redundancy() RedundancyLevel {
	if r.InvocationCount == 0 && r.SessionCount >= minSessionsForHardRedundancy {
		return RedundancyHard
	}
	// Soft-redundant: invocations are suspiciously rare relative to sessions.
	// Threshold: ≤ sessions/5 invocations (e.g. 1 call across 10 sessions).
	if r.SessionCount > 0 && r.InvocationCount > 0 && r.InvocationCount*5 <= r.SessionCount {
		return RedundancySoft
	}
	return RedundancyNone
}

// AggregateSources merges multiple per-session source-attribution slices into
// a single cross-session summary. Each inner slice is the output of one
// AttributeSources call. Duplicates within a single slice are impossible
// (AttributeSources already deduplicates); duplicates across slices (same
// source appearing in multiple sessions) are merged by accumulating tokens and
// incrementing SessionCount.
//
// The returned slice is sorted by TotalTokens descending.
func AggregateSources(perSession [][]SourceRow) []AggregatedRow {
	if len(perSession) == 0 {
		return nil
	}

	type bucket struct {
		kind         SourceKind
		totalTokens  int
		sessionCount int
	}
	buckets := map[string]*bucket{}
	order := []string{} // insertion order for determinism before sort

	for _, sessionRows := range perSession {
		for _, row := range sessionRows {
			if b, ok := buckets[row.Name]; ok {
				b.totalTokens += row.Tokens
				b.sessionCount++
			} else {
				buckets[row.Name] = &bucket{
					kind:         row.Kind,
					totalTokens:  row.Tokens,
					sessionCount: 1,
				}
				order = append(order, row.Name)
			}
		}
	}

	out := make([]AggregatedRow, 0, len(buckets))
	for _, name := range order {
		b := buckets[name]
		out = append(out, AggregatedRow{
			Name:         name,
			Kind:         b.kind,
			TotalTokens:  b.totalTokens,
			SessionCount: b.sessionCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Name < out[j].Name
	})
	return out
}
