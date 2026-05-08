# W11 · Smart Selector + Summarize / Title Tasks

> **Wave:** 2 · **Effort:** M · **Depends on:** W0, W3, W8, W10 · **Recommended skills:** `general-purpose` + `cost-aware-llm-pipeline` + `claude-api`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths".

---

## Goal

Implement the smart BYOK selector per spec §8 plus the summarize/title tasks. Wire a runner goroutine that subscribes to `MsgNew` SSE events and triggers summarization every 50 messages or on a `CompactDetected` event.

---

## Spec sections

- §6 flow C (`/compact` recovery)
- §7 step 4 (summarizer worker)
- §8 (the BYOK matrix — verbatim source for selector logic)

---

## Owned paths

```
internal/ai/selector.go
internal/ai/selector_test.go
internal/ai/tasks/summarize.go
internal/ai/tasks/summarize_test.go
internal/ai/tasks/title.go
internal/ai/tasks/title_test.go
internal/ai/tasks/runner.go         (subscribes to MsgNew; triggers summarize/title)
internal/ai/tasks/runner_test.go
```

---

## Inputs

- W0 config schema (`ai.summary_model`, `ai.title_model`, `ai.embed_model`).
- W3 summaries DAO (`InsertSummary`, `LatestSummary`).
- W8 SSE hub (subscribe to `MsgNew`, publish `SummaryReady`).
- W10 providers + `DetectAvailable()`.

---

## Outputs

```go
package ai

type TaskKind int
const (
    TaskSummarize TaskKind = iota
    TaskTitle
    TaskEmbed       // v1.1 stub now to avoid drift
)

type Choice struct {
    Provider string
    Model    string
    Reason   string  // user-readable for wizard
}

func Pick(task TaskKind, available []ProviderInfo) (Choice, error)

// tasks/runner.go
type Runner struct { /* ... */ }

func NewRunner(deps RunnerDeps) *Runner
func (r *Runner) Start(ctx context.Context) error
func (r *Runner) Stop() error

// Internally:
//   subscribe to hub; on each MsgNew, count messages per session in-memory;
//   every 50 messages OR on CompactDetected → enqueue summarize task;
//   summarize task fetches last 50 messages (+ prior summary if any);
//   calls selected provider; persists via store.InsertSummary;
//   publishes SummaryReady.
```

---

## Selector rules (verbatim from spec §8)

For each task, preference order (first match wins):

| Task | Preference order |
|---|---|
| `Summarize` | Gemini Flash-Lite → OpenAI gpt-5-mini → Anthropic claude-haiku-4 → Ollama llama3.1:8b |
| `Title`     | Gemini Flash-Lite → gpt-5-nano → claude-haiku-4 → llama3.1:8b |
| `Embed`     | OpenAI text-embedding-3-small → Gemini text-embedding-004 → local nomic-embed-text via Ollama |

`config.ai.summary_model` etc. can override:
- `"auto"` → use selector preference order
- `"<provider>:<model>"` → force that provider/model (validate provider available; else fall back with a warning)

**Reason strings** must be user-readable — they appear in the wizard's "why this was picked" badge.

---

## Acceptance criteria

- [ ] Selector unit tests cover the **full §8 matrix exhaustively** (3 tasks × 16 provider availability permutations).
- [ ] Reason strings are user-readable (e.g., `"Gemini Flash-Lite picked: free tier, 1,000 RPD covers your workload"`).
- [ ] Runner end-to-end test: feed mock providers, 50-message fixture → summary persisted via W3 → `SummaryReady` event broadcast via W8.
- [ ] Runner correctly batches: 50 messages triggers exactly one summarize call, not 50.
- [ ] Runner debounces on `CompactDetected` — multi-emit within 500ms produces only one summary.
- [ ] **Summaries never run inline with the message writer** (mitigates risk R7) — confirm by goroutine inspection in tests.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

Selector:
1. `TestPick_FullMatrix` — table-driven over `(task, available providers)` pairs.
2. `TestPick_ConfigOverride_Honored`.
3. `TestPick_ConfigOverride_FallsBackOnUnavailable`.
4. `TestPick_NoneAvailable_Errors`.

Summarize task:
5. `TestSummarize_50MessageWindow_PersistsAndPublishes` — mock provider, mock hub, asserts both side-effects.
6. `TestSummarize_RollingPriorSummary` — second invocation includes prior summary in prompt.
7. `TestSummarize_ProviderError_RetriesOnce` — first call 5xx, second succeeds.

Runner:
8. `TestRunner_BatchOf50` — emit 50 `MsgNew` for one session → exactly one summarize.
9. `TestRunner_CompactTriggersImmediate` — emit `CompactDetected` → summarize fires immediately.
10. `TestRunner_PerSessionCounters` — interleaved sessions don't trigger summaries on the wrong one.

---

## Hard boundaries

- Do **NOT** implement Embed (`tasks/embed.go`) — it's v1.1, opt-in. Stub the type but don't ship the task.
- Do **NOT** publish your own SSE events directly to the network — go through W8's hub.
- Do **NOT** modify W10's provider implementations.
- Do **NOT** modify W3's summaries DAO.

---

## Done

When the daemon (W12) wires you in, summaries appear in the SQLite DB and stream as `SummaryReady` events to a connected EventSource client during a real Claude Code session.
