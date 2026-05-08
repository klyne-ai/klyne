# W10 · AI Providers (Anthropic / OpenAI / Gemini / Ollama)

> **Wave:** 1 · **Effort:** M · **Depends on:** W0 · **Recommended skills:** `general-purpose` + `claude-api`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. **§18 decisions are LOCKED.** TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths".

> **CRITICAL legal constraint (spec §8):** Anthropic enforces, fully effective **2026-04-04**, that Claude Code OAuth tokens cannot be reused by third-party tools. **You must NEVER read `~/.claude` OAuth tokens.** API-key only. The `Anthropic` provider hard-fails with `ErrNoCredential` if `ANTHROPIC_API_KEY` is unset, even if `~/.claude` exists.
>
> Same conservative stance for OpenAI: **NEVER read `~/.codex/auth.json`** (spec §17 #3).

---

## Goal

Implement the `Provider` interface and 4 implementations: Anthropic, OpenAI, Gemini, Ollama. **API-key (or local Ollama) only.** Implement `DetectAvailable()` that checks env vars + Ollama localhost.

---

## Spec sections

- §8 (the BYOK matrix and legal constraints)
- §17 (non-goals — no OAuth re-use)
- §18 (locked decisions)

---

## Owned paths

```
internal/ai/provider.go                       (interface + ChatRequest, ChatResponse, EmbedRequest, EmbedResponse, ProviderInfo)
internal/ai/providers/anthropic.go
internal/ai/providers/anthropic_test.go
internal/ai/providers/openai.go
internal/ai/providers/openai_test.go
internal/ai/providers/gemini.go
internal/ai/providers/gemini_test.go
internal/ai/providers/ollama.go
internal/ai/providers/ollama_test.go
internal/ai/providers/detect.go               (DetectAvailable())
internal/ai/providers/detect_test.go
```

---

## Inputs

- Spec §8 matrix.
- W0 `internal/config/schema.go` (knows which envs are read).

---

## Outputs

```go
package ai

type Provider interface {
    Name() string                                                          // "anthropic" | "openai" | "gemini" | "ollama"
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)   // optional; return ErrUnsupported if not
    Models() []string                                                      // list of models the user could pick
}

type ChatRequest struct {
    Model       string
    Messages    []Message  // role + content
    MaxTokens   int
    Temperature float64
    SystemPrompt string
}

type ChatResponse struct {
    Text       string
    TokensIn   int64
    TokensOut  int64
    Model      string
    StopReason string
}

type ProviderInfo struct {
    Name           string
    Available      bool
    Reason         string  // user-readable; e.g., "ANTHROPIC_API_KEY set"
    Models         []string
    SupportsEmbed  bool
}

// providers/detect.go
func DetectAvailable(ctx context.Context) []ProviderInfo
```

---

## Detection rules (spec §8)

`DetectAvailable()` returns:
- `anthropic` available iff env `ANTHROPIC_API_KEY` set.
- `openai` available iff env `OPENAI_API_KEY` set.
- `gemini` available iff env `GEMINI_API_KEY` set.
- `ollama` available iff HTTP GET `http://localhost:11434/api/tags` returns 200 within 1s timeout.

**Never read `~/.claude` or `~/.codex/auth.json`.**

---

## Acceptance criteria

- [ ] Each provider has tests against `httptest.Server` mocking the upstream API. **No real network calls in CI.**
- [ ] **Anthropic provider hard-fails (`ErrNoCredential`) if `ANTHROPIC_API_KEY` is unset, even if `~/.claude` exists.** Document this in a comment citing spec §8 enforcement date.
- [ ] **Ollama provider degrades gracefully** if `localhost:11434` is unreachable — `Chat` returns `ErrProviderUnavailable`.
- [ ] `DetectAvailable` returns user-readable reason strings (used in the wizard UI by W15).
- [ ] All providers honor `ctx` cancellation.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

Per provider:
1. `TestChat_Success` — mock 200 response with realistic shape; assert `ChatResponse` parsed.
2. `TestChat_APIError` — mock 4xx/5xx; assert error.
3. `TestChat_ContextCanceled` — cancel before response; assert `context.Canceled` returned.
4. `TestChat_NoCredential` — env unset; assert `ErrNoCredential`.

For Anthropic specifically:
5. `TestAnthropic_NeverReadsOAuth` — set `~/.claude` fixture; without `ANTHROPIC_API_KEY`, provider returns `ErrNoCredential` and never opens any file under `~/.claude`.

For Ollama:
6. `TestOllama_LocalhostUnreachable` — port closed; `DetectAvailable()` reports unavailable, `Chat` returns `ErrProviderUnavailable`.

For `DetectAvailable`:
7. `TestDetect_AllCombinations` — env permutations; assert reason strings are stable.

Use the `claude-api` skill to confirm the Anthropic Messages API request/response shape.

---

## Hard boundaries

- Do **NOT** implement the selector (W11). You provide the provider tools; the selector chooses.
- Do **NOT** implement summarize / title tasks (W11).
- Do **NOT** read `~/.claude` for any reason.
- Do **NOT** read `~/.codex/auth.json` for any reason.

---

## Done

When W11's selector can call `DetectAvailable()` and dispatch chat requests to the chosen provider, and the wizard UI (W15) can display reason strings.
