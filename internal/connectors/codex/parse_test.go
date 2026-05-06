package codex

import (
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

const fakePath = "/home/dev/.codex/sessions/2026/05/06/rollout-abc.jsonl"

// TestParse_SessionMeta verifies session_meta lines produce system messages.
func TestParse_SessionMeta(t *testing.T) {
	line := []byte(`{"type":"session_meta","session_id":"cx-001","model":"gpt-5","cwd":"/Users/dev/project","timestamp":"2026-05-06T10:00:00.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleSystem)
	}
	if msg.CLI != connectors.CLICodex {
		t.Errorf("cli: got %q, want %q", msg.CLI, connectors.CLICodex)
	}
	if msg.SessionID != "cx-001" {
		t.Errorf("session_id: got %q, want cx-001", msg.SessionID)
	}
	if msg.ProjectPath != "/Users/dev/project" {
		t.Errorf("project_path: got %q, want /Users/dev/project", msg.ProjectPath)
	}
	if msg.Ts == 0 {
		t.Error("ts should be non-zero")
	}
}

// TestParse_Input verifies input (user) lines.
func TestParse_Input(t *testing.T) {
	line := []byte(`{"type":"input","session_id":"cx-001","role":"user","content":"Hello world","timestamp":"2026-05-06T10:00:05.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleUser {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleUser)
	}
	if msg.Content != "Hello world" {
		t.Errorf("content: got %q, want %q", msg.Content, "Hello world")
	}
}

// TestParse_Output verifies output (assistant) lines.
func TestParse_Output(t *testing.T) {
	line := []byte(`{"type":"output","session_id":"cx-001","role":"assistant","content":"Hi there","model":"gpt-5","timestamp":"2026-05-06T10:00:08.000Z","usage":{"prompt_tokens":95,"completion_tokens":118,"total_tokens":213}}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleAssistant {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleAssistant)
	}
	if msg.Model != "gpt-5" {
		t.Errorf("model: got %q, want gpt-5", msg.Model)
	}
	if msg.Content != "Hi there" {
		t.Errorf("content: got %q, want %q", msg.Content, "Hi there")
	}
}

// TestParse_TokenExtraction verifies that usage.prompt_tokens and
// usage.completion_tokens are mapped to Message.TokensIn / TokensOut.
func TestParse_TokenExtraction(t *testing.T) {
	line := []byte(`{"type":"output","session_id":"cx-001","content":"answer","model":"gpt-5","timestamp":"2026-05-06T10:00:08.000Z","usage":{"prompt_tokens":95,"completion_tokens":118,"total_tokens":213}}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.TokensIn != 95 {
		t.Errorf("tokens_in: got %d, want 95", msg.TokensIn)
	}
	if msg.TokensOut != 118 {
		t.Errorf("tokens_out: got %d, want 118", msg.TokensOut)
	}
}

// TestParse_FunctionCall verifies function_call lines produce tool messages
// with a populated ToolCalls slice.
func TestParse_FunctionCall(t *testing.T) {
	line := []byte(`{"type":"function_call","session_id":"cx-002","call_id":"cx-call-001","name":"shell","arguments":{"command":"ls -la"},"timestamp":"2026-05-06T11:00:07.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleTool {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleTool)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len: got %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "cx-call-001" {
		t.Errorf("tool_call id: got %q, want cx-call-001", tc.ID)
	}
	if tc.Name != "shell" {
		t.Errorf("tool_call name: got %q, want shell", tc.Name)
	}
}

// TestParse_FunctionCallOutput verifies function_call_output lines.
func TestParse_FunctionCallOutput(t *testing.T) {
	line := []byte(`{"type":"function_call_output","session_id":"cx-002","call_id":"cx-call-001","output":"total 0\ndrwxr-xr-x  2 dev dev 40 Jan  1 00:00 .","timestamp":"2026-05-06T11:00:08.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleTool {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleTool)
	}
	if len(msg.ToolResults) != 1 {
		t.Fatalf("tool_results len: got %d, want 1", len(msg.ToolResults))
	}
	tr := msg.ToolResults[0]
	if tr.ID != "cx-call-001" {
		t.Errorf("tool_result id: got %q, want cx-call-001", tr.ID)
	}
}

// TestParse_CompactEvent verifies the synthetic compact_event line is
// parsed as a system message without panicking.
func TestParse_CompactEvent(t *testing.T) {
	line := []byte(`{"type":"compact_event","session_id":"cx-003","before_tokens":18420,"after_tokens":2180,"summary":"Working on webhookservice.","timestamp":"2026-05-06T12:15:02.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("role: got %q, want %q", msg.Role, connectors.RoleSystem)
	}
	// Content should mention the token counts.
	if msg.Content == "" {
		t.Error("content should not be empty for compact_event")
	}
}

// TestParse_UnknownField_Tolerated verifies that extra unknown fields do not
// cause parse errors (forward-compatibility requirement).
func TestParse_UnknownField_Tolerated(t *testing.T) {
	line := []byte(`{"type":"output","session_id":"cx-001","content":"ok","model":"gpt-5","timestamp":"2026-05-06T10:00:08.000Z","future_field":"some_value","nested":{"a":1}}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error on unknown fields: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
}

// TestParse_UnknownType verifies that a line with an unknown type value is
// returned as a system message rather than causing a panic or error.
func TestParse_UnknownType(t *testing.T) {
	line := []byte(`{"type":"totally_new_type","session_id":"cx-001","content":"mystery","timestamp":"2026-05-06T10:00:08.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error on unknown type: %v", err)
	}
	if msg.Role != connectors.RoleSystem {
		t.Errorf("unknown type should map to RoleSystem, got %q", msg.Role)
	}
}

// TestParse_MalformedLine_Skipped verifies that non-JSON input returns an
// error and does not panic.
func TestParse_MalformedLine_Skipped(t *testing.T) {
	line := []byte(`this is not json {{{`)
	msg, err := ParseLine(line, fakePath)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if msg != nil {
		t.Fatal("expected nil message for malformed JSON")
	}
}

// TestParse_EmptyLine_Skipped verifies that an empty line returns an error
// rather than a nil dereference.
func TestParse_EmptyLine_Skipped(t *testing.T) {
	_, err := ParseLine([]byte{}, fakePath)
	if err == nil {
		t.Fatal("expected error for empty line")
	}
}

// TestParse_SessionIDFromPath verifies fallback when session_id is absent.
func TestParse_SessionIDFromPath(t *testing.T) {
	line := []byte(`{"type":"input","content":"hello","timestamp":"2026-05-06T10:00:05.000Z"}`)
	path := "/home/dev/.codex/sessions/2026/05/06/rollout-myid.jsonl"
	msg, err := ParseLine(line, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.SessionID != "rollout-myid" {
		t.Errorf("session_id fallback: got %q, want rollout-myid", msg.SessionID)
	}
}

// TestParse_TimestampParsing verifies ISO 8601 timestamps are converted to epoch-ms.
func TestParse_TimestampParsing(t *testing.T) {
	line := []byte(`{"type":"input","session_id":"cx-001","content":"x","timestamp":"2026-05-06T10:00:05.000Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-05-06T10:00:05.000Z in epoch-ms = 1778061605000
	const want int64 = 1778061605000
	if msg.Ts != want {
		t.Errorf("ts: got %d, want %d", msg.Ts, want)
	}
}

// TestParse_TimestampFallback verifies that a missing timestamp still yields
// a non-zero Ts (wall-clock fallback).
func TestParse_TimestampFallback(t *testing.T) {
	line := []byte(`{"type":"input","session_id":"cx-001","content":"no-ts"}`)
	before := time.Now().UnixMilli()
	msg, err := ParseLine(line, fakePath)
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Ts < before || msg.Ts > after {
		t.Errorf("ts fallback out of expected range: got %d (before=%d after=%d)", msg.Ts, before, after)
	}
}

// TestParse_TimestampRFC3339Nano verifies that RFC3339Nano timestamps parse.
func TestParse_TimestampRFC3339Nano(t *testing.T) {
	line := []byte(`{"type":"input","session_id":"cx-001","content":"x","timestamp":"2026-05-06T10:00:05.123456789Z"}`)
	msg, err := ParseLine(line, fakePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Ts == 0 {
		t.Error("ts should be non-zero for RFC3339Nano timestamp")
	}
}

// TestNormalizeRole_DirectRoleFields exercises role fallback paths in
// normalizeRole when type is empty or unrecognized.
func TestNormalizeRole_DirectRoleFields(t *testing.T) {
	tests := []struct {
		role     string
		typ      string
		wantRole connectors.Role
	}{
		{"user", "", connectors.RoleUser},
		{"assistant", "", connectors.RoleAssistant},
		{"system", "", connectors.RoleSystem},
		{"unknown", "unknown_type", connectors.RoleSystem},
	}
	for _, tt := range tests {
		got := normalizeRole(tt.role, tt.typ)
		if got != tt.wantRole {
			t.Errorf("normalizeRole(%q, %q): got %q, want %q", tt.role, tt.typ, got, tt.wantRole)
		}
	}
}

// TestParse_FixtureSimple exercises session-001-simple.jsonl by parsing each
// type found in the simplest fixture.
func TestParse_FixtureSimple(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"cwd":"/Users/dev/projects/webhookservice","model":"gpt-5","session_id":"cx-0001-0001-0001-0001-000000000001","timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta"}`),
		[]byte(`{"content":"Why does my Go webhook return 500?","role":"user","timestamp":"2026-05-06T10:00:05.000Z","type":"input"}`),
		[]byte(`{"content":"The cause is...","model":"gpt-5","role":"assistant","timestamp":"2026-05-06T10:00:08.000Z","type":"output","usage":{"prompt_tokens":95,"completion_tokens":118,"total_tokens":213}}`),
	}
	wantRoles := []connectors.Role{
		connectors.RoleSystem,
		connectors.RoleUser,
		connectors.RoleAssistant,
	}
	for i, line := range lines {
		msg, err := ParseLine(line, fakePath)
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", i, err)
			continue
		}
		if msg.Role != wantRoles[i] {
			t.Errorf("line %d role: got %q, want %q", i, msg.Role, wantRoles[i])
		}
	}
}
