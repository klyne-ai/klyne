package claude

import (
	"encoding/json"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

const (
	testPath    = "/Users/alice/.claude/projects/-Users-alice-projects-foo/session.jsonl"
	testSession = "a1b2c3d4-0001-0001-0001-000000000001"
)

// ── helper ────────────────────────────────────────────────────────────────────

func mustParse(t *testing.T, line string) *connectors.Message {
	t.Helper()
	msg, err := Parse([]byte(line), testPath)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	return msg
}

// ── TestParse_<MessageType> ×6 ────────────────────────────────────────────────

func TestParse_System(t *testing.T) {
	line := `{"cwd":"/Users/dev/projects/webhookservice","message":"Session initialized","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","subtype":"init","timestamp":"2026-05-06T10:00:00.000Z","type":"system","uuid":"sys-0001-0001-0001-0001-000000000001"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleSystem {
		t.Errorf("role = %q; want %q", msg.Role, connectors.RoleSystem)
	}
	if msg.CLI != connectors.CLIClaude {
		t.Errorf("cli = %q; want %q", msg.CLI, connectors.CLIClaude)
	}
	if msg.Content != "Session initialized" {
		t.Errorf("content = %q; want %q", msg.Content, "Session initialized")
	}
	if msg.ProjectPath != "/Users/dev/projects/webhookservice" {
		t.Errorf("project_path = %q; want %q", msg.ProjectPath, "/Users/dev/projects/webhookservice")
	}
	if msg.SessionID != testSession {
		t.Errorf("session_id = %q; want %q", msg.SessionID, testSession)
	}
	if msg.Ts == 0 {
		t.Error("Ts is zero")
	}
}

func TestParse_UserText(t *testing.T) {
	line := `{"cwd":"/Users/dev/projects/webhookservice","message":{"content":"I'm seeing 500 errors on our webhook endpoint.","role":"user"},"parentUuid":"null","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","timestamp":"2026-05-06T10:00:05.000Z","type":"user","uuid":"usr-0001-0002-0002-0002-000000000002","version":"1.0.0"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleUser {
		t.Errorf("role = %q; want %q", msg.Role, connectors.RoleUser)
	}
	if msg.Content == "" {
		t.Error("content is empty")
	}
	if msg.ParentUUID != "" {
		t.Errorf("parentUUID should be empty for root, got %q", msg.ParentUUID)
	}
	if msg.CLI != connectors.CLIClaude {
		t.Errorf("cli = %q; want %q", msg.CLI, connectors.CLIClaude)
	}
}

func TestParse_AssistantText(t *testing.T) {
	line := `{"message":{"content":[{"text":"The HMAC logic is correct.","type":"text"}],"id":"msg_01simple001","model":"claude-sonnet-4-6","role":"assistant","stop_reason":"end_turn","usage":{"input_tokens":85,"output_tokens":118,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"parentUuid":"usr-0001-0002-0002-0002-000000000002","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","timestamp":"2026-05-06T10:00:08.000Z","type":"assistant","uuid":"ast-0001-0003-0003-0003-000000000003"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role = %q; want %q", msg.Role, connectors.RoleAssistant)
	}
	if msg.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q; want %q", msg.Model, "claude-sonnet-4-6")
	}
	if msg.TokensIn != 85 {
		t.Errorf("tokens_in = %d; want 85", msg.TokensIn)
	}
	if msg.TokensOut != 118 {
		t.Errorf("tokens_out = %d; want 118", msg.TokensOut)
	}
	if msg.Content == "" {
		t.Error("content is empty")
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(msg.ToolCalls))
	}
}

func TestParse_AssistantToolUse(t *testing.T) {
	// assistant with stop_reason=tool_use; content array contains a tool_use item.
	line := `{"message":{"content":[{"id":"toolu_01BBread001","input":{"file_path":"/Users/dev/projects/webhookservice/internal/webhook/handler.go"},"name":"Read","type":"tool_use"}],"id":"msg_01tools001","model":"claude-sonnet-4-6","role":"assistant","stop_reason":"tool_use","usage":{"input_tokens":110,"output_tokens":38,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"parentUuid":"usr-0002-0002-0002-0002-000000000002","sessionId":"a1b2c3d4-0002-0002-0002-000000000002","timestamp":"2026-05-06T11:00:07.000Z","type":"assistant","uuid":"ast-0002-0003-0003-0003-000000000003"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role = %q; want %q", msg.Role, connectors.RoleAssistant)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != "Read" {
		t.Errorf("tool_call.name = %q; want %q", tc.Name, "Read")
	}
	if tc.ID != "toolu_01BBread001" {
		t.Errorf("tool_call.id = %q; want %q", tc.ID, "toolu_01BBread001")
	}
	// Input should be a valid JSON object
	var inp map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Input), &inp); err != nil {
		t.Errorf("tool_call.input is not valid JSON: %v", err)
	}
}

func TestParse_ToolResult(t *testing.T) {
	// user line carrying a tool_result content item.
	line := `{"cwd":"/Users/dev/projects/webhookservice","message":{"content":[{"content":"package webhook\n\ntype PushEvent struct {...}\n","tool_use_id":"toolu_01BBread001","type":"tool_result"}],"role":"user"},"parentUuid":"ast-0002-0003-0003-0003-000000000003","sessionId":"a1b2c3d4-0002-0002-0002-000000000002","timestamp":"2026-05-06T11:00:08.000Z","type":"user","uuid":"usr-0002-0004-0004-0004-000000000004","version":"1.0.0"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleTool {
		t.Errorf("role = %q; want %q", msg.Role, connectors.RoleTool)
	}
	if len(msg.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(msg.ToolResults))
	}
	tr := msg.ToolResults[0]
	if tr.ID != "toolu_01BBread001" {
		t.Errorf("tool_result.id = %q; want %q", tr.ID, "toolu_01BBread001")
	}
	if tr.Output == "" {
		t.Error("tool_result.output is empty")
	}
	if tr.IsError {
		t.Error("tool_result.is_error should be false")
	}
}

func TestParse_Summary(t *testing.T) {
	line := `{"leafUuid":"ast-0003-0016-0016-0016-000000000016","sessionId":"a1b2c3d4-0003-0003-0003-000000000003","summary":"Working on webhookservice Go project. Added per-IP token bucket rate limiting middleware.","timestamp":"2026-05-06T12:15:03.000Z","type":"summary"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleSystem {
		t.Errorf("role = %q; want %q (summary maps to system)", msg.Role, connectors.RoleSystem)
	}
	if msg.Content == "" {
		t.Error("content is empty for summary message")
	}
	if msg.ParentUUID != "ast-0003-0016-0016-0016-000000000016" {
		t.Errorf("parentUUID = %q; want leafUuid as parentUUID", msg.ParentUUID)
	}
}

// ── tolerance tests ───────────────────────────────────────────────────────────

func TestParse_UnknownField_Tolerated(t *testing.T) {
	// Extra fields "future_field", "experimental_data" should not cause an error.
	line := `{"type":"system","uuid":"sys-001","sessionId":"sess-001","timestamp":"2026-05-06T10:00:00.000Z","message":"Init","future_field":"will-be-ignored","experimental_data":{"nested":true},"cwd":"/tmp/proj"}`
	msg, err := Parse([]byte(line), testPath)
	if err != nil {
		t.Fatalf("Parse() should tolerate unknown fields, got error: %v", err)
	}
	if msg == nil {
		t.Fatal("msg is nil")
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("role = %q; want system", msg.Role)
	}
}

func TestParse_MalformedLine_Skipped(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"not-json", `this is not json at all`},
		{"truncated", `{"type":"assistant","message":`},
		{"empty", ``},
		{"only-brace", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := Parse([]byte(tc.line), testPath)
			if err == nil {
				t.Errorf("expected error for malformed input %q, got msg=%+v", tc.name, msg)
			}
		})
	}
}

// ── fixture file integration tests ───────────────────────────────────────────

func TestParse_FixtureSimple(t *testing.T) {
	// Parse the first fixture file and verify all lines succeed.
	lines := []string{
		`{"cwd":"/Users/dev/projects/webhookservice","message":"Session initialized","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","subtype":"init","timestamp":"2026-05-06T10:00:00.000Z","type":"system","uuid":"sys-0001-0001-0001-0001-000000000001"}`,
		`{"cwd":"/Users/dev/projects/webhookservice","message":{"content":"I'm seeing 500 errors on our webhook endpoint /api/webhooks/github. The error log shows 'invalid signature' but the secret looks correct. Can you help me debug?","role":"user"},"parentUuid":"null","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","timestamp":"2026-05-06T10:00:05.000Z","type":"user","uuid":"usr-0001-0002-0002-0002-000000000002","version":"1.0.0"}`,
		`{"message":{"content":[{"text":"The 'invalid signature' error usually comes down to one of a few causes.","type":"text"}],"id":"msg_01simple001","model":"claude-sonnet-4-6","role":"assistant","stop_reason":"end_turn","usage":{"input_tokens":85,"output_tokens":118,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"parentUuid":"usr-0001-0002-0002-0002-000000000002","sessionId":"a1b2c3d4-0001-0001-0001-000000000001","timestamp":"2026-05-06T10:00:08.000Z","type":"assistant","uuid":"ast-0001-0003-0003-0003-000000000003"}`,
	}
	for i, l := range lines {
		msg, err := Parse([]byte(l), testPath)
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", i, err)
		}
		if msg == nil {
			t.Errorf("line %d: msg is nil", i)
		}
	}
}

func TestParse_ToolUse_MultipleToolCalls(t *testing.T) {
	// Edge case: assistant message with multiple tool_use entries in content.
	line := `{"message":{"content":[{"id":"tool_001","input":{"cmd":"ls"},"name":"Bash","type":"tool_use"},{"id":"tool_002","input":{"path":"/tmp"},"name":"Read","type":"tool_use"}],"id":"msg_multi","model":"claude-sonnet-4-6","role":"assistant","stop_reason":"tool_use","usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}},"sessionId":"sess-001","timestamp":"2026-05-06T10:00:00.000Z","type":"assistant","uuid":"ast-001"}`
	msg := mustParse(t, line)

	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
}

func TestParse_TimestampEpochMs(t *testing.T) {
	line := `{"type":"system","uuid":"sys-001","sessionId":"sess-001","timestamp":"2026-05-06T10:00:00.000Z","message":"Init","cwd":"/tmp"}`
	msg := mustParse(t, line)
	// 2026-05-06T10:00:00Z = 1778061600000 ms
	const want int64 = 1778061600000
	if msg.Ts != want {
		t.Errorf("Ts = %d; want %d", msg.Ts, want)
	}
}

func TestParse_TimestampFallback(t *testing.T) {
	// No sub-second part — should parse via RFC3339 fallback.
	line := `{"type":"system","uuid":"sys-002","sessionId":"sess-002","timestamp":"2026-05-06T10:00:00Z","message":"Init","cwd":"/tmp"}`
	msg, err := Parse([]byte(line), testPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Ts == 0 {
		t.Error("Ts should not be zero")
	}
}

func TestParse_UserPlainStringContent(t *testing.T) {
	// Some user lines have content as a plain string (not array).
	line := `{"type":"user","uuid":"usr-001","sessionId":"sess-001","timestamp":"2026-05-06T10:00:00.000Z","cwd":"/tmp","message":{"role":"user","content":"hello world"}}`
	msg := mustParse(t, line)
	if msg.Role != connectors.RoleUser {
		t.Errorf("role = %q; want user", msg.Role)
	}
	if msg.Content != "hello world" {
		t.Errorf("content = %q; want %q", msg.Content, "hello world")
	}
}

func TestParse_ToolResultArrayContent(t *testing.T) {
	// tool_result content as array of text items.
	line := `{"type":"user","uuid":"usr-002","sessionId":"sess-002","timestamp":"2026-05-06T10:00:00.000Z","cwd":"/tmp","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_001","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}]}}`
	msg := mustParse(t, line)
	if msg.Role != connectors.RoleTool {
		t.Errorf("role = %q; want tool", msg.Role)
	}
	if len(msg.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(msg.ToolResults))
	}
}

func TestParse_UnknownTypeLine(t *testing.T) {
	// An unknown type should return an error (caller skips it).
	line := `{"type":"unknown_future_type","uuid":"x","sessionId":"s","timestamp":"2026-05-06T10:00:00.000Z"}`
	msg, err := Parse([]byte(line), testPath)
	if err == nil {
		t.Errorf("expected error for unknown type, got msg=%+v", msg)
	}
}

// TestParse_AssistantCachedTokens verifies that an assistant message with
// non-zero cache_read_input_tokens and cache_creation_input_tokens populates
// Message.CachedReadTokens and Message.CachedWriteTokens, and that
// Message.TokensIn is the SUM of fresh + cache_read + cache_write.
//
// This is the W4 fix for the bug where Claude sessions undercounted prompt
// tokens by 99%+: the parser previously read only input_tokens (the fresh
// portion).
func TestParse_AssistantCachedTokens(t *testing.T) {
	// 100 fresh + 50_000 cache_read + 25_000 cache_creation
	line := `{"message":{"content":[{"text":"hello cached","type":"text"}],"id":"msg_cached_001","model":"claude-opus-4-7","role":"assistant","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":42,"cache_read_input_tokens":50000,"cache_creation_input_tokens":25000}},"parentUuid":"usr-cached","sessionId":"sess-cached-01","timestamp":"2026-05-06T10:00:00.000Z","type":"assistant","uuid":"ast-cached-001"}`
	msg := mustParse(t, line)

	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role = %q; want assistant", msg.Role)
	}
	wantTokensIn := int64(100 + 50000 + 25000)
	if msg.TokensIn != wantTokensIn {
		t.Errorf("TokensIn = %d; want %d (fresh+cache_read+cache_write)", msg.TokensIn, wantTokensIn)
	}
	if msg.TokensOut != 42 {
		t.Errorf("TokensOut = %d; want 42", msg.TokensOut)
	}
	if msg.CachedReadTokens != 50000 {
		t.Errorf("CachedReadTokens = %d; want 50000", msg.CachedReadTokens)
	}
	if msg.CachedWriteTokens != 25000 {
		t.Errorf("CachedWriteTokens = %d; want 25000", msg.CachedWriteTokens)
	}
}

func TestConnector_NameAndPricing(t *testing.T) {
	c := New("/tmp/test")
	if c.Name() != "claude" {
		t.Errorf("Name() = %q; want %q", c.Name(), "claude")
	}
	p := c.Pricing()
	if p.Models != nil {
		t.Errorf("Pricing().Models should be nil, got %v", p.Models)
	}
}

func TestConnector_ParseDelegates(t *testing.T) {
	c := New("/tmp/test")
	line := `{"type":"system","uuid":"sys-003","sessionId":"sess-003","timestamp":"2026-05-06T10:00:00.000Z","message":"Init","cwd":"/tmp"}`
	msg, err := c.Parse([]byte(line), testPath)
	if err != nil {
		t.Fatalf("Connector.Parse() unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("role = %q; want system", msg.Role)
	}
}
