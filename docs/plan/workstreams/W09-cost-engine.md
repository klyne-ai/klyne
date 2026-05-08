# W9 · Cost Engine + Pricing Table

> **Wave:** 1 · **Effort:** S · **Depends on:** W0, W6 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths".

---

## Goal

Embed a LiteLLM-style pricing JSON in the binary; provide `Lookup(model)` for per-token rates and `Cost(tokensIn, tokensOut, model)` for USD calculation. Support per-session, per-project, per-day, per-model rollups. Allow `~/.klyne/pricing.json` to override embedded values.

---

## Spec sections

- §7 (cost fields in `Message` / `Session`)
- §10 (LiteLLM-style table)
- §15 D7 acceptance: per-session $ matches `ccusage` to within 0.5%

---

## Owned paths

```
internal/cost/pricing.go
internal/cost/pricing_test.go
internal/cost/pricing.json          (embedded via //go:embed)
internal/cost/refresh.go            (loads override from ~/.klyne/pricing.json if present)
internal/cost/refresh_test.go
```

---

## Inputs

- W0 `internal/cost/pricing_schema.go` (typed `PricingTable` struct).
- W6 config (for `PricingOverridePath()`).

---

## Outputs

```go
package cost

type Engine struct { /* ... */ }

func New(cfg *config.Config) (*Engine, error)

func (e *Engine) Lookup(model string) (PerTokenRates, bool)

func (e *Engine) Cost(tokensIn, tokensOut int64, model string) float64
// Returns USD; logs warning + returns 0 if model unknown

func (e *Engine) RollupSession(ctx context.Context, db *store.DB, sessionID string) (float64, error)
func (e *Engine) RollupProject(ctx context.Context, db *store.DB, path string, since int64) (float64, error)
func (e *Engine) RollupDaily(ctx context.Context, db *store.DB, date time.Time) (float64, error)
func (e *Engine) RollupByModel(ctx context.Context, db *store.DB, since int64) (map[string]float64, error)
```

---

## Required model coverage in `pricing.json` (v1)

At minimum:
- `claude-sonnet-4.5`, `claude-haiku-4`, `claude-opus-4.6`
- `gpt-5`, `gpt-5-mini`, `gpt-5-nano`
- `gemini-2.5-flash`, `gemini-2.5-flash-lite`
- `text-embedding-3-small`
- `llama3.1:8b` (free / 0.0)

Pull current rates from each vendor's pricing page (Anthropic, OpenAI, Google AI). Document source URLs in a comment at the top of `pricing.json`.

---

## Acceptance criteria

- [ ] All listed models present in `pricing.json` with non-zero rates (except `llama3.1:8b`).
- [ ] `Cost(tokensIn, tokensOut, model)` returns USD; **0 with a warning log if model unknown** (not an error).
- [ ] **Per-session $ matches `ccusage` to within 0.5% on a fixture** (the spec D7 acceptance).
- [ ] Override file at `~/.klyne/pricing.json` takes precedence; tests cover both paths.
- [ ] Rollups (`RollupSession`, `RollupProject`, `RollupDaily`, `RollupByModel`) return correct sums.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

1. `TestLookup_KnownModel` — returns expected rates.
2. `TestLookup_UnknownModel` — returns `false`.
3. `TestCost_KnownModel` — table-driven across 4–5 models, asserts USD output.
4. `TestCost_UnknownModel_LogsAndReturnsZero`.
5. `TestOverride_FromConfigDir` — temp config dir with `pricing.json` override; assert override values used.
6. `TestRollupSession_MatchesCCusage` — golden file with token totals from `ccusage` output; assert within 0.5%.
7. `TestRollupProject_MultiSession` — 3 sessions in same project; sum is correct.
8. `TestRollupDaily_BoundaryHandling` — events at 23:59:59.999 and 00:00:00.000 land in correct day.

---

## Hard boundaries

- Do **NOT** modify `pricing_schema.go` (W0-owned).
- Do **NOT** call any AI provider — cost is pure math against the table.
- Do **NOT** touch `internal/store/**` (W1/W2/W3).

---

## Done

When W7's `/cost/summary` handler can call your rollups and the W16 perf bench shows accurate $ per session matching the `ccusage` golden file.
