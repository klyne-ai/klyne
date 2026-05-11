// Package eval measures the deterministic context-health classifier
// and the proactive advisor against a labelled dataset of synthetic
// session shapes.
//
// Why a separate package: classifier.go and advisor.go in
// internal/contexthealth are pure functions whose thresholds are
// today heuristic. Before any future PR tunes those thresholds,
// we want a baseline number — accuracy / false-positive / false-
// negative — that a reviewer can rerun. This package supplies that
// runner. It is READ-ONLY against the classifier and the advisor;
// nothing here may modify their behavior.
//
// Fixtures are JSON files under testdata/. Each fixture describes a
// session in compact form (turn count, token totals, cache ratio,
// optional content patterns) plus the expected classification and
// whether an advisor line should fire. The runner synthesises a
// connectors.Message slice from the descriptor, runs Classify and
// RenderAdvisor, and produces a Report with overall accuracy plus
// per-fixture breakdowns.
package eval

import (
	"fmt"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// LabelledFixture is one row in the eval dataset. Compact-by-design:
// the test author specifies the high-level session shape and the
// runner synthesizes a connectors.Message slice that reproduces it.
//
// The fixture JSON schema mirrors this struct one-to-one — see
// testdata/*.json for examples.
type LabelledFixture struct {
	// Name is the fixture identifier. Used in the Report row keys.
	// Required and unique within a dataset.
	Name string `json:"name"`

	// Description is a one-line explanation of why this fixture
	// exists (clearly-healthy, edge-case-near-warning, etc.).
	// Informational only.
	Description string `json:"description"`

	// Shape describes the synthetic session: how many turns, how
	// many tokens, what tool-call patterns to seed.
	Shape SessionShape `json:"shape"`

	// ExpectedState is the classification the classifier SHOULD
	// produce. One of "healthy" | "drifting" | "risky" | "rescue_now".
	ExpectedState contexthealth.State `json:"expected_state"`

	// ExpectedAdvisorFires is true when at least one advisor
	// trigger should fire for this fixture. When false, the
	// advisor should stay silent.
	ExpectedAdvisorFires bool `json:"expected_advisor_fires"`
}

// SessionShape is a compact, human-writable description of the
// synthetic session the runner will reconstruct. Every field has a
// safe zero-value default so a minimal fixture only needs to set
// the dimensions it cares about.
type SessionShape struct {
	// ContextFillPct is the input's ContextFillPct (0..100). This
	// drives the dominant classifier rule.
	ContextFillPct float64 `json:"context_fill_pct"`

	// AssistantTurns is the number of assistant messages to
	// synthesise. Each gets TokensIn and CachedReadTokens computed
	// from TokensInPerTurn and CacheReadRatio.
	AssistantTurns int `json:"assistant_turns"`

	// UserTurns is the number of user messages to synthesise. The
	// first half use OpeningPrompt; the second half use ClosingPrompt
	// (so the topic-shift detector sees the divergence when those
	// two strings have low overlap). When UserTurns is 0 a small
	// default is used so the advisor input is plausible.
	UserTurns int `json:"user_turns"`

	// HiddenPerAssistant is the number of tool/system messages
	// inserted between assistant turns. Drives the hidden-ratio
	// signal used by the "risky: hidden ratio" rule.
	HiddenPerAssistant int `json:"hidden_per_assistant"`

	// TokensInPerTurn is the per-assistant-message TokensIn value.
	// Combined with CacheReadRatio it produces the effective-input
	// the acceleration detector reads.
	TokensInPerTurn int64 `json:"tokens_in_per_turn"`

	// AccelLatestTokensIn, when non-zero, overrides TokensInPerTurn
	// for the LAST assistant message only. Lets a fixture set up
	// an acceleration scenario (small baseline, big latest turn)
	// without dragging the prior mean above the gating floor.
	AccelLatestTokensIn int64 `json:"accel_latest_tokens_in"`

	// CacheReadRatio is the fraction of TokensIn served from cache
	// (0..1). Effective input = TokensIn * (1 - CacheReadRatio).
	CacheReadRatio float64 `json:"cache_read_ratio"`

	// RepeatedReadPath, when non-empty, seeds RepeatedReadCount
	// Read tool calls on this path AT THE END of the synthesised
	// message slice so the calls land in the classifier's last-20
	// repetition window. Use this to exercise the rescue / risky
	// "same file re-read N times" rules.
	RepeatedReadPath  string `json:"repeated_read_path"`
	RepeatedReadCount int    `json:"repeated_read_count"`

	// StaleReadPath, when non-empty, seeds StaleReadCount Read tool
	// calls on this path BEFORE the closing user prompts so the file
	// is touched "in the past" — outside the advisor's recency
	// horizon (the last 10 user messages) — and can therefore be
	// classified stale once the user's vocabulary diverges. Use this
	// for fixtures that target the stale / topic_shift advisor
	// triggers rather than the classifier rescue rule.
	StaleReadPath  string `json:"stale_read_path"`
	StaleReadCount int    `json:"stale_read_count"`

	// RepeatedFailedCommand, when non-empty, seeds
	// RepeatedFailedCommandCount Bash calls + matching failing
	// tool-result rows so the "command failed N times" rescue rule
	// can be exercised.
	RepeatedFailedCommand      string `json:"repeated_failed_command"`
	RepeatedFailedCommandCount int    `json:"repeated_failed_command_count"`

	// OpeningPrompt is the user message text used for the first
	// half of UserTurns. ClosingPrompt is used for the second half.
	// When ClosingPrompt has low bag-of-words overlap with
	// OpeningPrompt, the topic-shift detector fires.
	OpeningPrompt string `json:"opening_prompt"`
	ClosingPrompt string `json:"closing_prompt"`

	// LoadedFileBytes, when > 0, attaches a synthetic tool_result
	// payload of this many bytes to each Read call so the advisor's
	// relevance detector sees a non-trivial "loaded file context"
	// volume. Without this, RelevanceVerdict.TotalBytes stays below
	// the gating threshold and the stale/topic-shift advisors never
	// fire.
	LoadedFileBytes int `json:"loaded_file_bytes"`

	// FiveHourPctUsed, when > 0, configures the FiveHourSummary
	// the advisor sees so the rate-limit triggers can be exercised
	// without depending on cross-session aggregation.
	FiveHourPctUsed float64 `json:"five_hour_pct_used"`
}

// defaultUserTurns is the number of user messages synthesised when
// SessionShape.UserTurns is left at zero. Chosen large enough that
// the topic-shift detector has data on both sides (it needs at least
// 2 * topicSampleSize = 10 user messages) without overwhelming the
// runner.
const defaultUserTurns = 12

// synthesiseMessages turns a SessionShape into a chronological
// connectors.Message slice the classifier and advisor can both
// consume. Pure function — no randomness, no time-of-day dependence.
//
// Layout (in chronological order):
//
//  1. Opening half of UserTurns (using OpeningPrompt content).
//  2. The StaleRead sequence — Reads on StaleReadPath that happen
//     BEFORE the closing prompts so the file is "in the past"
//     relative to the advisor's recency horizon and can be flagged
//     stale once the closing-prompt vocabulary differs.
//  3. Closing half of UserTurns (using ClosingPrompt content). Topic
//     shift fires when this content has low bag-of-words overlap
//     with OpeningPrompt.
//  4. Assistant turns interleaved with hidden tool/system filler.
//  5. The RepeatedRead sequence — Reads on RepeatedReadPath placed
//     at the END so they land in the classifier's last-20
//     repetition window (the rescue / risky file-read rules).
//  6. The RepeatedFailedCommand sequence — at the end for the same
//     reason.
//
// All synthesised messages get a monotonically increasing Ts so the
// classifier's order-sensitive code paths (acceleration, tail
// windows) see a realistic stream.
func synthesiseMessages(shape SessionShape) []*connectors.Message {
	out := make([]*connectors.Message, 0)
	add := func(m *connectors.Message) {
		m.Ts = int64(len(out)+1) * 1000
		out = append(out, m)
	}

	userN := shape.UserTurns
	if userN == 0 {
		userN = defaultUserTurns
	}
	opening := shape.OpeningPrompt
	if opening == "" {
		opening = "refactor auth module split helper functions improve coverage"
	}
	closing := shape.ClosingPrompt
	if closing == "" {
		closing = opening
	}
	half := userN / 2
	if half < 1 {
		half = 1
	}

	// 1. Opening user messages.
	for i := 0; i < half; i++ {
		add(&connectors.Message{
			ID:      fmt.Sprintf("u-open-%d", i),
			Role:    connectors.RoleUser,
			Content: opening,
		})
	}

	// 2. StaleRead sequence — happens before the closing prompts so
	// it sits outside the advisor's recency horizon. Each Read is
	// paired with a tool_result carrying LoadedFileBytes bytes so
	// the relevance detector sees a non-trivial loaded volume.
	if shape.StaleReadPath != "" && shape.StaleReadCount > 0 {
		payload := makePayload(shape.LoadedFileBytes)
		for i := 0; i < shape.StaleReadCount; i++ {
			callID := fmt.Sprintf("s%d", i)
			add(&connectors.Message{
				ID:      fmt.Sprintf("sa%d", i),
				Role:    connectors.RoleAssistant,
				Content: "(reading-stale)",
				ToolCalls: []connectors.ToolCall{{
					ID:    callID,
					Name:  "Read",
					Input: fmt.Sprintf(`{"file_path":%q}`, shape.StaleReadPath),
				}},
			})
			add(&connectors.Message{
				ID:      fmt.Sprintf("st%d", i),
				Role:    connectors.RoleTool,
				Content: payload,
				ToolResults: []connectors.ToolResult{{
					ID:     callID,
					Output: payload,
				}},
			})
		}
	}

	// 3. Closing user messages.
	for i := half; i < userN; i++ {
		add(&connectors.Message{
			ID:      fmt.Sprintf("u-close-%d", i),
			Role:    connectors.RoleUser,
			Content: closing,
		})
	}

	// 4. Assistant turns + hidden filler.
	for i := 0; i < shape.AssistantTurns; i++ {
		tokensIn := shape.TokensInPerTurn
		if i == shape.AssistantTurns-1 && shape.AccelLatestTokensIn > 0 {
			tokensIn = shape.AccelLatestTokensIn
		}
		cached := int64(float64(tokensIn) * shape.CacheReadRatio)
		add(&connectors.Message{
			ID:               fmt.Sprintf("a%d", i),
			Role:             connectors.RoleAssistant,
			Content:          "(reasoning)",
			TokensIn:         tokensIn,
			CachedReadTokens: cached,
		})
		for j := 0; j < shape.HiddenPerAssistant; j++ {
			add(&connectors.Message{
				ID:      fmt.Sprintf("h%d-%d", i, j),
				Role:    connectors.RoleTool,
				Content: "(tool output)",
			})
		}
	}

	// 5. RepeatedRead sequence — placed at the END so it lands in
	// the classifier's last-20 repetition window. Each Read is
	// paired with a tool_result carrying LoadedFileBytes bytes so
	// the relevance detector also sees the loaded volume.
	if shape.RepeatedReadPath != "" && shape.RepeatedReadCount > 0 {
		payload := makePayload(shape.LoadedFileBytes)
		for i := 0; i < shape.RepeatedReadCount; i++ {
			callID := fmt.Sprintf("r%d", i)
			add(&connectors.Message{
				ID:      fmt.Sprintf("ra%d", i),
				Role:    connectors.RoleAssistant,
				Content: "(reading)",
				ToolCalls: []connectors.ToolCall{{
					ID:    callID,
					Name:  "Read",
					Input: fmt.Sprintf(`{"file_path":%q}`, shape.RepeatedReadPath),
				}},
			})
			add(&connectors.Message{
				ID:      fmt.Sprintf("rt%d", i),
				Role:    connectors.RoleTool,
				Content: payload,
				ToolResults: []connectors.ToolResult{{
					ID:     callID,
					Output: payload,
				}},
			})
		}
	}

	// 6. RepeatedFailedCommand sequence — also at the end.
	if shape.RepeatedFailedCommand != "" && shape.RepeatedFailedCommandCount > 0 {
		for i := 0; i < shape.RepeatedFailedCommandCount; i++ {
			callID := fmt.Sprintf("c%d", i)
			add(&connectors.Message{
				ID:      fmt.Sprintf("ca%d", i),
				Role:    connectors.RoleAssistant,
				Content: "(running)",
				ToolCalls: []connectors.ToolCall{{
					ID:    callID,
					Name:  "Bash",
					Input: fmt.Sprintf(`{"command":%q}`, shape.RepeatedFailedCommand),
				}},
			})
			add(&connectors.Message{
				ID:      fmt.Sprintf("ct%d", i),
				Role:    connectors.RoleTool,
				Content: "command failed",
				ToolResults: []connectors.ToolResult{{
					ID:      callID,
					Output:  "boom",
					IsError: true,
				}},
			})
		}
	}

	return out
}

// makePayload returns a deterministic byte-blob of the requested
// size, used as a synthetic Read tool_result body. The content
// itself is irrelevant — only its byte length matters for the
// advisor's stale-share calculation.
func makePayload(size int) string {
	if size <= 0 {
		return ""
	}
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = 'x'
	}
	return string(buf)
}

// buildInput builds the classifier Input + advisor AdvisorInput pair
// for one fixture. Centralised so the runner and the explain mode
// produce identical synthetic shapes from the same descriptor.
func buildInput(fx LabelledFixture) (contexthealth.Input, contexthealth.AdvisorInput) {
	msgs := synthesiseMessages(fx.Shape)
	id := "eval-" + fx.Name
	clsIn := contexthealth.Input{
		SessionID:      id,
		CLI:            connectors.CLIClaude,
		ContextFillPct: fx.Shape.ContextFillPct,
		MsgCount:       len(msgs),
		Messages:       msgs,
	}
	advIn := contexthealth.AdvisorInput{
		SessionID:      id,
		Messages:       msgs,
		ContextFillPct: fx.Shape.ContextFillPct,
		FiveHour:       buildFiveHour(fx.Shape.FiveHourPctUsed),
		State:          contexthealth.AdvisorState{},
		NowMs:          1,
	}
	return clsIn, advIn
}

// buildFiveHour builds a FiveHourSummary that matches the requested
// PctUsed. When pct is zero, the summary has Cap=0 so the rate-limit
// triggers stay silent (matching production for un-configured users).
func buildFiveHour(pct float64) contexthealth.FiveHourSummary {
	if pct <= 0 {
		return contexthealth.FiveHourSummary{}
	}
	const cap int64 = 1_000_000
	return contexthealth.FiveHourSummary{
		TotalEffective: int64(pct / 100 * float64(cap)),
		Cap:            cap,
		PctUsed:        pct,
	}
}
