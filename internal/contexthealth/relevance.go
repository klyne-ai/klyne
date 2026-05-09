package contexthealth

import (
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// relevance.go — per-file topic relevance scoring.
//
// Each file read in the session sits at some message index i. The
// surrounding user messages (window [i-anchorWindow, i+anchorWindow])
// describe what the user was working on when that read happened — the
// "topic anchor" for the file. The user's "current direction" is the
// bag-of-words of the last currentDirectionMsgs user messages.
//
// Per-file relevance = Jaccard(anchor, current_direction). A file is
// "stale" when its score falls below relevanceThreshold. Stale-share
// is sum(stale_bytes) / sum(all_loaded_file_bytes).
//
// The scoring is fully deterministic (Jaccard over bag-of-words). No
// AI calls. The thresholds are tunable from one place; eval data lives
// under docs/marketing/context-rescue-eval.md alongside the existing
// classifier rubric.

const (
	// anchorBackward is how many messages BEFORE a file read we sweep
	// for the user-prompt context that triggered the read. The window
	// is intentionally backward-only: forward-looking context bleeds
	// the next topic into a file's anchor as soon as the user pivots,
	// which is exactly the case we are trying to detect (the file is
	// stale relative to the new direction). Three messages is enough
	// to capture the immediate ask plus a clarifying turn.
	anchorBackward = 3

	// currentDirectionMsgs is how many of the most-recent user
	// messages define "what the user is currently working on." Mirrors
	// topicSampleSize so the relevance signal stays consistent with
	// the topic-shift detector.
	currentDirectionMsgs = 5

	// relevanceThreshold is the Jaccard cutoff below which a file is
	// considered stale relative to the user's current direction.
	// Tuned against the rubric examples; intentionally loose so we
	// don't false-positive when there is partial vocabulary overlap.
	relevanceThreshold = 0.20

	// staleShareThreshold is the fraction of total loaded file bytes
	// that must be classified stale before the relevance trigger
	// fires. Half is the smallest threshold that still says "more of
	// your loaded context is irrelevant than relevant."
	staleShareThreshold = 0.5

	// minLoadedBytesForRelevance gates the trigger on absolute
	// loaded-byte volume. A small session with one stale file does
	// not need an advisory; the saving would be in noise.
	minLoadedBytesForRelevance = 8 * 1024
)

// FileRelevance is the per-file relevance row returned by ScoreFiles.
// Sorted in the slice the function returns by Bytes descending, then
// Path ascending.
type FileRelevance struct {
	// Path is the absolute file path as recorded by the tool call.
	Path string
	// Score is the Jaccard overlap between the file's anchor and the
	// user's current direction. 0..1; higher means more relevant.
	Score float64
	// Bytes is the attributed context cost of the file across all of
	// its read+edit operations in the snapshot. Same accounting as
	// computeBloat — read-result bytes plus edit-payload bytes.
	Bytes int
	// Stale is true when Score < relevanceThreshold.
	Stale bool
}

// RelevanceVerdict is the aggregate result of ScoreFiles. Ready to
// drop into an advisor message.
type RelevanceVerdict struct {
	// Files is every loaded file with its score, sorted by bytes desc.
	Files []FileRelevance
	// StaleBytes is the sum of Bytes across files marked Stale.
	StaleBytes int
	// TotalBytes is the sum of Bytes across every entry in Files.
	TotalBytes int
	// StaleShare is StaleBytes / TotalBytes. 0 when TotalBytes is 0.
	StaleShare float64
	// ShouldFire is true when stale-share exceeds the trigger
	// threshold AND total loaded bytes exceed the minimum gate.
	ShouldFire bool
	// RelevantSubset is the top non-stale files by Bytes — the names
	// the advisor surfaces to the user as "still worth carrying
	// forward into a fresh session."
	RelevantSubset []FileRelevance
}

// ScoreFiles produces a RelevanceVerdict for the given message
// slice. Pure function — same input always yields the same output.
//
// Algorithm:
//  1. Pass over messages computing the per-file topic anchor
//     (bag-of-words of nearby user messages) AND attributed bytes
//     using the same accounting computeBloat already does.
//  2. Compute the user's current direction (bag-of-words of the last
//     N user messages).
//  3. For each file, score = Jaccard(anchor, current_direction).
//  4. Aggregate stale share and decide ShouldFire.
//
// Empty messages or zero loaded bytes return a zero verdict with
// ShouldFire == false.
func ScoreFiles(msgs []*connectors.Message) RelevanceVerdict {
	if len(msgs) == 0 {
		return RelevanceVerdict{}
	}

	currentDirection := bagOfWords(lastUserMsgs(msgs, currentDirectionMsgs))

	files := map[string]*fileAccum{}

	// Pass 1: build call-id -> file path attribution.
	type callMeta struct {
		path   string
		isRead bool
		isEdit bool
	}
	calls := map[string]callMeta{}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			path := extractPath(tc.Input)
			if path == "" {
				continue
			}
			switch {
			case isFileReadTool(tc.Name):
				calls[tc.ID] = callMeta{path: path, isRead: true}
				accum := getOrInitFile(files, path)
				mergeAnchor(accum.anchor, anchorBagAt(msgs, i))
			case isFileEditTool(tc.Name):
				calls[tc.ID] = callMeta{path: path, isEdit: true}
				accum := getOrInitFile(files, path)
				mergeAnchor(accum.anchor, anchorBagAt(msgs, i))
				// Edit payload bytes come straight from the call's
				// input (same accounting as computeBloat).
				accum.bytes += editPayloadBytes(tc.Input)
			}
		}
	}

	// Pass 2: attribute tool_result bytes back to the file bucket.
	for _, m := range msgs {
		for _, tr := range m.ToolResults {
			meta, ok := calls[tr.ID]
			if !ok || meta.path == "" {
				continue
			}
			accum, present := files[meta.path]
			if !present {
				continue
			}
			accum.bytes += len(tr.Output)
		}
	}

	if len(files) == 0 {
		return RelevanceVerdict{}
	}

	rows := make([]FileRelevance, 0, len(files))
	totalBytes := 0
	staleBytes := 0
	for path, accum := range files {
		score := jaccard(accum.anchor, currentDirection)
		stale := score < relevanceThreshold
		row := FileRelevance{
			Path:  path,
			Score: score,
			Bytes: accum.bytes,
			Stale: stale,
		}
		rows = append(rows, row)
		totalBytes += accum.bytes
		if stale {
			staleBytes += accum.bytes
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Bytes != rows[j].Bytes {
			return rows[i].Bytes > rows[j].Bytes
		}
		return rows[i].Path < rows[j].Path
	})

	verdict := RelevanceVerdict{
		Files:      rows,
		StaleBytes: staleBytes,
		TotalBytes: totalBytes,
	}
	if totalBytes > 0 {
		verdict.StaleShare = float64(staleBytes) / float64(totalBytes)
	}
	verdict.ShouldFire = verdict.StaleShare > staleShareThreshold &&
		totalBytes >= minLoadedBytesForRelevance

	verdict.RelevantSubset = topRelevantSubset(rows)
	return verdict
}

// getOrInitFile returns the per-file accumulator for path, creating
// one with an empty anchor set when missing.
func getOrInitFile(files map[string]*fileAccum, path string) *fileAccum {
	if existing, ok := files[path]; ok {
		return existing
	}
	created := &fileAccum{anchor: map[string]bool{}}
	files[path] = created
	return created
}

// fileAccum is the per-file accumulation state used inside ScoreFiles.
// Defined at package scope so the helper above can reference it.
type fileAccum struct {
	bytes  int
	anchor map[string]bool
}

// anchorBagAt returns the bag-of-words for user messages in the
// backward-looking window [idx - anchorBackward, idx] (inclusive of
// idx itself in case the read sits inside a user message scope, e.g.
// a tool-call from a user-role message in some connectors). Skips
// non-user messages so the anchor stays focused on the human's
// framing of the task. Backward-only so a forward topic pivot does
// not contaminate files loaded under the prior topic.
func anchorBagAt(msgs []*connectors.Message, idx int) map[string]bool {
	if idx < 0 || idx >= len(msgs) {
		return nil
	}
	lo := idx - anchorBackward
	if lo < 0 {
		lo = 0
	}
	scope := make([]*connectors.Message, 0, idx-lo+1)
	for i := lo; i <= idx; i++ {
		if msgs[i].Role == connectors.RoleUser {
			scope = append(scope, msgs[i])
		}
	}
	return bagOfWords(scope)
}

// mergeAnchor copies all keys from src into dst. Used because a single
// file may be read multiple times across the session at different
// indices — each read contributes its surrounding user-message context
// to the file's overall anchor.
func mergeAnchor(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}

// lastUserMsgs returns the last n user messages from msgs. Returns the
// available subset when n exceeds the user-message count.
func lastUserMsgs(msgs []*connectors.Message, n int) []*connectors.Message {
	out := make([]*connectors.Message, 0, n)
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		if msgs[i].Role == connectors.RoleUser {
			out = append([]*connectors.Message{msgs[i]}, out...)
		}
	}
	return out
}

// jaccard computes the Jaccard overlap |A ∩ B| / |A ∪ B|. Returns 0
// when both bags are empty. Returns 0 when one bag is empty (overlap
// is undefined — the file has no anchor or the session has no current
// direction yet).
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersect := 0
	for w := range a {
		if b[w] {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

// topRelevantSubset returns the non-stale files ordered by Bytes
// descending, capped at the top three. Empty slice when no file is
// relevant.
func topRelevantSubset(rows []FileRelevance) []FileRelevance {
	out := make([]FileRelevance, 0, 3)
	for _, r := range rows {
		if r.Stale {
			continue
		}
		out = append(out, r)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// RelevantBasenames returns the basenames of v.RelevantSubset,
// joined by ", ". Convenience for the advisor message string.
func (v RelevanceVerdict) RelevantBasenames() string {
	if len(v.RelevantSubset) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v.RelevantSubset))
	for _, f := range v.RelevantSubset {
		parts = append(parts, displayPath(f.Path))
	}
	return strings.Join(parts, ", ")
}
