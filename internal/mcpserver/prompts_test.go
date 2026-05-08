package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callPrompt invokes a prompt handler with the given cwd argument and
// returns the result. Helper to keep prompt tests readable — every
// prompt accepts the same `cwd` override argument so tests can target
// the fake home built by withFakeHome.
func callPrompt(t *testing.T, h mcp.PromptHandler, cwd string) *mcp.GetPromptResult {
	t.Helper()
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Arguments: map[string]string{"cwd": cwd},
		},
	}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("prompt handler err: %v", err)
	}
	if res == nil {
		t.Fatalf("prompt handler returned nil result")
	}
	if len(res.Messages) == 0 {
		t.Fatalf("prompt result has zero messages")
	}
	return res
}

// promptText concatenates every TextContent across messages. Lets
// tests assert keywords appear without caring which message carries
// them.
func promptText(res *mcp.GetPromptResult) string {
	var b strings.Builder
	for _, m := range res.Messages {
		if tc, ok := m.Content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestPromptHealth_ReportsRescueOnHighFill(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/prompt-health-rescue"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// 850K cache-aware tokens against Opus 4.7 1M window → 85% → rescue_now.
	writeJSONL(t, dir, "rescue.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-04-08T10:00:00.000Z","cwd":"`+cwd+`","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","cwd":"`+cwd+`","message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":850000}}}`,
	)

	res := callPrompt(t, PromptHealthHandler, cwd)
	text := promptText(res)
	if !strings.Contains(text, "rescue_now") {
		t.Errorf("prompt text missing 'rescue_now': %q", text)
	}
	if !strings.Contains(text, "start_fresh") {
		t.Errorf("prompt text missing 'start_fresh': %q", text)
	}
}

func TestPromptHealth_NoSessionGracefulMessage(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	withFakeHome(t)
	res := callPrompt(t, PromptHealthHandler, "/tmp/no-such-prompt-cwd")
	text := promptText(res)
	if !strings.Contains(strings.ToLower(text), "no") {
		t.Errorf("expected text to mention 'no session found', got %q", text)
	}
}

func TestPromptSessions_ListsBothCLIs(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/prompt-sessions"
	makeProjectDir(t, home, EncodeCWD(cwd), "claude-only.jsonl")
	// Add a Codex session in the same cwd via the helper.
	makeCodexSession(t, home, "codex-prompt-sess", cwd, time.Now())

	res := callPrompt(t, PromptSessionsHandler, cwd)
	text := promptText(res)
	if !strings.Contains(text, "claude") || !strings.Contains(text, "codex") {
		t.Errorf("expected both 'claude' and 'codex' in prompt text, got %q", text)
	}
}

func TestPromptHandoff_ReturnsMarkdown(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/prompt-handoff"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "h.jsonl",
		`{"type":"user","sessionId":"hsess","timestamp":"2026-04-08T10:00:00.000Z","cwd":"`+cwd+`","message":{"role":"user","content":[{"type":"text","text":"plan the rollout"}]}}`,
		`{"type":"assistant","sessionId":"hsess","uuid":"a1","timestamp":"2026-04-08T10:00:01.000Z","cwd":"`+cwd+`","message":{"role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"on it"}],"usage":{"input_tokens":1,"cache_read_input_tokens":1000}}}`,
	)
	res := callPrompt(t, PromptHandoffHandler, cwd)
	text := promptText(res)
	// Handoff renders Markdown with a "Handoff from session" header.
	if !strings.Contains(text, "Handoff from session") {
		t.Errorf("expected markdown handoff content, got %q", text)
	}
}

func TestPromptSearch_RendersHitsAsMarkdown(t *testing.T) {
	d := newFakeDaemon(t, 200, `{
		"query":"bank sms",
		"hits":[
			{"message_id":"m1","session_id":"sess-bank-fix","cli":"claude","project_path":"/repos/trackit","role":"user","snippet":"the bank sms isn't being parsed","score":-1.5,"ts":1746690000000}
		],
		"took_ms":7
	}`)
	t.Setenv("KLYNE_BASE_URL", d.srv.URL)

	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Arguments: map[string]string{"query": "bank sms"},
		},
	}
	res, err := PromptSearchHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	text := promptText(res)
	if !strings.Contains(text, "sess-ban") {
		t.Errorf("expected short session id in markdown, got %q", text)
	}
	if !strings.Contains(text, "/repos/trackit") {
		t.Errorf("expected project path in markdown, got %q", text)
	}
	if !strings.Contains(text, "bank sms") {
		t.Errorf("expected snippet text in markdown, got %q", text)
	}
}

func TestPromptSearch_DaemonDownGracefulMessage(t *testing.T) {
	t.Setenv("KLYNE_BASE_URL", "http://127.0.0.1:1") // not listening
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Arguments: map[string]string{"query": "anything"},
		},
	}
	res, err := PromptSearchHandler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler should not return go error; got %v", err)
	}
	text := strings.ToLower(promptText(res))
	if !strings.Contains(text, "daemon") {
		t.Errorf("expected daemon mention in fallback text, got %q", text)
	}
}

func TestPromptPreCompact_SurfacesNoCompactMessage(t *testing.T) {
	// Cannot t.Parallel: uses HOME via withFakeHome.
	home := withFakeHome(t)
	cwd := "/tmp/prompt-pc-fresh"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	writeJSONL(t, dir, "fresh.jsonl",
		`{"type":"user","sessionId":"pc","timestamp":"2026-04-08T10:00:00.000Z","cwd":"`+cwd+`","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
	)
	res := callPrompt(t, PromptPreCompactHandler, cwd)
	text := strings.ToLower(promptText(res))
	if !strings.Contains(text, "compact") {
		t.Errorf("expected text to mention compact, got %q", text)
	}
}

