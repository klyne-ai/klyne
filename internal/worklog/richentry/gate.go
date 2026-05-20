package richentry

import (
	"context"
	"encoding/json"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
)

// AIClient is the narrow interface the gate needs from the AI runner —
// a single Chat call matching internal/ai.Provider's shape so production
// can pass an ai.Provider directly and tests can pass a fake. Defined
// locally (not imported from internal/ai) so this package stays
// decoupled from the runner's larger surface area.
type AIClient interface {
	Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error)
}

// HeuristicVerdict is the three-valued result of the cheap heuristic
// pass. Admit + Deny are terminal; Borderline defers to the LLM tiebreak.
type HeuristicVerdict int

const (
	HeuristicBorderline HeuristicVerdict = iota
	HeuristicAdmit
	HeuristicDeny
)

// SessionMetrics holds the per-turn signals the gate reads. Built by
// the caller (Phase 5 worker) from the stop_summaries row plus the
// matching messages/commits the daemon already has. The keyword flags
// are computed by scanning the user-message text once on the way in;
// the gate itself is pure.
type SessionMetrics struct {
	UserCommits        int
	ActiveDuration     time.Duration
	UserMessages       int
	LinesChanged       int // sum of insertions+deletions across user commits in this turn
	NonDocFilesTouched int // count of non-{.md,.txt,docs/} files in commits
	HasDecisionKeyword bool
	HasBugKeyword      bool
	HasBlockerKeyword  bool
}

// HeuristicGate decides admit / deny / borderline from cheap signals
// only — no LLM call. Rules per the plan:
//   - Admit: a commit with real change (>=10 lines OR a non-doc file
//     touched) OR a long discussion with a decision/bug/blocker signal.
//   - Deny: too short to matter OR (no commits + thin conversation +
//     under 5min) OR (doc-only commit under 10 lines — typo fix).
//   - Otherwise borderline.
func HeuristicGate(m SessionMetrics) HeuristicVerdict {
	// Admit path 1: real-change commit.
	if m.UserCommits >= 1 && (m.LinesChanged >= 10 || m.NonDocFilesTouched >= 1) {
		return HeuristicAdmit
	}
	// Admit path 2: long-enough discussion WITH a worklog-worthy keyword.
	if m.ActiveDuration >= 15*time.Minute && m.UserMessages >= 3 &&
		(m.HasDecisionKeyword || m.HasBugKeyword || m.HasBlockerKeyword) {
		return HeuristicAdmit
	}
	// Deny path 1: drive-by short turn.
	if m.ActiveDuration < 3*time.Minute {
		return HeuristicDeny
	}
	// Deny path 2: no real activity at all.
	if m.UserCommits == 0 && m.UserMessages < 3 && m.ActiveDuration < 5*time.Minute {
		return HeuristicDeny
	}
	// Deny path 3: doc-only typo fix (commit happened but nothing of
	// substance). The plan calls this out because "any commit at all"
	// would let typo-fixes flood the worklog.
	if m.UserCommits >= 1 && m.LinesChanged < 10 && m.NonDocFilesTouched == 0 {
		return HeuristicDeny
	}
	return HeuristicBorderline
}

// llmGateReply is the JSON shape the borderline LLM tiebreak emits.
// Kept narrow so the prompt can be tiny and unambiguous; the writer's
// 15-category schema lives elsewhere (Phase 4) and is unrelated.
type llmGateReply struct {
	WorklogWorthy bool `json:"worklog_worthy"`
}

// Decide returns the final gate verdict for one turn:
//   - admitted-heuristic / skipped-heuristic when HeuristicGate is terminal
//   - admitted-llm / skipped-llm when the LLM tiebreak runs on borderline
//
// The gate NEVER returns "pending" — that vocabulary is reserved for the
// Phase 5 worker (queue state). On LLM transient failure or malformed
// reply, the gate defaults to skipped-llm; the worker may re-enqueue
// based on attempts < MAX, but that decision belongs to the worker.
//
// prose is the deterministic Stop-hook summary body — the borderline
// LLM gets it as ground truth so it can judge worthiness from real
// content, not from session metrics alone.
func Decide(ctx context.Context, llm AIClient, m SessionMetrics, prose string) (verdict string, admit bool) {
	switch HeuristicGate(m) {
	case HeuristicAdmit:
		return "admitted-heuristic", true
	case HeuristicDeny:
		return "skipped-heuristic", false
	}
	// Borderline — defer to LLM.
	resp, err := llm.Chat(ctx, ai.ChatRequest{
		Messages: []ai.Message{
			{Role: "user", Content: llmGatePrompt(m, prose)},
		},
		MaxTokens:   32,
		Temperature: 0,
	})
	if err != nil || resp == nil {
		// Transient failure — safe-skip; the worker can retry by
		// resetting the row to 'pending' if attempts < MAX.
		return "skipped-llm", false
	}
	var reply llmGateReply
	if jerr := json.Unmarshal([]byte(resp.Text), &reply); jerr != nil {
		// Malformed reply — same safe-skip default.
		return "skipped-llm", false
	}
	if reply.WorklogWorthy {
		return "admitted-llm", true
	}
	return "skipped-llm", false
}

// llmGatePrompt builds the borderline-judgement prompt. Kept short to
// minimize cost: the LLM only needs to decide a binary, not generate
// the entry. The writer's prompt (Phase 4) is a separate, larger thing.
func llmGatePrompt(m SessionMetrics, prose string) string {
	return `You decide if a development session turn is worth recording in the user's daily worklog.

Reply with EXACTLY this JSON object — no prose, no markdown:
  {"worklog_worthy": true}  // if the turn contains a real feature/bug/decision/blocker worth remembering
  {"worklog_worthy": false} // if it was chit-chat, exploration with no concrete output, or trivially repeated work

Session signals:
  commits:                  ` + intToString(m.UserCommits) + `
  lines_changed:            ` + intToString(m.LinesChanged) + `
  non_doc_files_touched:    ` + intToString(m.NonDocFilesTouched) + `
  active_duration_minutes:  ` + intToString(int(m.ActiveDuration.Minutes())) + `
  user_messages:            ` + intToString(m.UserMessages) + `

Deterministic Stop-hook summary of what happened:
` + prose
}

// intToString avoids the strconv import bloat for one-liner uses.
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	// Tiny stack-allocated path for small ints; the values here are all
	// per-turn counts so they fit easily.
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
