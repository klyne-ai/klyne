package codex

import (
	"bufio"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// fakePath is the default path used in parse tests.
const fakePath = "/home/dev/.codex/sessions/2026/05/06/rollout-abc.jsonl"

// realFixtureFilePath returns the absolute path to the sanitized real-format fixture.
// go test sets the working directory to the package directory
// (.../agentdeck/internal/connectors/codex), so 3 levels up reaches the repo root.
func realFixtureFilePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get working directory: %v", err)
	}
	// wd = .../agentdeck/internal/connectors/codex
	// repo root = 3 levels up
	p := filepath.Join(wd, "..", "..", "..", "examples", "sample-jsonl", "codex", "session-real-001.jsonl")
	return filepath.Clean(p)
}

// newMeta returns a fresh fileMeta and a mutex for tests.
func newMeta() (*fileMeta, *sync.Mutex) {
	return &fileMeta{}, &sync.Mutex{}
}

// newConnectorForTest builds a Connector with a fresh state map for use in
// tests that call c.Parse directly.
func newConnectorForTest() *Connector {
	return New("/tmp/test-root")
}

// ---------------------------------------------------------------------------
// 1. session_meta updates state and returns (nil, nil)
// ---------------------------------------------------------------------------

func TestParse_SessionMeta_UpdatesState(t *testing.T) {
	meta, mu := newMeta()
	line := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"test-session-id","cwd":"/Users/dev/myproject"}}`)
	msg, err := parseLine(line, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil message for session_meta, got %+v", msg)
	}
	if meta.sessionID != "test-session-id" {
		t.Errorf("sessionID: got %q, want test-session-id", meta.sessionID)
	}
	if meta.projectPath != "/Users/dev/myproject" {
		t.Errorf("projectPath: got %q, want /Users/dev/myproject", meta.projectPath)
	}
}

// ---------------------------------------------------------------------------
// 2. turn_context updates model
// ---------------------------------------------------------------------------

func TestParse_TurnContext_UpdatesModel(t *testing.T) {
	meta, mu := newMeta()
	// Prime with session_meta first.
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sid-abc","cwd":"/Users/dev/proj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta parse: %v", err)
	}

	contextLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5","cwd":"/Users/dev/proj"}}`)
	msg, err := parseLine(contextLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("turn_context parse: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil message for turn_context, got %+v", msg)
	}
	if meta.model != "gpt-5" {
		t.Errorf("model: got %q, want gpt-5", meta.model)
	}
	// Ensure session_meta's cwd was not overwritten.
	if meta.projectPath != "/Users/dev/proj" {
		t.Errorf("projectPath should remain from session_meta, got %q", meta.projectPath)
	}
}

// ---------------------------------------------------------------------------
// 3. User message emits correct Message
// ---------------------------------------------------------------------------

func TestParse_UserMessage(t *testing.T) {
	meta, mu := newMeta()
	// Set state via session_meta then turn_context.
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-user-01","cwd":"/Users/dev/userproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	ctxLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5-mini","cwd":"/Users/dev/userproj"}}`)
	if _, err := parseLine(ctxLine, fakePath, meta, mu); err != nil {
		t.Fatalf("turn_context: %v", err)
	}

	userLine := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello, fix the bug"}]}}`)
	msg, err := parseLine(userLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("user message: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message for user message")
	}
	if msg.Role != connectors.RoleUser {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleUser)
	}
	if msg.Content != "Hello, fix the bug" {
		t.Errorf("content: got %q, want %q", msg.Content, "Hello, fix the bug")
	}
	if msg.SessionID != "sess-user-01" {
		t.Errorf("session_id: got %q, want sess-user-01", msg.SessionID)
	}
	if msg.ProjectPath != "/Users/dev/userproj" {
		t.Errorf("project_path: got %q, want /Users/dev/userproj", msg.ProjectPath)
	}
	if msg.Model != "gpt-5-mini" {
		t.Errorf("model: got %q, want gpt-5-mini", msg.Model)
	}
	if msg.CLI != connectors.CLICodex {
		t.Errorf("cli: got %q, want %q", msg.CLI, connectors.CLICodex)
	}
	if msg.Ts == 0 {
		t.Error("ts should be non-zero")
	}
	if msg.ID == "" {
		t.Error("id should be non-empty")
	}
}

// ---------------------------------------------------------------------------
// 4. Assistant message (output_text)
// ---------------------------------------------------------------------------

func TestParse_AssistantMessage(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-asst-01","cwd":"/Users/dev/asstproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	ctxLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5","cwd":"/Users/dev/asstproj"}}`)
	if _, err := parseLine(ctxLine, fakePath, meta, mu); err != nil {
		t.Fatalf("turn_context: %v", err)
	}

	asstLine := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I found the bug on line 42."}]}}`)
	msg, err := parseLine(asstLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("assistant message: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleAssistant)
	}
	if msg.Content != "I found the bug on line 42." {
		t.Errorf("content: got %q", msg.Content)
	}
	if msg.Model != "gpt-5" {
		t.Errorf("model: got %q, want gpt-5", msg.Model)
	}
}

// ---------------------------------------------------------------------------
// 5. function_call emits ToolCalls
// ---------------------------------------------------------------------------

func TestParse_FunctionCall_Emits(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-fc-01","cwd":"/Users/dev/fcproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	fcLine := []byte(`{"timestamp":"2026-05-06T10:00:03.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls -la\"}","call_id":"call_abc123"}}`)
	msg, err := parseLine(fcLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("function_call: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message for function_call")
	}
	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleAssistant)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len: got %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_abc123" {
		t.Errorf("tool call id: got %q, want call_abc123", tc.ID)
	}
	if tc.Name != "exec_command" {
		t.Errorf("tool call name: got %q, want exec_command", tc.Name)
	}
	if tc.Input != `{"cmd":"ls -la"}` {
		t.Errorf("tool call input: got %q", tc.Input)
	}
}

// ---------------------------------------------------------------------------
// 6. function_call_output emits ToolResults
// ---------------------------------------------------------------------------

func TestParse_FunctionCallOutput_Emits(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-fco-01","cwd":"/Users/dev/fcoproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	fcoLine := []byte(`{"timestamp":"2026-05-06T10:00:04.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_abc123","output":"total 0\ndrwxr-xr-x 2 dev staff 64 May 6 10:00 ."}}`)
	msg, err := parseLine(fcoLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("function_call_output: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message for function_call_output")
	}
	if msg.Role != connectors.RoleTool {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleTool)
	}
	if len(msg.ToolResults) != 1 {
		t.Fatalf("tool_results len: got %d, want 1", len(msg.ToolResults))
	}
	tr := msg.ToolResults[0]
	if tr.ID != "call_abc123" {
		t.Errorf("tool_result id: got %q, want call_abc123", tr.ID)
	}
	if tr.Output == "" {
		t.Error("tool_result output should not be empty")
	}
	if msg.Content != tr.Output {
		t.Errorf("message content should match tool_result output")
	}
}

// ---------------------------------------------------------------------------
// 7. reasoning is skipped
// ---------------------------------------------------------------------------

func TestParse_Reasoning_Skipped(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-r-01","cwd":"/Users/dev/rproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	reasoningLine := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"response_item","payload":{"type":"reasoning","summary":[],"content":null,"encrypted_content":"REDACTED"}}`)
	msg, err := parseLine(reasoningLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error for reasoning: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for reasoning, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// 8. event_msg non-token_count subtypes are still skipped.
//    token_count with null/absent info is also skipped (no useful data).
//    token_count with real data is tested separately below.
// ---------------------------------------------------------------------------

func TestParse_EventMsg_Skipped(t *testing.T) {
	meta, mu := newMeta()
	// token_count with info:null — still skipped because cumIn==cumOut==0.
	subtypes := []string{
		`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
		`{"timestamp":"2026-05-06T10:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hello"}}`,
		`{"timestamp":"2026-05-06T10:00:03.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-05-06T10:00:04.000Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-05-06T10:00:05.000Z","type":"event_msg","payload":{"type":"turn_aborted"}}`,
		`{"timestamp":"2026-05-06T10:00:06.000Z","type":"event_msg","payload":{"type":"error","message":"something failed"}}`,
	}
	for _, raw := range subtypes {
		msg, err := parseLine([]byte(raw), fakePath, meta, mu)
		if err != nil {
			t.Errorf("event_msg should not error: %v", err)
		}
		if msg != nil {
			t.Errorf("event_msg should return nil, got %+v (line: %s)", msg, raw[:60])
		}
	}
}

// ---------------------------------------------------------------------------
// 13. token_count emits a delta system message
// ---------------------------------------------------------------------------

func TestParse_TokenCount_Emits_DeltaSystemMessage(t *testing.T) {
	meta, mu := newMeta()
	// Prime with session_meta so we have a session context.
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-tc-01","cwd":"/Users/dev/tcproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	tcLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"model_context_window":272000}}}`)
	msg, err := parseLine(tcLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("token_count: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message for token_count")
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleSystem)
	}
	if msg.TokensIn != 100 {
		t.Errorf("TokensIn: got %d, want 100", msg.TokensIn)
	}
	if msg.TokensOut != 20 {
		t.Errorf("TokensOut: got %d, want 20", msg.TokensOut)
	}
	if msg.Content != "" {
		t.Errorf("Content: expected empty string, got %q", msg.Content)
	}
	if msg.SessionID != "sess-tc-01" {
		t.Errorf("SessionID: got %q, want sess-tc-01", msg.SessionID)
	}
	if msg.CLI != connectors.CLICodex {
		t.Errorf("CLI: got %q, want %q", msg.CLI, connectors.CLICodex)
	}
	if msg.ID == "" {
		t.Error("ID should be non-empty")
	}
}

// ---------------------------------------------------------------------------
// 14. token_count computes deltas across consecutive events
// ---------------------------------------------------------------------------

func TestParse_TokenCount_DeltasAcrossEvents(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-tc-02","cwd":"/Users/dev/tcproj2"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	// First event: cumulative in=100, out=20 → delta = 100, 20.
	tc1 := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"model_context_window":272000}}}`)
	msg1, err := parseLine(tc1, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("tc1: %v", err)
	}
	if msg1 == nil {
		t.Fatal("expected non-nil message for tc1")
	}
	if msg1.TokensIn != 100 {
		t.Errorf("tc1 TokensIn: got %d, want 100", msg1.TokensIn)
	}
	if msg1.TokensOut != 20 {
		t.Errorf("tc1 TokensOut: got %d, want 20", msg1.TokensOut)
	}

	// Second event: cumulative in=300, out=80 → delta = 200, 60.
	tc2 := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":0,"output_tokens":80,"reasoning_output_tokens":0,"total_tokens":380},"last_token_usage":{"input_tokens":300,"cached_input_tokens":0,"output_tokens":80,"reasoning_output_tokens":0,"total_tokens":380},"model_context_window":272000}}}`)
	msg2, err := parseLine(tc2, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("tc2: %v", err)
	}
	if msg2 == nil {
		t.Fatal("expected non-nil message for tc2")
	}
	if msg2.TokensIn != 200 {
		t.Errorf("tc2 TokensIn: got %d, want 200", msg2.TokensIn)
	}
	if msg2.TokensOut != 60 {
		t.Errorf("tc2 TokensOut: got %d, want 60", msg2.TokensOut)
	}
}

// ---------------------------------------------------------------------------
// 15. negative delta is clamped to zero
// ---------------------------------------------------------------------------

func TestParse_TokenCount_NegativeDelta_ClampedToZero(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-tc-03","cwd":"/Users/dev/tcproj3"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}

	// First event: cumulative in=100, out=20.
	tc1 := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"model_context_window":272000}}}`)
	if _, err := parseLine(tc1, fakePath, meta, mu); err != nil {
		t.Fatalf("tc1: %v", err)
	}

	// Second event: cumulative in=50 (lower than previous) → delta clamped to 0.
	tc2 := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":70},"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":70},"model_context_window":272000}}}`)
	msg2, err := parseLine(tc2, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("tc2: %v", err)
	}
	// deltaIn = 50 - 100 = -50 → clamped to 0; deltaOut = 20 - 20 = 0.
	// Both deltas are 0 → no message emitted.
	if msg2 != nil {
		t.Errorf("expected nil (both deltas zero after clamp), got TokensIn=%d TokensOut=%d", msg2.TokensIn, msg2.TokensOut)
	}
}

// ---------------------------------------------------------------------------
// 16. token_count before session_meta is skipped
// ---------------------------------------------------------------------------

func TestParse_TokenCount_BeforeSessionMeta_Skipped(t *testing.T) {
	meta, mu := newMeta() // no session_meta
	tcLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"model_context_window":272000}}}`)
	msg, err := parseLine(tcLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil when token_count arrives before session_meta, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// 17. token_count per-file state isolation — two paths track deltas separately
// ---------------------------------------------------------------------------

func TestParse_TokenCount_PerFileStateIsolation(t *testing.T) {
	c := newConnectorForTest()

	pathA := "/home/dev/.codex/sessions/2026/05/06/rollout-tcA.jsonl"
	pathB := "/home/dev/.codex/sessions/2026/05/06/rollout-tcB.jsonl"

	// Prime both paths with session_meta.
	metaA := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-tc-A","cwd":"/Users/dev/projA"}}`)
	metaB := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-tc-B","cwd":"/Users/dev/projB"}}`)
	if _, err := c.Parse(metaA, pathA); err != nil {
		t.Fatalf("pathA session_meta: %v", err)
	}
	if _, err := c.Parse(metaB, pathB); err != nil {
		t.Fatalf("pathB session_meta: %v", err)
	}

	// Emit first token_count on pathA: cumIn=100.
	tcA1 := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110},"model_context_window":272000}}}`)
	msgA1, err := c.Parse(tcA1, pathA)
	if err != nil {
		t.Fatalf("pathA tc1: %v", err)
	}
	if msgA1 == nil {
		t.Fatal("expected non-nil message for pathA tc1")
	}

	// Emit first token_count on pathB: cumIn=200 — pathB's prev should be 0.
	tcB1 := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":200,"cached_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":0,"total_tokens":230},"last_token_usage":{"input_tokens":200,"cached_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":0,"total_tokens":230},"model_context_window":272000}}}`)
	msgB1, err := c.Parse(tcB1, pathB)
	if err != nil {
		t.Fatalf("pathB tc1: %v", err)
	}
	if msgB1 == nil {
		t.Fatal("expected non-nil message for pathB tc1")
	}

	// pathA delta should be 100 (not 200), pathB delta should be 200 (not 100).
	if msgA1.TokensIn != 100 {
		t.Errorf("pathA TokensIn: got %d, want 100", msgA1.TokensIn)
	}
	if msgB1.TokensIn != 200 {
		t.Errorf("pathB TokensIn: got %d, want 200", msgB1.TokensIn)
	}

	// Second event on pathA: cumIn=150 → delta 50.
	tcA2 := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":0,"output_tokens":15,"reasoning_output_tokens":0,"total_tokens":165},"last_token_usage":{"input_tokens":150,"cached_input_tokens":0,"output_tokens":15,"reasoning_output_tokens":0,"total_tokens":165},"model_context_window":272000}}}`)
	// Note: 150 < 200 (pathB's prev), so pathB state must NOT be used for pathA.
	msgA2, err := c.Parse(tcA2, pathA)
	if err != nil {
		t.Fatalf("pathA tc2: %v", err)
	}
	if msgA2 == nil {
		t.Fatal("expected non-nil message for pathA tc2")
	}
	if msgA2.TokensIn != 50 {
		t.Errorf("pathA tc2 TokensIn: got %d, want 50 (delta from 100→150)", msgA2.TokensIn)
	}
}

// ---------------------------------------------------------------------------
// 18. parse real fixture end-to-end: sum token deltas > 0
// ---------------------------------------------------------------------------

func TestParse_RealFixture_ProducesNonZeroSessionTotals(t *testing.T) {
	fixturePath := realFixtureFilePath(t)
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("cannot open real fixture %s: %v", fixturePath, err)
	}
	defer f.Close()

	meta, mu := newMeta()
	scanner := bufio.NewScanner(f)

	var totalIn, totalOut int64
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		lineCopy := make([]byte, len(raw))
		copy(lineCopy, raw)

		msg, err := parseLine(lineCopy, fixturePath, meta, mu)
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", lineNum, err)
			continue
		}
		if msg != nil {
			totalIn += msg.TokensIn
			totalOut += msg.TokensOut
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	t.Logf("fixture token totals: in=%d out=%d (across %d lines)", totalIn, totalOut, lineNum)

	if totalIn == 0 {
		t.Error("expected totalIn > 0 after parsing real fixture")
	}
	if totalOut == 0 {
		t.Error("expected totalOut > 0 after parsing real fixture")
	}
}

// ---------------------------------------------------------------------------
// 9. Message before session_meta returns (nil, nil) without panic
// ---------------------------------------------------------------------------

func TestParse_MessageBeforeSessionMeta_Skipped(t *testing.T) {
	meta, mu := newMeta() // fresh meta — no session_meta seen yet
	// Attempt to parse an assistant message before any session_meta.
	asstLine := []byte(`{"timestamp":"2026-05-06T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Early response"}]}}`)
	msg, err := parseLine(asstLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for message before session_meta, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// 10. Malformed JSON returns error
// ---------------------------------------------------------------------------

func TestParse_MalformedLine_Returns_Error(t *testing.T) {
	meta, mu := newMeta()
	line := []byte(`this is not {{{ json`)
	msg, err := parseLine(line, fakePath, meta, mu)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if msg != nil {
		t.Fatal("expected nil message for malformed JSON")
	}
}

// ---------------------------------------------------------------------------
// 11. Real fixture file: parse all lines, collect messages, assert invariants
// ---------------------------------------------------------------------------

func TestParse_RealFixtureFile(t *testing.T) {
	fixturePath := realFixtureFilePath(t)
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("cannot open real fixture %s: %v", fixturePath, err)
	}
	defer f.Close()

	meta, mu := newMeta()
	scanner := bufio.NewScanner(f)

	var messages []*connectors.Message
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		lineCopy := make([]byte, len(raw))
		copy(lineCopy, raw)

		msg, err := parseLine(lineCopy, fixturePath, meta, mu)
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", lineNum, err)
			continue
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	t.Logf("parsed %d messages from real fixture (total lines: %d)", len(messages), lineNum)

	// Require at least 1 user, 1 assistant, 1 tool call, 1 tool result.
	var userCount, asstCount, toolCallCount, toolResultCount int
	for _, m := range messages {
		switch m.Role {
		case connectors.RoleUser:
			userCount++
		case connectors.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				toolCallCount++
			} else {
				asstCount++
			}
		case connectors.RoleTool:
			toolResultCount++
		}
		// Every message must have SessionID, ProjectPath, CLI populated.
		if m.SessionID == "" {
			t.Errorf("message missing SessionID: %+v", m)
		}
		if m.ProjectPath == "" {
			t.Errorf("message missing ProjectPath: %+v", m)
		}
		if m.CLI != connectors.CLICodex {
			t.Errorf("message CLI: got %q, want %q", m.CLI, connectors.CLICodex)
		}
	}

	if userCount < 1 {
		t.Errorf("expected at least 1 user message, got %d", userCount)
	}
	if asstCount < 1 {
		t.Errorf("expected at least 1 assistant message, got %d", asstCount)
	}
	if toolCallCount < 1 {
		t.Errorf("expected at least 1 function_call message, got %d", toolCallCount)
	}
	if toolResultCount < 1 {
		t.Errorf("expected at least 1 function_call_output message, got %d", toolResultCount)
	}

	// SessionID should be consistent across all messages.
	if len(messages) > 1 {
		firstSID := messages[0].SessionID
		for i, m := range messages[1:] {
			if m.SessionID != firstSID {
				t.Errorf("message[%d] SessionID %q != first message SessionID %q", i+1, m.SessionID, firstSID)
			}
		}
	}

	t.Logf("message breakdown: user=%d assistant=%d tool_calls=%d tool_results=%d total=%d",
		userCount, asstCount, toolCallCount, toolResultCount, len(messages))
}

// ---------------------------------------------------------------------------
// 12. Per-file state isolation: two paths don't share state
// ---------------------------------------------------------------------------

func TestParse_PerFileStateIsolation(t *testing.T) {
	c := newConnectorForTest()

	pathA := "/home/dev/.codex/sessions/2026/05/06/rollout-fileA.jsonl"
	pathB := "/home/dev/.codex/sessions/2026/05/06/rollout-fileB.jsonl"

	sessionMetaA := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"session-AAA","cwd":"/Users/dev/projectA"}}`)
	sessionMetaB := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"session-BBB","cwd":"/Users/dev/projectB"}}`)

	// Parse session_meta for each path.
	if _, err := c.Parse(sessionMetaA, pathA); err != nil {
		t.Fatalf("pathA session_meta: %v", err)
	}
	if _, err := c.Parse(sessionMetaB, pathB); err != nil {
		t.Fatalf("pathB session_meta: %v", err)
	}

	// Now parse a user message on each path and verify isolation.
	userMsgA := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Message for project A"}]}}`)
	userMsgB := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Message for project B"}]}}`)

	msgA, err := c.Parse(userMsgA, pathA)
	if err != nil {
		t.Fatalf("pathA user message: %v", err)
	}
	msgB, err := c.Parse(userMsgB, pathB)
	if err != nil {
		t.Fatalf("pathB user message: %v", err)
	}

	if msgA == nil {
		t.Fatal("expected non-nil message for pathA")
	}
	if msgB == nil {
		t.Fatal("expected non-nil message for pathB")
	}

	// SessionID must differ.
	if msgA.SessionID == msgB.SessionID {
		t.Errorf("state leaked: both paths got same SessionID %q", msgA.SessionID)
	}
	if msgA.SessionID != "session-AAA" {
		t.Errorf("pathA SessionID: got %q, want session-AAA", msgA.SessionID)
	}
	if msgB.SessionID != "session-BBB" {
		t.Errorf("pathB SessionID: got %q, want session-BBB", msgB.SessionID)
	}

	// ProjectPath must differ.
	if msgA.ProjectPath == msgB.ProjectPath {
		t.Errorf("state leaked: both paths got same ProjectPath %q", msgA.ProjectPath)
	}
	if msgA.ProjectPath != "/Users/dev/projectA" {
		t.Errorf("pathA ProjectPath: got %q", msgA.ProjectPath)
	}
	if msgB.ProjectPath != "/Users/dev/projectB" {
		t.Errorf("pathB ProjectPath: got %q", msgB.ProjectPath)
	}
}

// ---------------------------------------------------------------------------
// Additional: empty line returns error
// ---------------------------------------------------------------------------

func TestParse_EmptyLine_Error(t *testing.T) {
	meta, mu := newMeta()
	_, err := parseLine([]byte{}, fakePath, meta, mu)
	if err == nil {
		t.Fatal("expected error for empty line, got nil")
	}
}

// ---------------------------------------------------------------------------
// Additional: developer role message is skipped
// ---------------------------------------------------------------------------

func TestParse_DeveloperMessage_Skipped(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-dev-01","cwd":"/Users/dev/devproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	devLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"System instructions here"}]}}`)
	msg, err := parseLine(devLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error for developer message: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for developer message, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// Additional: unknown top-level type is skipped without error
// ---------------------------------------------------------------------------

func TestParse_UnknownTopLevelType_Skipped(t *testing.T) {
	meta, mu := newMeta()
	line := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"future_type_v2","payload":{"data":"xyz"}}`)
	msg, err := parseLine(line, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error for unknown type: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for unknown type, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// Additional: Connector.Parse delegates correctly and maintains per-file state
// ---------------------------------------------------------------------------

func TestConnector_Parse_Delegates(t *testing.T) {
	c := newConnectorForTest()
	// Without session_meta, message-emitting lines should return nil.
	line := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	msg, err := c.Parse(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil before session_meta, got %+v", msg)
	}

	// After session_meta, same message path should emit.
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"cx-delegate","cwd":"/Users/dev/dp"}}`)
	if _, err := c.Parse(sessionLine, fakePath); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	msg2, err := c.Parse(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error after session_meta: %v", err)
	}
	if msg2 == nil {
		t.Fatal("expected non-nil message after session_meta")
	}
	if msg2.Content != "hi" {
		t.Errorf("content: got %q, want hi", msg2.Content)
	}
}

// ---------------------------------------------------------------------------
// Additional: DropPath clears per-file state
// ---------------------------------------------------------------------------

func TestConnector_DropPath(t *testing.T) {
	c := newConnectorForTest()
	path := "/tmp/test.jsonl"
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"cx-drop","cwd":"/Users/dev/dropproj"}}`)
	if _, err := c.Parse(sessionLine, path); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	// State should exist.
	c.mu.Lock()
	_, exists := c.state[path]
	c.mu.Unlock()
	if !exists {
		t.Fatal("state should exist after session_meta")
	}

	c.DropPath(path)

	// State should be gone.
	c.mu.Lock()
	_, exists = c.state[path]
	c.mu.Unlock()
	if exists {
		t.Fatal("state should be removed after DropPath")
	}
}

// ---------------------------------------------------------------------------
// Additional: user message with only environment context text is skipped
// (content after joinText is empty)
// ---------------------------------------------------------------------------

func TestParse_UserMessage_EmptyContent_Skipped(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-empty","cwd":"/Users/dev/emptyproj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	// A user message whose only content items are of type other than input_text.
	userLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"other_type","text":"skip me"}]}}`)
	msg, err := parseLine(userLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for empty-content user message, got %+v", msg)
	}
}

// ---------------------------------------------------------------------------
// Coverage boosters: exercise uncovered branches
// ---------------------------------------------------------------------------

// TestParse_FunctionCall_BeforeSessionMeta_Skipped covers the early-exit
// path in handleFunctionCall when no session_meta has been seen.
func TestParse_FunctionCall_BeforeSessionMeta_Skipped(t *testing.T) {
	meta, mu := newMeta()
	fcLine := []byte(`{"timestamp":"2026-05-06T10:00:03.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"call_early"}}`)
	msg, err := parseLine(fcLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for function_call before session_meta, got %+v", msg)
	}
}

// TestParse_FunctionCallOutput_BeforeSessionMeta_Skipped covers the early-exit
// path in handleFunctionCallOutput when no session_meta has been seen.
func TestParse_FunctionCallOutput_BeforeSessionMeta_Skipped(t *testing.T) {
	meta, mu := newMeta()
	fcoLine := []byte(`{"timestamp":"2026-05-06T10:00:04.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_early","output":"result"}}`)
	msg, err := parseLine(fcoLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for function_call_output before session_meta, got %+v", msg)
	}
}

// TestParse_UnknownResponseItemType_Skipped covers the default case in
// handleResponseItem for an unknown payload.type.
func TestParse_UnknownResponseItemType_Skipped(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-unknown-ri","cwd":"/Users/dev/proj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	unknownLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"future_response_type","data":"ignored"}}`)
	msg, err := parseLine(unknownLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for unknown response_item type, got %+v", msg)
	}
}

// TestParse_UnknownMessageRole_Skipped covers the default case in handleMessage
// for an unknown role (not user, assistant, or developer).
func TestParse_UnknownMessageRole_Skipped(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-role","cwd":"/Users/dev/proj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	line := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"system","content":[{"type":"input_text","text":"system msg"}]}}`)
	msg, err := parseLine(line, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for unknown role, got %+v", msg)
	}
}

// TestParse_TurnContext_EmptyModel_NoUpdate covers the early-return path
// in handleTurnContext when payload.model is empty.
func TestParse_TurnContext_EmptyModel_NoUpdate(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-em","cwd":"/Users/dev/proj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	// turn_context without a model field.
	ctxLine := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"turn_context","payload":{"cwd":"/Users/dev/proj"}}`)
	msg, err := parseLine(ctxLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for turn_context, got %+v", msg)
	}
	// Model should remain empty since turn_context had no model.
	if meta.model != "" {
		t.Errorf("model should remain empty, got %q", meta.model)
	}
}

// TestParse_Timestamp_MsFormat covers the millisecond-format timestamp path.
func TestParse_Timestamp_MsFormat(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"sess-ts","cwd":"/Users/dev/proj"}}`)
	if _, err := parseLine(sessionLine, fakePath, meta, mu); err != nil {
		t.Fatalf("session_meta: %v", err)
	}
	// Use the "2006-01-02T15:04:05.000Z" format (no timezone offset, only Z).
	line := []byte(`{"timestamp":"2026-05-06T10:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ts test"}]}}`)
	msg, err := parseLine(line, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Ts == 0 {
		t.Error("Ts should be non-zero")
	}
}

// TestParse_MissingTimestamp_FallsBackToWallClock covers the wall-clock fallback.
func TestParse_MissingTimestamp_FallsBackToWallClock(t *testing.T) {
	meta, mu := newMeta()
	sessionLine := []byte(`{"timestamp":"","type":"session_meta","payload":{"id":"sess-no-ts","cwd":"/Users/dev/proj"}}`)
	msg, err := parseLine(sessionLine, fakePath, meta, mu)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for session_meta, got %+v", msg)
	}
	// meta should still be updated even without a timestamp.
	if meta.sessionID != "sess-no-ts" {
		t.Errorf("session_id: got %q", meta.sessionID)
	}
}
