package contexthealth

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// classifier.go — the deterministic state machine.
//
// Thresholds and rule order are mirrored verbatim from the labelling
// rubric in docs/marketing/context-rescue-eval.md. When that doc moves,
// these constants move with it. The eval runner under
// docs/eval/context-health/ is the source of truth for whether they are
// correct; do not "tune" a constant here without re-running it.

const (
	// Context-fill bands. Each band is the LOWER bound of its state when
	// no other rule promotes the session above it.
	rescueFillThreshold   = 75.0 // ≥ this → rescue
	riskyFillThreshold    = 50.0 // ≥ this → risky
	driftingFillThreshold = 30.0 // ≥ this → drifting

	// Topic-shift fill threshold. A topic shift below this fill stays
	// "drifting"; at or above it, the shift escalates to rescue.
	topicShiftRescueFill = 40.0

	// Repetition bands for file reads and command failures.
	rescueFileReadCount    = 5 // ≥ this same-file read count → rescue
	riskyFileReadMinCount  = 3 // 3..rescueFileReadCount-1 → risky
	rescueCommandFailCount = 4 // ≥ this same-command failure count → rescue

	// Hidden-ratio threshold. tool+system messages over assistant turns.
	riskyHiddenRatio = 3.0

	// Snapshot window for repetition / ratio detection. The strategy doc
	// uses "the last 20 messages" as the unit of analysis; tests must
	// match.
	repetitionWindow = 20

	// Topic-shift detection looks at the first N user messages vs the
	// last N user messages. 5 is small enough to capture the opening
	// task framing, large enough that a single off-topic prompt can't
	// flip the verdict.
	topicSampleSize = 5

	// Bag-of-words overlap below this is "shifted". Tuned against the
	// rubric examples in docs/marketing/context-rescue-eval.md.
	topicOverlapMin = 0.30
	topicMinWords   = 3 // each side needs at least this many distinct words

	// Bloat scorecard cap.
	maxBloatRows = 5
)

// classify is the deterministic state machine. It is split into three
// stages: signal extraction, rule evaluation, then result assembly with
// a human-readable reason. Splitting them keeps each stage independently
// reviewable against the rubric.
func classify(in Input) Result {
	signals := extractSignals(in)
	state, reason := decideState(in.ContextFillPct, signals)
	return Result{
		State:   state,
		Action:  actionFor(state),
		Reason:  reason,
		Bloat:   computeBloat(in.Messages),
		Signals: signals,
	}
}

// extractSignals walks the snapshot once and computes every input the
// state machine needs. Pure function over Input.
func extractSignals(in Input) Signals {
	window := tailMessages(in.Messages, repetitionWindow)

	assistantCount := 0
	hiddenCount := 0
	for _, m := range window {
		switch m.Role {
		case connectors.RoleAssistant:
			assistantCount++
		case connectors.RoleTool, connectors.RoleSystem:
			hiddenCount++
		}
	}
	hiddenRatio := 0.0
	if assistantCount > 0 {
		hiddenRatio = float64(hiddenCount) / float64(assistantCount)
	} else if hiddenCount > 0 {
		// Avoid div-by-zero while still surfacing pathological cases.
		hiddenRatio = float64(hiddenCount)
	}

	topFile, topFileCount := topRepeatedFileRead(window)
	topCmd, topCmdCount := topRepeatedFailedCommand(window)
	shifted := topicShifted(in.Messages)

	return Signals{
		ContextFillPct:        in.ContextFillPct,
		MsgCount:              in.MsgCount,
		AssistantMsgCount:     assistantCount,
		HiddenRatio:           hiddenRatio,
		TopRepeatedFile:       topFile,
		TopRepeatedFileCount:  topFileCount,
		TopFailedCommand:      topCmd,
		TopFailedCommandCount: topCmdCount,
		TopicShifted:          shifted,
	}
}

// decideState applies the rubric in strict order: rescue triggers win,
// then risky, then drifting, then healthy by default. The reason string
// names the FIRST trigger that fired so the UI can be honest about why.
func decideState(fill float64, s Signals) (State, string) {
	// 1. Rescue triggers (any one).
	if fill >= rescueFillThreshold {
		return StateRescueNow, fmt.Sprintf(
			"Context is %.0f%% full — start fresh before the next turn balloons.", fill,
		)
	}
	if s.TopRepeatedFileCount >= rescueFileReadCount {
		return StateRescueNow, fmt.Sprintf(
			"%s has been re-read %d times — context is loaded with redundant copies.",
			displayPath(s.TopRepeatedFile), s.TopRepeatedFileCount,
		)
	}
	if s.TopFailedCommandCount >= rescueCommandFailCount {
		return StateRescueNow, fmt.Sprintf(
			"`%s` has failed %d times — start fresh with a corrected plan.",
			s.TopFailedCommand, s.TopFailedCommandCount,
		)
	}
	if s.TopicShifted && fill >= topicShiftRescueFill {
		return StateRescueNow, fmt.Sprintf(
			"Topic has shifted from the original task and context is %.0f%% full — handoff to a fresh session.", fill,
		)
	}

	// 2. Risky triggers (any one).
	if fill >= riskyFillThreshold {
		return StateRisky, fmt.Sprintf(
			"Context is %.0f%% full — compact now to keep the next turns fast.", fill,
		)
	}
	if s.TopRepeatedFileCount >= riskyFileReadMinCount {
		return StateRisky, fmt.Sprintf(
			"%s has been re-read %d times — compact to drop the duplicates.",
			displayPath(s.TopRepeatedFile), s.TopRepeatedFileCount,
		)
	}
	if s.HiddenRatio >= riskyHiddenRatio {
		return StateRisky, fmt.Sprintf(
			"Tool/system rows outnumber assistant turns %.1f:1 — compact to thin the noise.", s.HiddenRatio,
		)
	}

	// 3. Drifting triggers (any one).
	if fill >= driftingFillThreshold {
		return StateDrifting, fmt.Sprintf(
			"Context is %.0f%% full — consider compacting before it grows further.", fill,
		)
	}
	if s.TopicShifted {
		return StateDrifting, "Conversation has shifted topic from the original task — compact when convenient."
	}

	// 4. Healthy default.
	return StateHealthy, "Session is healthy — keep going."
}

// actionFor maps each state to its next-step recommendation. Kept as a
// switch (not a map) so the compiler catches any new state added without
// a corresponding action.
func actionFor(s State) Action {
	switch s {
	case StateRescueNow:
		return ActionStartFresh
	case StateRisky:
		return ActionCompact
	case StateDrifting, StateHealthy:
		return ActionContinue
	default:
		return ActionContinue
	}
}

// tailMessages returns the last n messages from xs (or all of them when
// len(xs) ≤ n). Returns the original slice rather than copying — callers
// must not mutate it.
func tailMessages(xs []*connectors.Message, n int) []*connectors.Message {
	if len(xs) <= n {
		return xs
	}
	return xs[len(xs)-n:]
}

// topRepeatedFileRead inspects every Read/View/Cat/Open tool call in
// the window — explicitly NOT Edit/MultiEdit — and returns the path
// read most often plus its count. Returns "", 0 when no path appeared
// more than once.
//
// The Read-only narrowing is deliberate. The earlier version of this
// helper bucketed Edit and MultiEdit alongside Read, which caused
// long iterative-refactor sessions to falsely trigger the "same file
// re-read N+ times" rescue rule. Edits change the file; the AI is not
// re-reading the prior content because it forgot, it is making
// progress. Reads, by contrast, indicate context-loss: a Read of the
// same file 5 times is the AI re-loading content it should have
// remembered.
func topRepeatedFileRead(window []*connectors.Message) (string, int) {
	counts := map[string]int{}
	for _, m := range window {
		for _, tc := range m.ToolCalls {
			if !isFileReadTool(tc.Name) {
				continue
			}
			path := extractPath(tc.Input)
			if path == "" {
				continue
			}
			counts[path]++
		}
	}
	return topByCount(counts)
}

// topRepeatedFailedCommand pairs Bash tool calls with their tool-result
// replies (matched by ToolCall.ID == ToolResult.ID) and counts how many
// times each normalised command stem failed.
func topRepeatedFailedCommand(window []*connectors.Message) (string, int) {
	// Build call-id → command-stem from assistant messages.
	stems := map[string]string{}
	for _, m := range window {
		for _, tc := range m.ToolCalls {
			if !isShellTool(tc.Name) {
				continue
			}
			stem := commandStem(extractCommand(tc.Input))
			if stem == "" {
				continue
			}
			stems[tc.ID] = stem
		}
	}
	// Walk tool-result messages and count failures per stem.
	counts := map[string]int{}
	for _, m := range window {
		for _, tr := range m.ToolResults {
			if !tr.IsError {
				continue
			}
			stem, ok := stems[tr.ID]
			if !ok {
				continue
			}
			counts[stem]++
		}
	}
	return topByCount(counts)
}

// topByCount returns the key with the highest value plus that value.
// Ties broken by lexicographic order on the key for determinism.
// Returns "", 0 when counts is empty or every value is ≤ 1.
func topByCount(counts map[string]int) (string, int) {
	bestKey := ""
	bestN := 0
	for k, n := range counts {
		if n < 2 {
			continue
		}
		if n > bestN || (n == bestN && k < bestKey) {
			bestKey = k
			bestN = n
		}
	}
	return bestKey, bestN
}

// topicShifted compares the bag-of-words of the first topicSampleSize
// user messages against the last topicSampleSize user messages. Returns
// true when the Jaccard overlap falls below topicOverlapMin AND both
// sides have at least topicMinWords distinct content words.
//
// Sampling intentionally walks the full Messages slice, not the
// repetition window, so an old-but-relevant opening task isn't lost when
// the snapshot trims it.
func topicShifted(all []*connectors.Message) bool {
	users := make([]*connectors.Message, 0, len(all))
	for _, m := range all {
		if m.Role == connectors.RoleUser {
			users = append(users, m)
		}
	}
	if len(users) < 2*topicSampleSize {
		return false
	}
	first := users[:topicSampleSize]
	last := users[len(users)-topicSampleSize:]

	firstBag := bagOfWords(first)
	lastBag := bagOfWords(last)
	if len(firstBag) < topicMinWords || len(lastBag) < topicMinWords {
		return false
	}

	intersect := 0
	for w := range firstBag {
		if lastBag[w] {
			intersect++
		}
	}
	union := len(firstBag) + len(lastBag) - intersect
	if union == 0 {
		return false
	}
	jaccard := float64(intersect) / float64(union)
	return jaccard < topicOverlapMin
}

// bagOfWords returns the set of distinct lowercase content words across
// a slice of messages, with stop-words and very short tokens dropped so
// the overlap signal isn't drowned by "the", "a", "to".
func bagOfWords(msgs []*connectors.Message) map[string]bool {
	out := map[string]bool{}
	for _, m := range msgs {
		for _, tok := range tokenize(m.Content) {
			if isStopWord(tok) {
				continue
			}
			out[tok] = true
		}
	}
	return out
}

// tokenize lowercases and splits text into alphabetic tokens of length
// ≥ 3. Punctuation, digits, and very short tokens are discarded so the
// overlap focuses on content nouns/verbs.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var (
		out  []string
		cur  strings.Builder
		emit = func() {
			if cur.Len() >= 3 {
				out = append(out, cur.String())
			}
			cur.Reset()
		}
	)
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			cur.WriteRune(r)
			continue
		}
		emit()
	}
	emit()
	return out
}

// stopWords are the most common English fillers that drown out the
// topic signal. Kept tight on purpose — bigger lists overfit to specific
// rubric fixtures.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "have": true, "has": true,
	"are": true, "was": true, "were": true, "but": true, "not": true,
	"can": true, "you": true, "your": true, "our": true, "any": true,
	"all": true, "use": true, "used": true, "using": true,
	"please": true, "now": true, "also": true, "still": true,
	"need": true, "want": true, "make": true, "make sure": true,
	"why": true, "what": true, "when": true, "where": true, "how": true,
	"its": true, "his": true, "her": true, "their": true, "they": true,
	"them": true, "him": true, "she": true, "let": true, "say": true,
	"there": true, "here": true,
}

func isStopWord(w string) bool { return stopWords[w] }

// isFileReadTool returns true for tool names that PURELY load file
// content into context — Read, View, Cat, Open. Edit and MultiEdit
// also load content (the old_string is matched against the file) but
// are tracked separately by isFileEditTool because their repetition
// pattern is fundamentally different: an Edit changes the file, so
// repeated Edits on the same file are progress, not context-loss.
func isFileReadTool(name string) bool {
	switch strings.ToLower(name) {
	case "read", "view", "cat", "open":
		return true
	}
	return false
}

// isFileEditTool returns true for tool names that modify file content.
// Like isFileReadTool, the names mirror Claude Code's canonical tool
// names. Used by computeBloat for the per-file edit-count breakdown
// the bloat scorecard exposes; NOT used by topRepeatedFileRead, on
// purpose (see that function's comment for why).
func isFileEditTool(name string) bool {
	switch strings.ToLower(name) {
	case "edit", "multiedit":
		return true
	}
	return false
}

// isShellTool returns true for tool names that execute shell commands.
func isShellTool(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "bash", "shell", "sh", "exec", "run":
		return true
	}
	return false
}

// extractPath pulls the file path out of a Read/Edit tool-call's JSON
// input. Tries the canonical Claude Code key (file_path) first, then a
// few Codex / fallback variants.
func extractPath(input string) string {
	if input == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filepath", "filename"} {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// extractCommand pulls the shell command out of a Bash tool-call's JSON
// input.
func extractCommand(input string) string {
	if input == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "script"} {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// commandStem returns a normalised shorthand for a shell command suitable
// for grouping repeated invocations. Examples:
//
//	"npm test"               → "npm test"
//	"go test ./internal/..." → "go test"
//	"git push origin main"   → "git push"
//	"./run.sh --flag"        → "./run.sh"
//
// Two tokens is enough for almost every common test/build/lint command;
// adding more would over-split (e.g. "npm test --silent" vs "npm test").
func commandStem(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	parts := strings.Fields(cmd)
	switch {
	case len(parts) == 1:
		return parts[0]
	case strings.HasPrefix(parts[1], "-"):
		return parts[0]
	default:
		return parts[0] + " " + parts[1]
	}
}

// displayPath returns just the basename of an absolute path so error
// messages stay readable. Falls back to the input when there is no
// path separator.
func displayPath(p string) string {
	if p == "" {
		return ""
	}
	base := filepath.Base(p)
	if base == "." || base == "/" {
		return p
	}
	return base
}

// computeBloat returns the top-N tool-output sources by attributed
// share, sorted descending by SharePct. The denominator is the total
// byte size of every tool result in the snapshot, so SharePct values
// across rows sum to ≤ 100 (≤ because the cap drops the long tail).
//
// File rows aggregate Read AND Edit calls on the same path into ONE
// bucket so the user does not see "foo.go" twice in the top-5. The
// row's ReadCount and EditCount fields preserve the breakdown so
// consumers can tell content-loading repetition from refactor-edit
// repetition. Kind is set to file_read / file_edit / file_mixed
// depending on which dominates.
func computeBloat(msgs []*connectors.Message) []BloatRow {
	// Per-file-path bucket aggregating both Read and Edit calls.
	type fileBucket struct {
		basename  string
		readCount int
		editCount int
		bytes     int
	}
	files := map[string]*fileBucket{} // keyed on full path

	// Per-non-file bucket (commands + unattributed tool_results).
	type nonFileBucket struct {
		label string
		kind  BloatKind
		count int
		bytes int
	}
	nonFiles := map[string]*nonFileBucket{} // keyed on label

	totalBytes := 0

	// Pass 1: build call-id → attribution. Files attribute to their
	// path; commands attribute to their stem; everything else falls
	// through to the unattributed "Tool result" bucket at result time.
	type callMeta struct {
		// Exactly one of these three is set per call.
		filePath string
		isRead   bool
		isEdit   bool
		// Or:
		commandLabel string
	}
	calls := map[string]callMeta{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			switch {
			case isFileReadTool(tc.Name):
				if path := extractPath(tc.Input); path != "" {
					calls[tc.ID] = callMeta{filePath: path, isRead: true}
				}
			case isFileEditTool(tc.Name):
				if path := extractPath(tc.Input); path != "" {
					calls[tc.ID] = callMeta{filePath: path, isEdit: true}
				}
			case isShellTool(tc.Name):
				if stem := commandStem(extractCommand(tc.Input)); stem != "" {
					calls[tc.ID] = callMeta{commandLabel: tc.Name + ": " + stem}
				}
			}
		}
	}

	// Pass 2: walk tool_results and attribute their bytes back to the
	// originating call's bucket.
	for _, m := range msgs {
		for _, tr := range m.ToolResults {
			size := len(tr.Output)
			totalBytes += size
			meta, ok := calls[tr.ID]
			switch {
			case ok && meta.filePath != "":
				b, present := files[meta.filePath]
				if !present {
					b = &fileBucket{basename: displayPath(meta.filePath)}
					files[meta.filePath] = b
				}
				if meta.isRead {
					b.readCount++
				}
				if meta.isEdit {
					b.editCount++
				}
				b.bytes += size
			case ok && meta.commandLabel != "":
				b, present := nonFiles[meta.commandLabel]
				if !present {
					b = &nonFileBucket{label: meta.commandLabel, kind: BloatKindCommand}
					nonFiles[meta.commandLabel] = b
				}
				b.count++
				b.bytes += size
			default:
				const fallback = "Tool result"
				b, present := nonFiles[fallback]
				if !present {
					b = &nonFileBucket{label: fallback, kind: BloatKindToolResult}
					nonFiles[fallback] = b
				}
				b.count++
				b.bytes += size
			}
		}
	}

	// Edits load file content into the prompt the same way Reads do
	// (via the old_string match), but a tool_result tied to an Edit
	// is typically very small ("File modified successfully"). To
	// reflect that Edits really DO contribute to bloat, also count
	// the Edit-call's old_string + new_string bytes into the file
	// bucket. Without this, an edit-heavy file shows artificially
	// low SharePct because the result blobs are tiny.
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			meta, ok := calls[tc.ID]
			if !ok || meta.filePath == "" || !meta.isEdit {
				continue
			}
			size := editPayloadBytes(tc.Input)
			totalBytes += size
			if b, present := files[meta.filePath]; present {
				b.bytes += size
			}
		}
	}

	if len(files) == 0 && len(nonFiles) == 0 || totalBytes == 0 {
		return nil
	}

	// Materialise rows. File rows get the new ReadCount / EditCount
	// breakdown plus a label that names the dominant action.
	rows := make([]BloatRow, 0, len(files)+len(nonFiles))
	for _, b := range files {
		count := b.readCount + b.editCount
		rows = append(rows, BloatRow{
			Label:     fileBloatLabel(b.basename, b.readCount, b.editCount),
			Kind:      fileBloatKind(b.readCount, b.editCount),
			Count:     count,
			ReadCount: b.readCount,
			EditCount: b.editCount,
			SharePct:  float64(b.bytes) / float64(totalBytes) * 100.0,
		})
	}
	for _, b := range nonFiles {
		rows = append(rows, BloatRow{
			Label:    b.label,
			Kind:     b.kind,
			Count:    b.count,
			SharePct: float64(b.bytes) / float64(totalBytes) * 100.0,
		})
	}
	sortBloatRows(rows)
	if len(rows) > maxBloatRows {
		rows = rows[:maxBloatRows]
	}
	return rows
}

// fileBloatLabel produces the human-readable label for a file row,
// naming the dominant operation so the user sees whether repetition
// is content-loading or iterative edits.
//
// Examples:
//
//	fileBloatLabel("foo.go", 3, 0) -> "Read foo.go (3 reads)"
//	fileBloatLabel("foo.go", 0, 7) -> "Edit foo.go (7 edits)"
//	fileBloatLabel("foo.go", 3, 7) -> "Read+Edit foo.go (3 reads, 7 edits)"
func fileBloatLabel(basename string, readN, editN int) string {
	switch {
	case readN > 0 && editN == 0:
		return "Read " + basename + " (" + plural(readN, "read") + ")"
	case editN > 0 && readN == 0:
		return "Edit " + basename + " (" + plural(editN, "edit") + ")"
	default:
		return "Read+Edit " + basename + " (" +
			plural(readN, "read") + ", " + plural(editN, "edit") + ")"
	}
}

// fileBloatKind picks the BloatKind for a file row. Mixed when the
// file has at least 2 of each operation; otherwise whichever side
// dominates.
func fileBloatKind(readN, editN int) BloatKind {
	switch {
	case readN >= 2 && editN >= 2:
		return BloatKindFileMixed
	case readN >= editN:
		return BloatKindFileRead
	default:
		return BloatKindFileEdit
	}
}

// plural returns "1 word" / "N words" for the count + word noun. Tiny
// helper so labels read naturally without grammar bugs.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// editPayloadBytes returns the approximate context cost of an Edit
// tool call. Sums the lengths of the old_string + new_string fields
// because those are the substrings that actually land in the model's
// prompt when the Edit is recorded. Returns 0 on parse failure.
func editPayloadBytes(input string) int {
	if input == "" {
		return 0
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return 0
	}
	total := 0
	for _, key := range []string{"old_string", "new_string"} {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok {
				total += len(s)
			}
		}
	}
	return total
}

// sortBloatRows orders rows by SharePct desc, label asc on ties. A small
// hand-rolled insertion sort keeps the package free of "sort" import for
// what is always a tiny slice.
func sortBloatRows(rows []BloatRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if a.SharePct > b.SharePct ||
				(a.SharePct == b.SharePct && a.Label <= b.Label) {
				break
			}
			rows[j-1], rows[j] = b, a
		}
	}
}
