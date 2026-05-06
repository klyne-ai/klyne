//go:build ignore
// +build ignore

// generate.go produces deterministic synthetic JSONL fixture files for
// Claude Code and Codex CLI sessions used in W4/W5 parser tests, W15 compact
// recovery tests, and W16 performance benchmarks.
//
// Run with:
//
//	go run examples/sample-jsonl/generate.go
//
// The output is byte-identical across runs (fixed seed, fixed timestamps).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ---------------------------------------------------------------------------
// Shared types
// ---------------------------------------------------------------------------

// epoch is the deterministic start time used for all fixtures.
var epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)

func ts(add time.Duration) string {
	return epoch.Add(add).UTC().Format("2006-01-02T15:04:05.000Z")
}

// seqUUID produces a deterministic UUID-shaped string from a fixed prefix and
// an incrementing counter. It is NOT random — that is intentional so that
// re-running the generator produces identical files.
func seqUUID(prefix string, n int) string {
	return fmt.Sprintf("%s-%04d-%04d-%04d-%012d", prefix, n, n, n, n)
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeLine(f *os.File, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		panic(err)
	}
}

func openFile(path string) *os.File {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	return f
}

// ---------------------------------------------------------------------------
// Claude Code line builders
// Field-name decisions are documented in README.md.
// ---------------------------------------------------------------------------

func claudeSystem(sessionID, uuid string, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "system",
		"subtype":   "init",
		"cwd":       "/Users/dev/projects/webhookservice",
		"sessionId": sessionID,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
		"uuid":      uuid,
		"message":   "Session initialized",
	}
}

func claudeUser(sessionID, uuid, parentUUID, content string, offsetSec int) map[string]any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
		"uuid":       uuid,
		"parentUuid": parentUUID,
		"sessionId":  sessionID,
		"timestamp":  ts(time.Duration(offsetSec) * time.Second),
		"cwd":        "/Users/dev/projects/webhookservice",
		"version":    "1.0.0",
	}
}

func claudeUserToolResult(sessionID, uuid, parentUUID, toolUseID, content string, offsetSec int) map[string]any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "tool_result", "tool_use_id": toolUseID, "content": content},
			},
		},
		"uuid":       uuid,
		"parentUuid": parentUUID,
		"sessionId":  sessionID,
		"timestamp":  ts(time.Duration(offsetSec) * time.Second),
		"cwd":        "/Users/dev/projects/webhookservice",
		"version":    "1.0.0",
	}
}

type claudeUsage struct {
	InputTokens               int `json:"input_tokens"`
	OutputTokens              int `json:"output_tokens"`
	CacheReadInputTokens      int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
}

func claudeAssistantText(sessionID, msgID, uuid, parentUUID, text string, usage claudeUsage, offsetSec int) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    msgID,
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
			"stop_reason": "end_turn",
			"usage":       usage,
		},
		"uuid":       uuid,
		"parentUuid": parentUUID,
		"sessionId":  sessionID,
		"timestamp":  ts(time.Duration(offsetSec) * time.Second),
	}
}

func claudeAssistantToolUse(sessionID, msgID, uuid, parentUUID, toolID, toolName string, input map[string]any, usage claudeUsage, offsetSec int) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    msgID,
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "tool_use", "id": toolID, "name": toolName, "input": input},
			},
			"stop_reason": "tool_use",
			"usage":       usage,
		},
		"uuid":       uuid,
		"parentUuid": parentUUID,
		"sessionId":  sessionID,
		"timestamp":  ts(time.Duration(offsetSec) * time.Second),
	}
}

func claudeSummary(sessionID, summaryText, leafUUID string, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "summary",
		"summary":   summaryText,
		"leafUuid":  leafUUID,
		"sessionId": sessionID,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
	}
}

// ---------------------------------------------------------------------------
// Codex CLI line builders
// Field-name decisions are documented in README.md.
// ---------------------------------------------------------------------------

func codexMeta(sessionID, model, cwd string, offsetSec int) map[string]any {
	return map[string]any{
		"type":       "session_meta",
		"session_id": sessionID,
		"model":      model,
		"timestamp":  ts(time.Duration(offsetSec) * time.Second),
		"cwd":        cwd,
	}
}

func codexInput(content string, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "input",
		"role":      "user",
		"content":   content,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
	}
}

type codexUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func codexOutput(model, content string, usage codexUsage, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "output",
		"role":      "assistant",
		"model":     model,
		"content":   content,
		"usage":     usage,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
	}
}

func codexFunctionCall(callID, name string, args map[string]any, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": args,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
	}
}

func codexFunctionOutput(callID, output string, offsetSec int) map[string]any {
	return map[string]any{
		"type":      "function_call_output",
		"call_id":   callID,
		"output":    output,
		"timestamp": ts(time.Duration(offsetSec) * time.Second),
	}
}

// codexCompactEvent is a synthetic compact marker invented for testing.
// Codex's actual compaction signal is not publicly documented as of May 2026;
// W5 will adapt this shape to the real format when fixtures are available.
func codexCompactEvent(sessionID string, beforeTokens, afterTokens int, summary string, offsetSec int) map[string]any {
	return map[string]any{
		"type":          "compact_event",
		"session_id":    sessionID,
		"before_tokens": beforeTokens,
		"after_tokens":  afterTokens,
		"summary":       summary,
		"timestamp":     ts(time.Duration(offsetSec) * time.Second),
	}
}

// ---------------------------------------------------------------------------
// Claude fixtures
// ---------------------------------------------------------------------------

func writeClaudeSession001(dir string) {
	f := openFile(filepath.Join(dir, "claude", "session-001-simple.jsonl"))
	defer f.Close()

	sid := "a1b2c3d4-0001-0001-0001-000000000001"

	writeLine(f, claudeSystem(sid, seqUUID("sys-0001", 1), 0))
	writeLine(f, claudeUser(sid, seqUUID("usr-0001", 2), "null",
		"I'm seeing 500 errors on our webhook endpoint /api/webhooks/github. The error log shows 'invalid signature' but the secret looks correct. Can you help me debug?",
		5))
	writeLine(f, claudeAssistantText(sid, "msg_01simple001", seqUUID("ast-0001", 3), seqUUID("usr-0001", 2),
		"The 'invalid signature' error usually comes down to one of a few causes:\n\n1. **Whitespace in the secret**: Trailing newline from .env files changes the HMAC.\n2. **Wrong encoding**: GitHub sends `sha256=<hex>` — compare against hex-encoded HMAC with the prefix.\n3. **Reading the body twice**: If a middleware consumes `req.body` before your signature check, the raw bytes used for signing are gone.\n\nCan you share the relevant handler code?",
		claudeUsage{InputTokens: 85, OutputTokens: 118}, 8))
	writeLine(f, claudeUser(sid, seqUUID("usr-0001", 4), seqUUID("ast-0001", 3),
		"Here's the handler — I'm using io.ReadAll. The secret comes from os.Getenv(\"WEBHOOK_SECRET\"). The HMAC logic looks correct to me.",
		20))
	writeLine(f, claudeAssistantText(sid, "msg_01simple002", seqUUID("ast-0001", 5), seqUUID("usr-0001", 4),
		"The HMAC logic is correct. The issue is almost certainly the secret value. Try `strings.TrimSpace(os.Getenv(\"WEBHOOK_SECRET\"))` — secrets injected from files often carry a trailing newline that's invisible but changes the HMAC entirely. Also return HTTP 401 instead of 500 for invalid signatures.",
		claudeUsage{InputTokens: 310, OutputTokens: 88}, 24))
	writeLine(f, claudeUser(sid, seqUUID("usr-0001", 6), seqUUID("ast-0001", 5),
		"That was it! Trailing newline in the .env file. strings.TrimSpace fixed it. Thanks!",
		65))
	writeLine(f, claudeAssistantText(sid, "msg_01simple003", seqUUID("ast-0001", 7), seqUUID("usr-0001", 6),
		"Classic gotcha. Trailing newlines in secret files are extremely common when using `echo SECRET=value >> .env`. Using `strings.TrimSpace` as a default for all env-var secrets prevents this entire class of bug.",
		claudeUsage{InputTokens: 465, OutputTokens: 60}, 67))
}

func writeClaudeSession002(dir string) {
	f := openFile(filepath.Join(dir, "claude", "session-002-with-tools.jsonl"))
	defer f.Close()

	sid := "a1b2c3d4-0002-0002-0002-000000000002"
	// Offset epoch by 1 hour for session 002
	epoch = time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)
	defer func() { epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC) }()

	writeLine(f, claudeSystem(sid, seqUUID("sys-0002", 1), 0))
	writeLine(f, claudeUser(sid, seqUUID("usr-0002", 2), "null",
		"The webhook handler is still misfiring on GitHub push events. Half the time the branch filter drops valid pushes. Can you look at the handler file?",
		5))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01tools001", seqUUID("ast-0002", 3), seqUUID("usr-0002", 2),
		"toolu_01BBread001", "Read",
		map[string]any{"file_path": "/Users/dev/projects/webhookservice/internal/webhook/handler.go"},
		claudeUsage{InputTokens: 110, OutputTokens: 38}, 7))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0002", 4), seqUUID("ast-0002", 3),
		"toolu_01BBread001",
		"package webhook\n\ntype PushEvent struct {\n\tRef string `json:\"ref\"`\n}\n\nfunc HandlePush(w http.ResponseWriter, r *http.Request, allowedBranch string) {\n\tvar evt PushEvent\n\tjson.NewDecoder(r.Body).Decode(&evt)\n\t// BUG: ref is \"refs/heads/main\", not \"main\"\n\tif evt.Ref != allowedBranch {\n\t\tw.WriteHeader(http.StatusNoContent)\n\t\treturn\n\t}\n\tprocessPush(evt)\n\tw.WriteHeader(http.StatusOK)\n}\n",
		8))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01tools002", seqUUID("ast-0002", 5), seqUUID("usr-0002", 4),
		"toolu_01BBbash001", "Bash",
		map[string]any{"command": "grep -n 'allowedBranch\\|HandlePush' /Users/dev/projects/webhookservice/cmd/server/main.go"},
		claudeUsage{InputTokens: 285, OutputTokens: 42}, 10))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0002", 6), seqUUID("ast-0002", 5),
		"toolu_01BBbash001",
		"42:    branch := os.Getenv(\"ALLOWED_BRANCH\")\n44:        webhook.HandlePush(w, r, branch)\n",
		11))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01tools003", seqUUID("ast-0002", 7), seqUUID("usr-0002", 6),
		"toolu_01BBedit001", "Edit",
		map[string]any{
			"file_path":  "/Users/dev/projects/webhookservice/internal/webhook/handler.go",
			"old_string": "\t// BUG: ref is \"refs/heads/main\", not \"main\"\n\tif evt.Ref != allowedBranch {",
			"new_string":  "\tbranch := strings.TrimPrefix(evt.Ref, \"refs/heads/\")\n\tif branch != allowedBranch {",
		},
		claudeUsage{InputTokens: 375, OutputTokens: 88}, 13))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0002", 8), seqUUID("ast-0002", 7),
		"toolu_01BBedit001",
		"File edited successfully. Applied 1 change.",
		14))
	writeLine(f, claudeAssistantText(sid, "msg_01tools004", seqUUID("ast-0002", 9), seqUUID("usr-0002", 8),
		"Patch applied. GitHub's `ref` field uses `refs/heads/main`, not `main`. The `strings.TrimPrefix` call normalizes it before comparison. Tag pushes (`refs/tags/v1.0`) are also filtered out correctly now.",
		claudeUsage{InputTokens: 495, OutputTokens: 72}, 17))
	writeLine(f, claudeUser(sid, seqUUID("usr-0002", 10), seqUUID("ast-0002", 9),
		"Works perfectly now. Branch filter is correct and tags are ignored.",
		90))
	writeLine(f, claudeAssistantText(sid, "msg_01tools005", seqUUID("ast-0002", 11), seqUUID("usr-0002", 10),
		"The full `refs/heads/` prefix is a very common GitHub webhook pitfall. Adding a unit test for both the pass case and the skip case will lock in the correct behavior.",
		claudeUsage{InputTokens: 580, OutputTokens: 45}, 92))
}

func writeClaudeSession003(dir string) {
	f := openFile(filepath.Join(dir, "claude", "session-003-with-compact.jsonl"))
	defer f.Close()

	sid := "a1b2c3d4-0003-0003-0003-000000000003"
	// Offset epoch by 2 hours for session 003
	epoch = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	defer func() { epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC) }()

	writeLine(f, claudeSystem(sid, seqUUID("sys-0003", 1), 0))
	// --- Pre-compact messages (tokens climb) ---
	writeLine(f, claudeUser(sid, seqUUID("usr-0003", 2), "null",
		"I need to add rate limiting to our webhook endpoint. We're getting hammered by retries when CI is slow. Per-IP limiter with configurable limit.",
		5))
	writeLine(f, claudeAssistantText(sid, "msg_01compact001", seqUUID("ast-0003", 3), seqUUID("usr-0003", 2),
		"Good plan. Token bucket keyed by IP is the right tool. I'll use `golang.org/x/time/rate` + `sync.Map`. Let me read the middleware and config files first.",
		claudeUsage{InputTokens: 105, OutputTokens: 80}, 8))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact002", seqUUID("ast-0003", 4), seqUUID("ast-0003", 3),
		"toolu_01CCread001", "Read",
		map[string]any{"file_path": "/Users/dev/projects/webhookservice/internal/middleware/middleware.go"},
		claudeUsage{InputTokens: 260, OutputTokens: 35}, 9))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 5), seqUUID("ast-0003", 4),
		"toolu_01CCread001",
		"package middleware\n\nimport \"net/http\"\n\nfunc Logger(next http.Handler) http.Handler { ... }\nfunc Recovery(next http.Handler) http.Handler { ... }\n",
		10))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact003", seqUUID("ast-0003", 6), seqUUID("usr-0003", 5),
		"toolu_01CCread002", "Read",
		map[string]any{"file_path": "/Users/dev/projects/webhookservice/internal/config/config.go"},
		claudeUsage{InputTokens: 475, OutputTokens: 34}, 11))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 7), seqUUID("ast-0003", 6),
		"toolu_01CCread002",
		"package config\n\ntype Config struct {\n\tPort int; Secret string; Branch string\n}\n",
		12))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact004", seqUUID("ast-0003", 8), seqUUID("usr-0003", 7),
		"toolu_01CCedit001", "Edit",
		map[string]any{
			"file_path":  "/Users/dev/projects/webhookservice/internal/config/config.go",
			"old_string": "type Config struct {\n\tPort int; Secret string; Branch string\n}",
			"new_string":  "type Config struct {\n\tPort int; Secret string; Branch string\n\tRateLimit float64 `toml:\"rate_limit\"`\n\tBurst     int     `toml:\"burst\"`\n}",
		},
		claudeUsage{InputTokens: 640, OutputTokens: 90}, 14))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 9), seqUUID("ast-0003", 8),
		"toolu_01CCedit001",
		"File edited successfully. Applied 1 change.",
		15))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact005", seqUUID("ast-0003", 10), seqUUID("usr-0003", 9),
		"toolu_01CCedit002", "Edit",
		map[string]any{
			"file_path":  "/Users/dev/projects/webhookservice/internal/middleware/middleware.go",
			"old_string": "package middleware\n\nimport \"net/http\"",
			"new_string":  "package middleware\n\nimport (\n\t\"net/http\"\n\t\"strings\"\n\t\"sync\"\n\n\t\"golang.org/x/time/rate\"\n)",
		},
		claudeUsage{InputTokens: 750, OutputTokens: 72}, 18))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 11), seqUUID("ast-0003", 10),
		"toolu_01CCedit002",
		"File edited successfully. Applied 1 change.",
		19))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact006", seqUUID("ast-0003", 12), seqUUID("usr-0003", 11),
		"toolu_01CCbash001", "Bash",
		map[string]any{"command": "cd /Users/dev/projects/webhookservice && go build ./..."},
		claudeUsage{InputTokens: 1090, OutputTokens: 38}, 23))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 13), seqUUID("ast-0003", 12),
		"toolu_01CCbash001",
		"",
		25))
	writeLine(f, claudeAssistantText(sid, "msg_01compact007", seqUUID("ast-0003", 14), seqUUID("usr-0003", 13),
		"Build passes. Rate limiter middleware complete: `NewRateLimiter(rps, burst)` using `sync.Map` + `rate.Limiter`, `realIP()` respects `X-Forwarded-For`, returns 429 on limit exceeded.",
		claudeUsage{InputTokens: 1180, OutputTokens: 68}, 27))
	writeLine(f, claudeUser(sid, seqUUID("usr-0003", 15), seqUUID("ast-0003", 14),
		"This session is getting long. Can you do a /compact so we have a fresh context?",
		900))

	// The last assistant message before compact — leafUuid points here
	leafUUID := seqUUID("ast-0003", 16)
	writeLine(f, claudeAssistantText(sid, "msg_01compact008", leafUUID, seqUUID("usr-0003", 15),
		"Sure, compacting now.",
		claudeUsage{InputTokens: 1250, OutputTokens: 8}, 902))

	// --- Compact summary marker (W15 detects this line) ---
	writeLine(f, claudeSummary(sid,
		"Working on webhookservice Go project. Added per-IP token bucket rate limiting middleware. Changes: (1) Config.RateLimit (float64, default 10 rps) and Config.Burst (int, default 20) added. (2) middleware.NewRateLimiter uses sync.Map + rate.Limiter, returns 429 on limit exceeded. (3) realIP() respects X-Forwarded-For. Build passes. Next: wire middleware into main.go.",
		leafUUID,
		903))

	// --- Post-compact messages (input_tokens dramatically lower — W15 heuristic) ---
	writeLine(f, claudeUser(sid, seqUUID("usr-0003", 17), leafUUID,
		"Great. Now let's wire the middleware into main.go.",
		910))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact009", seqUUID("ast-0003", 18), seqUUID("usr-0003", 17),
		"toolu_01CCread003", "Read",
		map[string]any{"file_path": "/Users/dev/projects/webhookservice/cmd/server/main.go"},
		// input_tokens is dramatically lower post-compact — this is the W15 detection heuristic
		claudeUsage{InputTokens: 185, OutputTokens: 34}, 912))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 19), seqUUID("ast-0003", 18),
		"toolu_01CCread003",
		"package main\n\nfunc main() {\n\tcfg := config.Default()\n\t...\n\thandler := middleware.Logger(middleware.Recovery(mux))\n\thttp.ListenAndServe(\":8080\", handler)\n}\n",
		913))
	writeLine(f, claudeAssistantToolUse(sid, "msg_01compact010", seqUUID("ast-0003", 20), seqUUID("usr-0003", 19),
		"toolu_01CCedit003", "Edit",
		map[string]any{
			"file_path":  "/Users/dev/projects/webhookservice/cmd/server/main.go",
			"old_string": "\thandler := middleware.Logger(middleware.Recovery(mux))",
			"new_string":  "\trl := middleware.NewRateLimiter(cfg.RateLimit, cfg.Burst)\n\thandler := rl(middleware.Logger(middleware.Recovery(mux)))",
		},
		claudeUsage{InputTokens: 415, OutputTokens: 82}, 914))
	writeLine(f, claudeUserToolResult(sid, seqUUID("usr-0003", 21), seqUUID("ast-0003", 20),
		"toolu_01CCedit003",
		"File edited successfully. Applied 1 change.",
		915))
	writeLine(f, claudeAssistantText(sid, "msg_01compact011", seqUUID("ast-0003", 22), seqUUID("usr-0003", 21),
		"Done. Middleware chain is now RateLimiter → Logger → Recovery → mux. Rate-limited requests return 429 before logging, keeping access logs clean.",
		claudeUsage{InputTokens: 485, OutputTokens: 42}, 918))
}

// ---------------------------------------------------------------------------
// Codex fixtures
// ---------------------------------------------------------------------------

func writeCodexSession001(dir string) {
	f := openFile(filepath.Join(dir, "codex", "session-001-simple.jsonl"))
	defer f.Close()

	sid := "cx-0001-0001-0001-0001-000000000001"
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)

	writeLine(f, codexMeta(sid, "gpt-5", "/Users/dev/projects/webhookservice", 0))
	writeLine(f, codexInput("Why does my Go webhook handler return 500 for GitHub events? The signature check seems to fail every time.", 5))
	writeLine(f, codexOutput("gpt-5",
		"The most common cause of HMAC signature mismatch in Go webhook handlers is reading the request body twice. Once a `json.NewDecoder(r.Body).Decode(...)` call consumes the body, the raw bytes are gone for HMAC computation.\n\nFix: read `r.Body` into `[]byte` first with `io.ReadAll`, validate the signature, then decode JSON from the same slice with `bytes.NewReader`. Also ensure you compare `\"sha256=\" + hex.EncodeToString(mac.Sum(nil))` against the `X-Hub-Signature-256` header.",
		codexUsage{PromptTokens: 95, CompletionTokens: 118, TotalTokens: 213}, 8))
	writeLine(f, codexInput("I'm already using io.ReadAll. Could it be the secret?", 20))
	writeLine(f, codexOutput("gpt-5",
		"Yes. Secrets in `.env` files often carry a trailing newline. Try: `strings.TrimSpace(os.Getenv(\"GITHUB_WEBHOOK_SECRET\"))`. Also consider returning HTTP 401 instead of 500 for signature failures — 500 implies a server error.",
		codexUsage{PromptTokens: 265, CompletionTokens: 72, TotalTokens: 337}, 23))
	writeLine(f, codexInput("That was it! Trailing newline in the .env file. Thanks.", 70))
	writeLine(f, codexOutput("gpt-5",
		"Classic. `strings.TrimSpace` is a good default for any secret read from env vars. Consider wrapping all secret loading in a helper that trims automatically.",
		codexUsage{PromptTokens: 380, CompletionTokens: 38, TotalTokens: 418}, 72))
}

func writeCodexSession002(dir string) {
	f := openFile(filepath.Join(dir, "codex", "session-002-with-tools.jsonl"))
	defer f.Close()

	sid := "cx-0002-0002-0002-0002-000000000002"
	epoch = time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)

	writeLine(f, codexMeta(sid, "gpt-5", "/Users/dev/projects/webhookservice", 0))
	writeLine(f, codexInput("The branch filter in our GitHub webhook handler drops valid pushes. Can you look at the handler code?", 5))
	writeLine(f, codexFunctionCall("cx-call-0002-001", "shell",
		map[string]any{"command": "cat /Users/dev/projects/webhookservice/internal/webhook/handler.go"}, 7))
	writeLine(f, codexFunctionOutput("cx-call-0002-001",
		"package webhook\n\ntype PushEvent struct {\n\tRef string `json:\"ref\"`\n}\n\nfunc HandlePush(w http.ResponseWriter, r *http.Request, allowedBranch string) {\n\tvar evt PushEvent\n\tjson.NewDecoder(r.Body).Decode(&evt)\n\tif evt.Ref != allowedBranch {\n\t\tw.WriteHeader(http.StatusNoContent)\n\t\treturn\n\t}\n\tprocessPush(evt)\n}\n",
		8))
	writeLine(f, codexOutput("gpt-5",
		"Found it. GitHub's `ref` is always `refs/heads/<branch>` (e.g. `refs/heads/main`), not just `main`. Your filter fails because `allowedBranch` is `\"main\"`. Let me check the config:",
		codexUsage{PromptTokens: 220, CompletionTokens: 48, TotalTokens: 268}, 9))
	writeLine(f, codexFunctionCall("cx-call-0002-002", "shell",
		map[string]any{"command": "grep -n 'ALLOWED_BRANCH\\|HandlePush' /Users/dev/projects/webhookservice/cmd/server/main.go"}, 10))
	writeLine(f, codexFunctionOutput("cx-call-0002-002",
		"38:    branch := os.Getenv(\"ALLOWED_BRANCH\")\n43:        webhook.HandlePush(w, r, branch)\n",
		11))
	writeLine(f, codexFunctionCall("cx-call-0002-003", "str_replace_editor",
		map[string]any{
			"command":  "str_replace",
			"path":     "/Users/dev/projects/webhookservice/internal/webhook/handler.go",
			"old_str":  "\tif evt.Ref != allowedBranch {",
			"new_str":  "\tbranch := strings.TrimPrefix(evt.Ref, \"refs/heads/\")\n\tif branch != allowedBranch {",
		}, 13))
	writeLine(f, codexFunctionOutput("cx-call-0002-003", "Applied edit to handler.go", 14))
	writeLine(f, codexFunctionCall("cx-call-0002-004", "shell",
		map[string]any{"command": "cd /Users/dev/projects/webhookservice && go build ./..."}, 15))
	writeLine(f, codexFunctionOutput("cx-call-0002-004", "", 17))
	writeLine(f, codexOutput("gpt-5",
		"Build clean. Handler now strips `refs/heads/` before comparing. Tag pushes are also correctly filtered out.",
		codexUsage{PromptTokens: 425, CompletionTokens: 35, TotalTokens: 460}, 18))
	writeLine(f, codexInput("Works great. Filter is correct.", 60))
	writeLine(f, codexOutput("gpt-5",
		"Add unit tests for both the matching case and the filter case to lock in the correct behavior.",
		codexUsage{PromptTokens: 490, CompletionTokens: 28, TotalTokens: 518}, 62))
}

func writeCodexSession003(dir string) {
	f := openFile(filepath.Join(dir, "codex", "session-003-with-compact.jsonl"))
	defer f.Close()

	sid := "cx-0003-0003-0003-0003-000000000003"
	epoch = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	writeLine(f, codexMeta(sid, "gpt-5", "/Users/dev/projects/webhookservice", 0))
	// Pre-compact messages (token count builds up)
	writeLine(f, codexInput("I need to add per-IP rate limiting to our webhook endpoint.", 5))
	writeLine(f, codexOutput("gpt-5",
		"Token bucket keyed by IP is ideal. I'll use `golang.org/x/time/rate` + `sync.Map`. Reading config and middleware files first.",
		codexUsage{PromptTokens: 88, CompletionTokens: 80, TotalTokens: 168}, 8))
	writeLine(f, codexFunctionCall("cx-call-0003-001", "shell",
		map[string]any{"command": "cat /Users/dev/projects/webhookservice/internal/middleware/middleware.go"}, 9))
	writeLine(f, codexFunctionOutput("cx-call-0003-001",
		"package middleware\nimport \"net/http\"\nfunc Logger(...) http.Handler { ... }\nfunc Recovery(...) http.Handler { ... }\n", 10))
	writeLine(f, codexFunctionCall("cx-call-0003-002", "shell",
		map[string]any{"command": "cat /Users/dev/projects/webhookservice/internal/config/config.go"}, 11))
	writeLine(f, codexFunctionOutput("cx-call-0003-002",
		"package config\ntype Config struct { Port int; Secret string; Branch string }\n", 12))
	writeLine(f, codexOutput("gpt-5",
		"Got both files. Adding RateLimit and Burst to Config, then writing the middleware.",
		codexUsage{PromptTokens: 340, CompletionTokens: 22, TotalTokens: 362}, 13))
	writeLine(f, codexFunctionCall("cx-call-0003-003", "str_replace_editor",
		map[string]any{
			"command": "str_replace",
			"path":    "/Users/dev/projects/webhookservice/internal/config/config.go",
			"old_str": "type Config struct { Port int; Secret string; Branch string }",
			"new_str": "type Config struct {\n\tPort int; Secret string; Branch string\n\tRateLimit float64 `toml:\"rate_limit\"`\n\tBurst int `toml:\"burst\"`\n}",
		}, 14))
	writeLine(f, codexFunctionOutput("cx-call-0003-003", "Applied edit to config.go", 15))
	writeLine(f, codexFunctionCall("cx-call-0003-004", "str_replace_editor",
		map[string]any{
			"command": "str_replace",
			"path":    "/Users/dev/projects/webhookservice/internal/config/config.go",
			"old_str": "func Default() Config { return Config{Port: 8080} }",
			"new_str": "func Default() Config { return Config{Port: 8080, Branch: \"main\", RateLimit: 10, Burst: 20} }",
		}, 16))
	writeLine(f, codexFunctionOutput("cx-call-0003-004", "Applied edit to config.go", 17))
	writeLine(f, codexFunctionCall("cx-call-0003-005", "shell",
		map[string]any{"command": "cd /Users/dev/projects/webhookservice && go build ./..."}, 25))
	writeLine(f, codexFunctionOutput("cx-call-0003-005", "", 27))
	writeLine(f, codexOutput("gpt-5",
		"Build passes. Rate limiter: sync.Map + rate.Limiter, realIP() respects X-Forwarded-For, 429 on limit exceeded.",
		codexUsage{PromptTokens: 780, CompletionTokens: 42, TotalTokens: 822}, 28))
	writeLine(f, codexInput("Session is getting large. Let's compact and continue.", 900))

	// --- Compact marker (synthetic — Codex's real signal is not documented; W5 adapts) ---
	writeLine(f, codexCompactEvent(sid, 18420, 2180,
		"Working on webhookservice Go project. Added per-IP token bucket rate limiting. Config.RateLimit (float64) and Config.Burst (int) added with defaults 10/20. NewRateLimiter middleware using sync.Map + rate.Limiter. Build passes. Next: wire into main.go.",
		902))

	// --- Post-compact messages (prompt_tokens dramatically lower — W15 heuristic) ---
	writeLine(f, codexInput("Now wire the rate limiter into main.go.", 910))
	writeLine(f, codexFunctionCall("cx-call-0003-006", "shell",
		map[string]any{"command": "cat /Users/dev/projects/webhookservice/cmd/server/main.go"}, 911))
	writeLine(f, codexFunctionOutput("cx-call-0003-006",
		"package main\nfunc main() {\n\tcfg := config.Default()\n\t...\n\thandler := middleware.Logger(middleware.Recovery(mux))\n\thttp.ListenAndServe(\":8080\", handler)\n}\n", 912))
	// prompt_tokens dramatically lower after compact — this is the W15 detection heuristic
	writeLine(f, codexOutput("gpt-5",
		"Wrapping the chain with the rate limiter as the outermost layer:",
		codexUsage{PromptTokens: 195, CompletionTokens: 18, TotalTokens: 213}, 913))
	writeLine(f, codexFunctionCall("cx-call-0003-007", "str_replace_editor",
		map[string]any{
			"command": "str_replace",
			"path":    "/Users/dev/projects/webhookservice/cmd/server/main.go",
			"old_str": "\thandler := middleware.Logger(middleware.Recovery(mux))",
			"new_str": "\trl := middleware.NewRateLimiter(cfg.RateLimit, cfg.Burst)\n\thandler := rl(middleware.Logger(middleware.Recovery(mux)))",
		}, 914))
	writeLine(f, codexFunctionOutput("cx-call-0003-007", "Applied edit to main.go", 915))
	writeLine(f, codexOutput("gpt-5",
		"Done. Chain: RateLimiter → Logger → Recovery → mux. Rate-limited requests return 429 before logging.",
		codexUsage{PromptTokens: 295, CompletionTokens: 28, TotalTokens: 323}, 917))
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	// Determine output directory: the same directory as this script.
	// When invoked as `go run examples/sample-jsonl/generate.go` from the
	// repo root, we write relative to the current working directory.
	dir := filepath.Join("examples", "sample-jsonl")

	fmt.Println("Generating Claude fixtures...")
	writeClaudeSession001(dir)
	// Reset epoch between sessions (writeClaudeSession002/003 set their own)
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	writeClaudeSession002(dir)
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	writeClaudeSession003(dir)

	fmt.Println("Generating Codex fixtures...")
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	writeCodexSession001(dir)
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	writeCodexSession002(dir)
	epoch = time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	writeCodexSession003(dir)

	fmt.Println("Done. Files written to", dir)
	fmt.Println("  claude/session-001-simple.jsonl")
	fmt.Println("  claude/session-002-with-tools.jsonl")
	fmt.Println("  claude/session-003-with-compact.jsonl")
	fmt.Println("  codex/session-001-simple.jsonl")
	fmt.Println("  codex/session-002-with-tools.jsonl")
	fmt.Println("  codex/session-003-with-compact.jsonl")
}

// Ensure unused import warning doesn't appear — mustMarshal is a utility
// kept for callers who need inline JSON strings.
var _ = mustMarshal
