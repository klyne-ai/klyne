# W13 · Frontend Foundation (SvelteKit Shell + lib + stores)

> **Wave:** 1 · **Effort:** M · **Depends on:** W0 · **Recommended skills:** `coding-standards` + `frontend-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. **§18 decisions are LOCKED** (SvelteKit 5, Tailwind 4, adapter-static, no React). TDD-first; ≥80% coverage on `lib/`. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Need a Go DTO field? Open a `contract-change` PR against W0's `contracts.go`.

---

## Goal

SvelteKit 5 scaffold with Tailwind 4, runes-based stores, typed API client, typed SSE client, and a CI-enforced contract check that asserts TS types match Go DTOs.

**No business components yet** — those are W14.

---

## Spec sections

- §10 (versions)
- §11 (file layout)
- §18 #2, #10 (Svelte locked, Tailwind defaults only)

---

## Owned paths

```
ui/package.json
ui/svelte.config.js               (adapter-static)
ui/vite.config.ts
ui/tsconfig.json
ui/tailwind.config.ts
ui/postcss.config.js
ui/.eslintrc.cjs
ui/src/app.html
ui/src/app.css
ui/src/lib/api.ts                 (typed fetch wrappers; types mirror W0 contracts.go)
ui/src/lib/api.test.ts
ui/src/lib/sse.ts                 (EventSource client; reconnect; types mirror W0 sse_events.go)
ui/src/lib/sse.test.ts
ui/src/lib/types.ts               (TS mirror of Message, Session, Summary, etc.)
ui/src/lib/stores.svelte.ts
ui/src/lib/stores.svelte.test.ts
ui/src/routes/+layout.svelte      (just shell; navigation in W14)
ui/src/routes/+page.svelte        (placeholder "loading…" — W14 overwrites)
ui/scripts/check-contracts.ts     (consumes tools/dump-contracts/ output; asserts TS matches Go)
```

---

## Inputs

- W0's `internal/api/contracts.go` and `internal/api/sse_events.go` (read-only Go source; translate to TS).
- W0's `tools/dump-contracts/` produces a JSON dump of all Go DTO types — `check-contracts.ts` consumes that JSON and validates `types.ts`.

---

## Outputs

- `npm run build` produces `ui/build/` ready for Go's `embed.FS` (W12 embeds it).
- `api.ts` exports typed fetch helpers for every route in `contracts.go`.
- `sse.ts` exports typed `subscribe(handler)` for every event in `sse_events.go`.
- `stores.svelte.ts` exports runes-based stores: `sessions`, `currentSession`, `searchResults`, `costSummary`, `settings`.

---

## Acceptance criteria

- [ ] `npm run build` succeeds; output goes to `ui/build/`.
- [ ] `npm run check` (svelte-check) clean.
- [ ] `npm run test` (vitest) passes; **80%+ coverage on `lib/`.**
- [ ] **Contract check enforced:** `ui/scripts/check-contracts.ts` runs in `npm run check` and asserts every Go DTO field is mirrored in the corresponding TS type. Failing field → CI red. (Mitigates risk R4.)
- [ ] `api.ts` and `sse.ts` are typed end-to-end — no `any`.
- [ ] Bundle size < 60 KB gzipped (sanity check; spec mentions <50 KB but full check is W16).
- [ ] **No business components** (`SessionList`, `SessionView`, etc.) — those are W14.

---

## Testing requirements (TDD-first)

Vitest + `@testing-library/svelte`.

1. `api.ts`:
   - `TestFetchSessions_HappyPath` — mock fetch; assert response typed.
   - `TestFetchSessions_4xx_Throws` — mock 400; assert thrown error has DTO.
   - `TestSearch_QueryEncoded`.
2. `sse.ts`:
   - `TestSubscribe_DispatchesByEventName` — mock EventSource; emit `MsgNew`; handler receives typed payload.
   - `TestSubscribe_ReconnectsOnClose`.
3. `stores.svelte.ts`:
   - `TestSessionsStore_UpdatesOnMsgNew`.
   - `TestSearchResultsStore_ReplacesOnNewQuery`.
4. `check-contracts.ts`:
   - Snapshot test against the W0 dump-contracts JSON.

---

## Hard boundaries

- **No business components.** `SessionList`, `SessionView`, `SearchBar`, `CostPanel`, `MessageBubble`, `RestoreContext`, `ModelPicker`, `Wizard/*` — all owned by W14 (most) and W15 (wizard + restore).
- **No business routes.** Only `+layout.svelte` and a placeholder `+page.svelte`.
- Do **NOT** edit Go contract files — open a `contract-change` PR.
- Do **NOT** add a state library (Redux, Zustand, etc.) — Svelte 5 runes are sufficient (spec §10).

---

## Done

When W14 can `import { fetchSessions, search } from '$lib/api'` and `import { sessionsStore } from '$lib/stores.svelte'` and build the dashboard against your scaffold without changes.
