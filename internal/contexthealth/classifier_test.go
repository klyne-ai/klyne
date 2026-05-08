package contexthealth

import (
	"strings"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// userMsg builds a minimal user message with the given content. ID and Ts
// are seeded so a slice of these has stable ordering when compared.
func userMsg(idx int, content string) *connectors.Message {
	return &connectors.Message{
		ID:      "u" + itoa(idx),
		Role:    connectors.RoleUser,
		Content: content,
		Ts:      int64(idx) * 1000,
	}
}

// asstMsg builds a minimal assistant message.
func asstMsg(idx int, content string) *connectors.Message {
	return &connectors.Message{
		ID:      "a" + itoa(idx),
		Role:    connectors.RoleAssistant,
		Content: content,
		Ts:      int64(idx) * 1000,
	}
}

// editCallClassifier builds an assistant message that issued an Edit
// tool call against the given path. Used to verify that EDIT
// repetition does NOT trigger the rescue rule the way Read repetition
// does. Named to disambiguate from bloat_test.go's editCall (Go
// package-level naming would clash if both were named the same).
func editCallClassifier(idx int, path string) *connectors.Message {
	return &connectors.Message{
		ID:      "a" + itoa(idx),
		Role:    connectors.RoleAssistant,
		Content: "(editing)",
		Ts:      int64(idx) * 1000,
		ToolCalls: []connectors.ToolCall{{
			ID:    "tc" + itoa(idx),
			Name:  "Edit",
			Input: `{"file_path":"` + path + `","old_string":"a","new_string":"b"}`,
		}},
	}
}

// readCall builds an assistant message that issued a single Read tool
// call against the given path. Used to simulate "same file read N times"
// repetition.
func readCall(idx int, path string) *connectors.Message {
	return &connectors.Message{
		ID:      "a" + itoa(idx),
		Role:    connectors.RoleAssistant,
		Content: "(reading)",
		Ts:      int64(idx) * 1000,
		ToolCalls: []connectors.ToolCall{{
			ID:    "tc" + itoa(idx),
			Name:  "Read",
			Input: `{"file_path":"` + path + `"}`,
		}},
	}
}

// bashCall builds an assistant message that issued a Bash tool call. The
// matching ToolResult (success/failure) is attached as a sibling
// role=tool message; tests use bashFailure / bashSuccess for that.
func bashCall(idx int, cmd string) *connectors.Message {
	return &connectors.Message{
		ID:      "a" + itoa(idx),
		Role:    connectors.RoleAssistant,
		Content: "(running)",
		Ts:      int64(idx) * 1000,
		ToolCalls: []connectors.ToolCall{{
			ID:    "tc" + itoa(idx),
			Name:  "Bash",
			Input: `{"command":"` + cmd + `"}`,
		}},
	}
}

// bashFailure builds the role=tool reply that pairs with the bashCall
// (or readCall) at callIdx and reports a non-success exit. msgIdx is
// used for this row's own message ID and timestamp; callIdx is what
// connects the ToolResult back to the originating ToolCall.
func bashFailure(msgIdx, callIdx int, output string) *connectors.Message {
	return &connectors.Message{
		ID:      "t" + itoa(msgIdx),
		Role:    connectors.RoleTool,
		Content: output,
		Ts:      int64(msgIdx)*1000 + 500,
		ToolResults: []connectors.ToolResult{{
			ID:      "tc" + itoa(callIdx),
			Output:  output,
			IsError: true,
		}},
	}
}

// itoa is a no-import alternative to strconv.Itoa for tiny indices used
// in fixture IDs only — avoids importing strconv just for tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// repeat returns the message slice produced by calling f for i in [0, n).
func repeat(n int, f func(int) *connectors.Message) []*connectors.Message {
	out := make([]*connectors.Message, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, f(i))
	}
	return out
}

// concat joins multiple message slices into one in argument order.
func concat(parts ...[]*connectors.Message) []*connectors.Message {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]*connectors.Message, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestClassify_StateRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		input      Input
		wantState  State
		wantAction Action
	}{
		{
			name: "empty session is healthy",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 0,
				MsgCount:       0,
				Messages:       nil,
			},
			wantState:  StateHealthy,
			wantAction: ActionContinue,
		},
		{
			name: "low fill no shift is healthy",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 12.0,
				MsgCount:       6,
				Messages: []*connectors.Message{
					userMsg(0, "fix the http handler"),
					asstMsg(1, "looking at it"),
					userMsg(2, "also add tests"),
					asstMsg(3, "added"),
				},
			},
			wantState:  StateHealthy,
			wantAction: ActionContinue,
		},
		{
			name: "fill 35% drifts",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 35.0,
				MsgCount:       40,
				Messages: []*connectors.Message{
					userMsg(0, "rewrite the parser"),
					asstMsg(1, "doing it"),
				},
			},
			wantState:  StateDrifting,
			wantAction: ActionContinue,
		},
		{
			name: "fill 60% is risky",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 60.0,
				MsgCount:       80,
				Messages: []*connectors.Message{
					userMsg(0, "still working"),
					asstMsg(1, "ok"),
				},
			},
			wantState:  StateRisky,
			wantAction: ActionCompact,
		},
		{
			name: "fill 80% is rescue",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 80.0,
				MsgCount:       120,
				Messages: []*connectors.Message{
					userMsg(0, "still working"),
					asstMsg(1, "ok"),
				},
			},
			wantState:  StateRescueNow,
			wantAction: ActionStartFresh,
		},
		{
			name: "topic shift at 45% fill is rescue",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 45.0,
				MsgCount:       50,
				Messages: concat(
					// First 5 user prompts: backend topic.
					[]*connectors.Message{
						userMsg(0, "fix the postgres connection pool issue"),
						userMsg(1, "also rewrite the migration runner"),
						userMsg(2, "add an index on sessions.last_msg_at"),
						userMsg(3, "explain why the query is slow"),
						userMsg(4, "rerun the explain analyze query"),
					},
					// Last 5 user prompts: launch / marketing topic.
					[]*connectors.Message{
						userMsg(20, "draft launch tweet announcing release"),
						userMsg(21, "tweak the marketing landing copy"),
						userMsg(22, "write the producthunt headline"),
						userMsg(23, "marketing email subject line"),
						userMsg(24, "design the launch logo concept"),
					},
				),
			},
			wantState:  StateRescueNow,
			wantAction: ActionStartFresh,
		},
		{
			name: "topic shift at 25% fill is drifting",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 25.0,
				MsgCount:       30,
				Messages: concat(
					[]*connectors.Message{
						userMsg(0, "fix the postgres connection pool issue"),
						userMsg(1, "also rewrite the migration runner"),
						userMsg(2, "add an index on sessions.last_msg_at"),
						userMsg(3, "explain why the query is slow"),
						userMsg(4, "rerun the explain analyze query"),
					},
					[]*connectors.Message{
						userMsg(20, "draft launch tweet announcing release"),
						userMsg(21, "tweak the marketing landing copy"),
						userMsg(22, "write the producthunt headline"),
						userMsg(23, "marketing email subject line"),
						userMsg(24, "design the launch logo concept"),
					},
				),
			},
			wantState:  StateDrifting,
			wantAction: ActionContinue,
		},
		{
			name: "same file read 5 times is rescue",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 20.0,
				MsgCount:       40,
				Messages: repeat(5, func(i int) *connectors.Message {
					return readCall(i, "/repo/package-lock.json")
				}),
			},
			wantState:  StateRescueNow,
			wantAction: ActionStartFresh,
		},
		{
			name: "same file read 3 times is risky",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 20.0,
				MsgCount:       40,
				Messages: repeat(3, func(i int) *connectors.Message {
					return readCall(i, "/repo/go.sum")
				}),
			},
			wantState:  StateRisky,
			wantAction: ActionCompact,
		},
		{
			// Real-product fix: an Edit-heavy iterative refactor is
			// progress, not context-loss. Five Edits to the same file
			// at low fill must stay Healthy. Before this fix, Edit
			// counted toward "file read repetition" and falsely
			// triggered rescue on long refactor sessions (verified
			// against the user's trinity/CLI-1252 session on 2026-05-08
			// where review_plan_container.tsx had 16 Edits + 5 Reads).
			name: "same file edited 5 times is NOT rescue (refactor)",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 20.0,
				MsgCount:       40,
				Messages: repeat(5, func(i int) *connectors.Message {
					return editCallClassifier(i, "/repo/auth.go")
				}),
			},
			wantState:  StateHealthy,
			wantAction: ActionContinue,
		},
		{
			name: "same file edited 7 times at mid-fill is drifting (NOT rescue)",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 35.0, // drifting band
				MsgCount:       80,
				Messages: repeat(7, func(i int) *connectors.Message {
					return editCallClassifier(i, "/repo/auth.go")
				}),
			},
			// Drifting comes from the fill, NOT from edit repetition.
			wantState:  StateDrifting,
			wantAction: ActionContinue,
		},
		{
			name: "same command failed 4 times is rescue",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 20.0,
				MsgCount:       40,
				Messages: concat(
					// Pair each call (call_idx = i*2) with its failure
					// (msg_idx = i*2+1, call_idx = i*2) so the tool-result
					// ID round-trips back to the call ID.
					repeat(4, func(i int) *connectors.Message { return bashCall(i*2, "go test ./...") }),
					repeat(4, func(i int) *connectors.Message { return bashFailure(i*2+1, i*2, "FAIL: TestX") }),
				),
			},
			wantState:  StateRescueNow,
			wantAction: ActionStartFresh,
		},
		{
			name: "high hidden ratio is risky",
			input: Input{
				SessionID:      "s",
				CLI:            connectors.CLIClaude,
				ContextFillPct: 20.0,
				MsgCount:       40,
				// 1 assistant + 6 tool messages → ratio 6.0, well above 3.0.
				Messages: concat(
					[]*connectors.Message{asstMsg(0, "ok")},
					repeat(6, func(i int) *connectors.Message { return bashFailure(i+1, i+1, "n/a") }),
				),
			},
			wantState:  StateRisky,
			wantAction: ActionCompact,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Classify(tc.input)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tc.wantAction)
			}
			if strings.TrimSpace(got.Reason) == "" {
				t.Errorf("Reason is empty; want non-empty for every state")
			}
			// Signals.ContextFillPct must round-trip from input.
			if got.Signals.ContextFillPct != tc.input.ContextFillPct {
				t.Errorf("Signals.ContextFillPct = %v, want %v",
					got.Signals.ContextFillPct, tc.input.ContextFillPct)
			}
		})
	}
}

func TestClassify_ResultIsDeterministic(t *testing.T) {
	t.Parallel()
	in := Input{
		SessionID:      "s",
		CLI:            connectors.CLIClaude,
		ContextFillPct: 60.0,
		MsgCount:       80,
		Messages: []*connectors.Message{
			userMsg(0, "still working"),
			asstMsg(1, "ok"),
		},
	}
	a := Classify(in)
	b := Classify(in)
	if a.State != b.State || a.Action != b.Action || a.Reason != b.Reason {
		t.Errorf("Classify is non-deterministic: %+v vs %+v", a, b)
	}
}
