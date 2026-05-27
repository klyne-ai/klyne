# Productivity Compile — Model Picker

**Date**: 2026-05-27
**Status**: Design approved, ready for implementation plan
**Owner**: mohitpatel

## Problem

The productivity dashboard's "Compile" button (per-day, per-service) shells out to `claude -p /klyne:productivity-sync` with the model **hardcoded to `claude-opus-4-7`** at `internal/api/handlers/productivity_compile.go:210`. An A/B run on 2026-05-26 across the same `(operations-app, 2026-05-26)` data showed:

- Sonnet 4.6 and Opus 4.7 produce structurally identical card sets (same tickets, same SHIPPED/FIXED splits, same filtered meta turns).
- Opus's prose is marginally more identifier-dense; Sonnet's ref discipline is marginally cleaner.
- Opus costs roughly **7× more per run** (~$1.40 vs ~$0.16, driven by 5× per-token price plus ~1.4× token volume).

For a recurring per-day synthesis the user re-reads at standup, Sonnet's quality is sufficient and the cost delta is not justified. But there are days where the user wants the densest synthesis Opus produces (weekly recaps, high-stakes summaries). The current code offers no override — flipping models requires editing the Go source.

## Goal

Give the productivity page a model picker. **Default Sonnet**, opt-in Opus. Selection persists across page reloads. Applies only to **future compile runs** — does not retroactively re-compile existing cards.

## Non-goals

- Tracking which model produced each card (no `compiled_by_model` schema add). The picker controls future compiles; flipping it silently replaces older cards on re-compile.
- Adding more models than Sonnet and Opus (Haiku is out of scope; we'd add later if a use case appeared).
- Switching the underlying transport — keeps using the local `claude` CLI as today; no Anthropic API direct integration.
- Forking productivity-sync into model-specific prompts. The same slash command body runs against both models.

## Design

### Backend (Go)

**File**: `internal/api/handlers/productivity_compile.go`

1. Extend the request struct:
   ```go
   type productivityCompileRequest struct {
       ProjectPath string `json:"project_path"`
       Day         string `json:"day"`
       Model       string `json:"model,omitempty"` // "sonnet" | "opus", default "sonnet"
   }
   ```

2. Add a small allowlist map at package scope:
   ```go
   var compileModelAllowlist = map[string]string{
       "sonnet": "claude-sonnet-4-6",
       "opus":   "claude-opus-4-7",
   }
   const defaultCompileModel = "sonnet"
   ```

3. In `Run`, after parsing the request, resolve `req.Model`:
   - Empty → use `defaultCompileModel`.
   - Present but not in `compileModelAllowlist` → return `400 Bad Request` with body `"unknown model: <value>"`.
   - Otherwise → keep as-is and look up the model id when spawning.

4. Change `runCmd` signature from `func(ctx, projectPath, day string)` to `func(ctx, projectPath, day, modelKey string)`. Pass the validated `modelKey` through.

5. In `spawnClaudeProductivitySync`:
   - Remove the `const syncModel = "claude-opus-4-7"` line.
   - Look up the model id: `modelID := compileModelAllowlist[modelKey]`.
   - Pass `modelID` to the `--model` flag of the `claude` CLI exactly as today.
   - Update the existing 7-line comment block (lines 204–209) to read: "Model is caller-supplied, validated against `compileModelAllowlist` in `Run`. Default is `sonnet` (2026-05-27 — flipped from Opus after A/B showed Sonnet is sufficient for the daily card at ~1/7 the cost; users can opt into Opus from the productivity page header)."

### Frontend (Svelte / TS)

**Files**: `ui/src/routes/productivity/+page.svelte`, `ui/src/lib/api.ts`

1. **API wrapper** — `ui/src/lib/api.ts`:
   - Update `compileProductivity(projectPath: string, day: string)` to `compileProductivity(projectPath: string, day: string, model: "sonnet" | "opus")`.
   - Add `model` to the POST body: `{ project_path, day, model }`.

2. **Page state** — `ui/src/routes/productivity/+page.svelte`:
   - Add reactive state at the top:
     ```svelte
     const COMPILE_MODEL_KEY = 'klyne.productivity.compile_model';
     let compileModel = $state<'sonnet' | 'opus'>('sonnet');
     ```
   - On mount: read `localStorage.getItem(COMPILE_MODEL_KEY)`; if it's `'sonnet'` or `'opus'`, hydrate `compileModel`. Otherwise keep `'sonnet'`.
   - On change: write `localStorage.setItem(COMPILE_MODEL_KEY, compileModel)`.

3. **Picker UI** — page header:
   - Two-segment control rendered in the existing header strip (top of the productivity page, NOT inside per-day cards).
   - Markup sketch:
     ```svelte
     <div class="model-picker" role="radiogroup" aria-label="Compile model">
       <button
         role="radio"
         aria-checked={compileModel === 'sonnet'}
         class="pill" class:pill-active={compileModel === 'sonnet'}
         onclick={() => { compileModel = 'sonnet'; persistModel(); }}>
         Sonnet
       </button>
       <button
         role="radio"
         aria-checked={compileModel === 'opus'}
         class="pill" class:pill-active={compileModel === 'opus'}
         onclick={() => { compileModel = 'opus'; persistModel(); }}>
         Opus
       </button>
       <span class="model-picker-hint">
         {compileModel === 'opus' ? '~7× cost vs Sonnet' : 'recommended default'}
       </span>
     </div>
     ```
   - Reuses existing `.pill` / `.pill-active` styles; one new `.model-picker-hint` rule (muted small text).

4. **Wire to compile call** — `runCompileForAllPendingServices`:
   - Existing line `await compileProductivity(p, dayStr);` becomes `await compileProductivity(p, dayStr, compileModel);`.
   - No other change to the loop, error handling, or progress UI.

### Data flow

```
Page mount
  → read localStorage 'klyne.productivity.compile_model'
  → hydrate compileModel state (default 'sonnet')

User clicks "Sonnet" or "Opus" pill in header
  → compileModel updated
  → localStorage written

User clicks per-day "Compile (N pending)" button
  → runCompileForAllPendingServices() loops over pending services
  → for each: compileProductivity(projectPath, day, compileModel)
  → POST /api/productivity/compile { project_path, day, model: compileModel }

Backend
  → ProductivityCompileHandler.Run validates model against allowlist
  → spawnClaudeProductivitySync(ctx, projectPath, day, modelKey)
  → claude -p --model <resolved id> /klyne:productivity-sync ...
  → parseClaudeRunResult records token usage tagged with the actual model id
  → response → page refetches productivity → new card renders
```

### Error handling

- **Unknown model** (UI sends a value backend doesn't know): backend returns `400` with `"unknown model: <value>"`. UI surfaces in the existing `compileError` state — same pattern as today's "Compile" failures.
- **localStorage unavailable** (Safari private mode, etc.): the try/catch around read+write falls back to in-memory state; user can still pick a model, it just doesn't persist past reload.
- **Old localStorage value** (corrupted, future-introduced model removed): if the hydrated value isn't `'sonnet'` or `'opus'`, drop it and default to `'sonnet'`.

### Testing

- **Backend**: extend `internal/api/handlers/productivity_compile_test.go`:
  - New test: empty `model` → handler passes `"sonnet"` to `runCmd`.
  - New test: `model: "opus"` → handler passes `"opus"` to `runCmd`.
  - New test: `model: "haiku"` → 400 response, body contains `"unknown model"`.
  - Existing tests: verify they still pass with the new signature (likely just need a placeholder model arg in the stub `runCmd`).

- **Frontend**: manual smoke is enough — no Svelte unit tests in this codebase for the productivity page today. The flow is:
  1. Reload page, picker reads `Sonnet`.
  2. Click `Opus`, reload page, picker still reads `Opus`.
  3. Click `Sonnet`, trigger a Compile, watch `claude-usage` tile show Sonnet token spend.
  4. Click `Opus`, trigger a Compile on a different day, watch tile show Opus token spend.

## What this does NOT change

- The `record_productivity_card` MCP tool. Unchanged.
- The `productivity-sync` slash command body. Unchanged.
- The `productivity_snapshots` table schema. Unchanged.
- The `WhatWasDoneCard.svelte` render. Unchanged.
- Token-usage tile. Continues to record per-run spend tagged with the actual model id the CLI ran (it already pulls from `parseClaudeRunResult`).

## Open follow-ups (not in scope)

- **"Compiled by Sonnet/Opus" badge on cards**: needs `compiled_by_model` column on `productivity_snapshots` + render chip on the card. Defer until we actually want to A/B over time.
- **Cost estimate next to the picker hint**: show actual $/run from the usage tile instead of the static "~7× cost". Defer until the usage tile exposes a per-(project, day, model) breakdown.
- **Per-service override** (rare project where Opus is worth it but the rest aren't): would need either a per-service preference or a "use Opus for this service this time" modal. No demand yet.
