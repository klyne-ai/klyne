package attribution

import (
	"encoding/json"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// makeMsg builds a minimal assistant message with Bash tool calls.
func makeMsg(id string, ts int64, bashCmds ...string) *connectors.Message {
	tcs := make([]connectors.ToolCall, len(bashCmds))
	for i, cmd := range bashCmds {
		b, _ := json.Marshal(cmd)
		tcs[i] = connectors.ToolCall{
			ID:    id + "-tc" + string(rune('0'+i)),
			Name:  "Bash",
			Input: `{"command":` + string(b) + `}`,
		}
	}
	return &connectors.Message{
		ID:        id,
		SessionID: "sess-1",
		Role:      connectors.RoleAssistant,
		Ts:        ts,
		ToolCalls: tcs,
	}
}

// makePlainMsg builds a message with no tool calls (e.g. text-only).
func makePlainMsg(id string, ts int64) *connectors.Message {
	return &connectors.Message{
		ID:        id,
		SessionID: "sess-1",
		Role:      connectors.RoleAssistant,
		Ts:        ts,
	}
}

func TestDetectWasteLoop_NoWaste(t *testing.T) {
	msgs := []*connectors.Message{
		makeMsg("m1", 1, "npm test"),
		makeMsg("m2", 2, "go build ./..."),
		makeMsg("m3", 3, "git status"),
		makeMsg("m4", 4, "ls -la"),
		makeMsg("m5", 5, "echo hello"),
	}
	got := DetectWasteLoop(msgs)
	if len(got) != 0 {
		t.Errorf("expected no waste, got %v", got)
	}
}

func TestDetectWasteLoop_OAuthRetryStorm(t *testing.T) {
	// Simulate the OAuth retry storm from issue #10784: the same Bash call
	// repeating 8 times within a 10-message window.
	const repeatingCmd = "curl https://api.example.com/oauth/token --retry 3"
	msgs := make([]*connectors.Message, 10)
	for i := range msgs {
		msgs[i] = makeMsg("m"+string(rune('0'+i)), int64(i+1), repeatingCmd)
	}
	got := DetectWasteLoop(msgs)
	if len(got) == 0 {
		t.Fatal("expected WASTE_LOOP to fire on 10 identical messages, got none")
	}
	if got[0].Class != WasteLoop {
		t.Errorf("class mismatch: got %q", got[0].Class)
	}
	if got[0].Count <= repeatThreshold {
		t.Errorf("count = %d, want > %d", got[0].Count, repeatThreshold)
	}
}

func TestDetectWasteLoop_JustBelowThreshold(t *testing.T) {
	// 5 repetitions exactly (threshold is > 5) — should NOT fire.
	const cmd = "npm test"
	msgs := []*connectors.Message{
		makeMsg("m1", 1, cmd),
		makeMsg("m2", 2, cmd),
		makeMsg("m3", 3, cmd),
		makeMsg("m4", 4, cmd),
		makeMsg("m5", 5, cmd),
		makeMsg("m6", 6, "git status"),
		makeMsg("m7", 7, "echo done"),
		makeMsg("m8", 8, "ls"),
		makeMsg("m9", 9, "pwd"),
		makeMsg("m10", 10, "cat README.md"),
	}
	got := DetectWasteLoop(msgs)
	if len(got) != 0 {
		t.Errorf("expected no waste at threshold, got %v", got)
	}
}

func TestDetectWasteLoop_ExactlyOverThreshold(t *testing.T) {
	// 6 repetitions within window of 10 — should fire.
	const cmd = "npm test"
	msgs := make([]*connectors.Message, 10)
	for i := range msgs[:6] {
		msgs[i] = makeMsg("m"+string(rune('a'+i)), int64(i+1), cmd)
	}
	msgs[6] = makeMsg("m7", 7, "git status")
	msgs[7] = makeMsg("m8", 8, "echo done")
	msgs[8] = makeMsg("m9", 9, "ls")
	msgs[9] = makeMsg("m10", 10, "pwd")

	got := DetectWasteLoop(msgs)
	if len(got) == 0 {
		t.Fatal("expected WASTE_LOOP to fire with 6 repetitions in 10-window")
	}
	if got[0].Count != 6 {
		t.Errorf("count = %d, want 6", got[0].Count)
	}
}

func TestDetectWasteLoop_PlainMessages(t *testing.T) {
	// Plain (non-tool) messages should never trigger.
	msgs := make([]*connectors.Message, 15)
	for i := range msgs {
		msgs[i] = makePlainMsg("m"+string(rune('a'+i%26)), int64(i))
	}
	got := DetectWasteLoop(msgs)
	if len(got) != 0 {
		t.Errorf("plain messages triggered WASTE_LOOP: %v", got)
	}
}

func TestDetectWasteLoop_NilMessages(t *testing.T) {
	msgs := []*connectors.Message{nil, nil, nil}
	got := DetectWasteLoop(msgs)
	if len(got) != 0 {
		t.Errorf("nil messages triggered WASTE_LOOP: %v", got)
	}
}

func TestDetectWasteLoop_OrderingDoesNotMatter(t *testing.T) {
	// Two tool calls in different order should produce the same fingerprint.
	msg1 := &connectors.Message{
		ID:    "a",
		Ts:    1,
		Role:  connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{ID: "tc1", Name: "Bash", Input: `{"command":"git status"}`},
			{ID: "tc2", Name: "Read", Input: `{"path":"/foo"}`},
		},
	}
	msg2 := &connectors.Message{
		ID:    "b",
		Ts:    2,
		Role:  connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{ID: "tc3", Name: "Read", Input: `{"path":"/foo"}`},
			{ID: "tc4", Name: "Bash", Input: `{"command":"git status"}`},
		},
	}
	fp1 := fingerprintMessage(msg1)
	fp2 := fingerprintMessage(msg2)
	if fp1 != fp2 {
		t.Errorf("fingerprints differ for reordered tool calls:\n  %s\n  %s", fp1, fp2)
	}
}

func TestDetectWasteLoop_NoiseFieldsStripped(t *testing.T) {
	// Two messages that differ only in noise fields (id, timestamp) should
	// hash identically.
	msg1 := &connectors.Message{
		ID:   "x",
		Ts:   1,
		Role: connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{ID: "tc1", Name: "Bash", Input: `{"command":"npm test","id":"req-001","timestamp":1234}`},
		},
	}
	msg2 := &connectors.Message{
		ID:   "y",
		Ts:   2,
		Role: connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{ID: "tc2", Name: "Bash", Input: `{"command":"npm test","id":"req-002","timestamp":9999}`},
		},
	}
	fp1 := fingerprintMessage(msg1)
	fp2 := fingerprintMessage(msg2)
	if fp1 != fp2 {
		t.Errorf("noise fields affected fingerprint:\n  %s\n  %s", fp1, fp2)
	}
}

func TestFingerprintMessage_EmptyToolCalls(t *testing.T) {
	msg := makePlainMsg("x", 1)
	fp := fingerprintMessage(msg)
	if fp != "" {
		t.Errorf("expected empty fingerprint for no tool calls, got %q", fp)
	}
}
