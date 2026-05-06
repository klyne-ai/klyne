# W14 · Frontend Components + Routes (sessions / search / cost)

> **Wave:** 2 · **Effort:** L · **Depends on:** W13, W7, W8 · **Recommended skills:** `frontend-patterns` + `coding-standards` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. **§18 decisions are LOCKED** (Tailwind defaults only — no design system in v1). TDD-first; 80%+ component coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Need a new lib helper? Open a PR against W13.

---

## Goal

Implement v1 user-facing pages: dashboard (session list), session detail, search, and the cost panel. Live updates via SSE. Empty states everywhere.

**Wizard and restore-context modal are W15, not here.**

---

## Spec sections

- §6 flows A, B, D (dashboard / daily / find old thread). Flow C (`/compact` recovery) is **W15**.

---

## Owned paths

```
ui/src/lib/components/SessionList.svelte
ui/src/lib/components/SessionList.test.ts
ui/src/lib/components/SessionView.svelte
ui/src/lib/components/SessionView.test.ts
ui/src/lib/components/SearchBar.svelte
ui/src/lib/components/SearchBar.test.ts
ui/src/lib/components/CostPanel.svelte
ui/src/lib/components/CostPanel.test.ts
ui/src/lib/components/MessageBubble.svelte
ui/src/lib/components/MessageBubble.test.ts
ui/src/lib/components/ToolCallBlock.svelte
ui/src/lib/components/ToolCallBlock.test.ts
ui/src/routes/+page.svelte                   (overwrites W13's placeholder — dashboard)
ui/src/routes/sessions/[id]/+page.svelte
ui/src/routes/search/+page.svelte
```

---

## Inputs

- W13 lib: `$lib/api`, `$lib/sse`, `$lib/stores.svelte`, `$lib/types`.
- W7 backend: `/sessions`, `/sessions/:id/messages`, `/search`, `/cost/summary`.
- W8 backend: `/events` SSE stream.

---

## Outputs

Functional pages:
- `/` — dashboard with session list (live-updating), top-bar search, cost summary panel.
- `/sessions/:id` — full message tree (user, assistant, tool calls, tool results), live-updating as new messages arrive.
- `/search` — search results page, click-through to session detail with the matched message highlighted.

---

## Acceptance criteria

- [ ] All four spec §6 flows except C (A install/dashboard, B daily, D find old thread) work end-to-end against a running daemon.
- [ ] **80%+ component coverage** with Vitest + `@testing-library/svelte`.
- [ ] **Empty states present** for: no sessions, no search results, no cost data, network down.
- [ ] **Loading states** for slow networks (skeleton UI).
- [ ] **Error states** for 5xx responses.
- [ ] **Keyboard shortcuts:** `/` focuses search, `j`/`k` navigates session list, `Enter` opens highlighted session, `Esc` clears search.
- [ ] **Live updates:** new `MsgNew` event updates the session list (last_msg_at) and, if open, the session detail view in <1s.
- [ ] **Tailwind defaults only** — no custom design system in v1 (spec §18 #10).
- [ ] Mobile-narrow layout doesn't break (responsive Tailwind).
- [ ] All four flows verified manually in a browser before claiming done — see "Frontend testing" rule below.

---

## Testing requirements

Vitest + `@testing-library/svelte`. TDD per component.

Per component:
1. Renders happy path (props supplied).
2. Renders empty state.
3. Renders error state.
4. User interactions (click, keypress) dispatch expected events.

Routes:
5. `+page.svelte` (dashboard) integrates `SessionList`, `SearchBar`, `CostPanel`, subscribes to SSE, updates store on `MsgNew`.
6. `sessions/[id]/+page.svelte` fetches session + messages, subscribes for live updates.
7. `search/+page.svelte` debounces input (300ms), shows results.

**Manual browser verification (mandatory before merge):** start daemon, open `localhost:7878`, walk through flows A, B, D. Type-checking ≠ feature-checking.

---

## Hard boundaries

- Do **NOT** implement the wizard, restore-context modal, or model picker — those are **W15**.
- Do **NOT** modify `$lib/*` files — open a PR against W13 if a helper is missing.
- Do **NOT** introduce a CSS framework other than Tailwind (e.g., DaisyUI, Skeleton). Tailwind defaults only.
- Do **NOT** introduce a state library — runes only.

---

## Done

When W15 can `import` your `MessageBubble`, `ToolCallBlock`, `CostPanel` (read-only) and the daemon (W12) running with fixture data drives a fully functional dashboard.
