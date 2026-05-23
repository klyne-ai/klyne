# Self-contained Insights drill-in — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Insights tab self-contained — drilling Insights → project → session → back stays on the Insights tab — by extracting `ProjectDetail`/`SessionDetail` components and adding `/insights/projects/[name]` + `/insights/sessions/[id]` sub-routes, while the existing Work-tab `/projects` & `/sessions` routes keep working unchanged.

**Architecture:** The only genuinely new logic is context-aware link construction; it goes into a pure `src/lib/navlinks.ts` module (TDD, matches the existing `src/lib/*.test.ts` pure-function test pattern). The two large route pages are moved verbatim into `lib/ui/ProjectDetail.svelte` / `lib/ui/SessionDetail.svelte` components that take their identifier + a `basePath`/`backHref` via props; each route page (old and new) becomes a ~10-line wrapper. Three `goto` calls in `insights/+page.svelte` repoint to `/insights/...`.

**Tech Stack:** SvelteKit 2 + Svelte 5 (runes), TypeScript, Vitest (`npm run test`), `svelte-check` (`npm run check`), Vite build (`npm run build`). All commands run from `ui/`.

**Spec:** [`docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md`](../specs/2026-05-19-insights-self-contained-drill-in-design.md)

---

## Background the engineer must know

- Work in `ui/`. `npm run test` = `vitest run`. `npm run check` = `svelte-check` + a contracts script. `npm run build` compiles SvelteKit routes (catches missing/broken route files).
- Existing tests are all **pure-module** tests in `src/lib/*.test.ts` (vitest + `describe/it/expect`). There are **no component-render tests** in this codebase, and adding SvelteKit-module-mocked component tests is out of scope. Therefore the only automated test added here is for the pure `navlinks.ts` helpers; component extraction is verified by `npm run check` + `npm run build` + explicit manual browser steps. This matches the spec's testing intent (assert link construction honours `basePath`/`backHref`) by testing the unit where that logic actually lives.
- Svelte 5 runes props syntax used in this repo: `let { foo, bar = 'default' }: { foo: string; bar?: string } = $props();`
- The TopNav classifier (`src/lib/ui/TopNav.svelte:36-42`) already routes `/insights/*` to the Insights tab and a path like `/insights/projects/x` does NOT match `startsWith('/projects')`. **Do not modify TopNav.**

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `ui/src/lib/navlinks.ts` | **create** | Pure link builders: `sessionHref`, `projectHref`, `sessionBackHref`. The only new logic. |
| `ui/src/lib/navlinks.test.ts` | **create** | Vitest unit tests for the three helpers across Work (`''`) and Insights (`'/insights'`) base paths. |
| `ui/src/lib/ui/ProjectDetail.svelte` | **create** | All current `routes/projects/[name]/+page.svelte` logic+markup; props `{projectName, basePath, backHref}`. |
| `ui/src/lib/ui/SessionDetail.svelte` | **create** | All current `routes/sessions/[id]/+page.svelte` logic+markup; props `{sessionId, basePath}`. |
| `ui/src/routes/projects/[name]/+page.svelte` | **replace** | Thin wrapper → `<ProjectDetail … basePath="" backHref="/projects" />`. |
| `ui/src/routes/sessions/[id]/+page.svelte` | **replace** | Thin wrapper → `<SessionDetail … basePath="" />`. |
| `ui/src/routes/insights/projects/[name]/+page.svelte` | **create** | Thin wrapper → `<ProjectDetail … basePath="/insights" backHref="/insights" />`. |
| `ui/src/routes/insights/sessions/[id]/+page.svelte` | **create** | Thin wrapper → `<SessionDetail … basePath="/insights" />`. |
| `ui/src/routes/insights/+page.svelte` | **modify** (3 lines: 286, 326, 341) | Repoint the 3 `goto` calls to `/insights/...`. |

---

## Task 1: Pure `navlinks` helpers (TDD)

**Files:**
- Create: `ui/src/lib/navlinks.ts`
- Create: `ui/src/lib/navlinks.test.ts`

- [ ] **Step 1.1: Write the failing test**

Create `ui/src/lib/navlinks.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { sessionHref, projectHref, sessionBackHref } from './navlinks';

describe('sessionHref', () => {
  it('builds a Work-context session link when basePath is empty', () => {
    expect(sessionHref('', 'abc 123')).toBe('/sessions/abc%20123');
  });
  it('builds an Insights-context session link under /insights', () => {
    expect(sessionHref('/insights', 'abc123')).toBe('/insights/sessions/abc123');
  });
});

describe('projectHref', () => {
  it('builds a Work-context project link when basePath is empty', () => {
    expect(projectHref('', 'oms/svc')).toBe('/projects/oms%2Fsvc');
  });
  it('builds an Insights-context project link under /insights', () => {
    expect(projectHref('/insights', 'oms-service')).toBe('/insights/projects/oms-service');
  });
});

describe('sessionBackHref', () => {
  it('Work + known project → /projects/<name>', () => {
    expect(sessionBackHref('', 'oms-service')).toBe('/projects/oms-service');
  });
  it('Work + unknown project → /projects', () => {
    expect(sessionBackHref('', '')).toBe('/projects');
  });
  it('Insights + known project → /insights/projects/<name>', () => {
    expect(sessionBackHref('/insights', 'oms-service')).toBe('/insights/projects/oms-service');
  });
  it('Insights + unknown project → /insights', () => {
    expect(sessionBackHref('/insights', '')).toBe('/insights');
  });
});
```

- [ ] **Step 1.2: Run the test, verify it fails**

Run: `cd ui && npm run test -- navlinks`
Expected: FAIL — `Cannot find module './navlinks'`.

- [ ] **Step 1.3: Implement the helpers**

Create `ui/src/lib/navlinks.ts`:

```ts
/**
 * Context-aware internal link builders.
 *
 * `basePath` is '' in the Work-tab route tree and '/insights' under the
 * Insights tab. Threading it through every internal link is what keeps a
 * drill chain (project → session → back) inside whichever top-level tab
 * the user started from. See
 * docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md
 */

export function sessionHref(basePath: string, sessionId: string): string {
  return `${basePath}/sessions/${encodeURIComponent(sessionId)}`;
}

export function projectHref(basePath: string, projectName: string): string {
  return `${basePath}/projects/${encodeURIComponent(projectName)}`;
}

/**
 * Back target from a session view. With a known project, go to that
 * project's detail in the same tab; otherwise fall back to the tab's
 * project index (Work: '/projects'; Insights: '/insights', since the
 * ranked list lives on the Insights page itself).
 */
export function sessionBackHref(basePath: string, projectName: string): string {
  return projectName ? projectHref(basePath, projectName) : (basePath || '/projects');
}
```

- [ ] **Step 1.4: Run the test, verify it passes**

Run: `cd ui && npm run test -- navlinks`
Expected: PASS — 8 assertions green.

- [ ] **Step 1.5: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/lib/navlinks.ts ui/src/lib/navlinks.test.ts
git commit -m "ui: add pure navlinks helpers for context-aware drill-in

basePath-aware session/project link builders. Pure module, unit-tested,
matches the existing src/lib/*.test.ts pattern. Consumed by the
ProjectDetail/SessionDetail components in the next tasks.

Spec: docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md"
```

---

## Task 2: Extract `ProjectDetail.svelte`

**Files:**
- Create: `ui/src/lib/ui/ProjectDetail.svelte`
- Replace: `ui/src/routes/projects/[name]/+page.svelte` (currently 272 lines)

- [ ] **Step 2.1: Create the component from the existing page**

Copy the entire current contents of `ui/src/routes/projects/[name]/+page.svelte` into a new file `ui/src/lib/ui/ProjectDetail.svelte`, then apply exactly these four edits to the new component:

1. Add `import { sessionHref } from '$lib/navlinks.js';` to the import block (next to the other `$lib` imports).

2. **Replace** the param derivation line (currently line 14-15):

```svelte
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const projectName = $derived(decodeURIComponent(($page.params as any)['name'] ?? ''));
```

   **with** the props declaration (and drop the now-unused `import { page } from '$app/stores';` line):

```svelte
  let {
    projectName,
    basePath = '',
    backHref = '/projects',
  }: { projectName: string; basePath?: string; backHref?: string } = $props();
```

3. **Replace** the back button (currently line 129):

```svelte
  <button onclick={() => goto('/projects')} class="ad-btn ad-btn--ghost" style="margin-bottom: 12px; padding-left: 4px;">
```

   **with**:

```svelte
  <button onclick={() => goto(backHref)} class="ad-btn ad-btn--ghost" style="margin-bottom: 12px; padding-left: 4px;">
```

4. **Replace** the two session-row navigation handlers (currently lines 236-237):

```svelte
              onclick={() => goto(`/sessions/${encodeURIComponent(s.id)}`)}
              onkeydown={(e) => { if (e.key === 'Enter') goto(`/sessions/${encodeURIComponent(s.id)}`); }}
```

   **with**:

```svelte
              onclick={() => goto(sessionHref(basePath, s.id))}
              onkeydown={(e) => { if (e.key === 'Enter') goto(sessionHref(basePath, s.id)); }}
```

Leave everything else (fetch logic, CLI tabs, grouping, delete, `<svelte:head>`) byte-for-byte unchanged.

- [ ] **Step 2.2: Replace the route page with a thin wrapper**

Overwrite `ui/src/routes/projects/[name]/+page.svelte` with exactly:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import ProjectDetail from '$lib/ui/ProjectDetail.svelte';

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const projectName = $derived(decodeURIComponent(($page.params as any)['name'] ?? ''));
</script>

<ProjectDetail {projectName} basePath="" backHref="/projects" />
```

- [ ] **Step 2.3: Type/svelte check**

Run: `cd ui && npm run check`
Expected: PASS — no new svelte-check or contract errors. (If a pre-existing unrelated warning appears, confirm it also exists on `git stash` of these changes; do not fix unrelated warnings.)

- [ ] **Step 2.4: Build**

Run: `cd ui && npm run build`
Expected: PASS — SvelteKit builds; `/projects/[name]` route compiles.

- [ ] **Step 2.5: Run the test suite (regression)**

Run: `cd ui && npm run test`
Expected: PASS — all suites including `navlinks` stay green.

- [ ] **Step 2.6: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/lib/ui/ProjectDetail.svelte ui/src/routes/projects/\[name\]/+page.svelte
git commit -m "ui: extract ProjectDetail component; route becomes a wrapper

Project detail logic moves verbatim into lib/ui/ProjectDetail.svelte
with {projectName, basePath, backHref} props; session-row links and the
back button now honour basePath/backHref. Work-tab /projects/[name]
behaviour unchanged (basePath='', backHref='/projects').

Spec: docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md"
```

---

## Task 3: Extract `SessionDetail.svelte`

**Files:**
- Create: `ui/src/lib/ui/SessionDetail.svelte`
- Replace: `ui/src/routes/sessions/[id]/+page.svelte` (currently 379 lines)

- [ ] **Step 3.1: Create the component from the existing page**

Copy the entire current contents of `ui/src/routes/sessions/[id]/+page.svelte` into a new file `ui/src/lib/ui/SessionDetail.svelte`, then apply exactly these three edits:

1. Add `import { sessionBackHref } from '$lib/navlinks.js';` to the import block.

2. **Replace** the param derivation (currently line 16):

```svelte
  const sessionId = $derived($page.params.id ?? '');
```

   **with** props (and remove the now-unused `import { page } from '$app/stores';` line):

```svelte
  let {
    sessionId,
    basePath = '',
  }: { sessionId: string; basePath?: string } = $props();
```

3. There are exactly two `/projects/...` link sites that must become context-aware. The `projectName` value is the existing `$derived` at line 66 — leave that derivation as-is.

   **Replace** the after-delete destination (currently line 36):

```svelte
      const dest = projectName ? `/projects/${encodeURIComponent(projectName)}` : '/projects';
```

   **with**:

```svelte
      const dest = sessionBackHref(basePath, projectName);
```

   **Replace** the back button handler (currently line 175):

```svelte
    onclick={() => projectName ? goto(`/projects/${encodeURIComponent(projectName)}`) : goto('/projects')}
```

   **with**:

```svelte
    onclick={() => goto(sessionBackHref(basePath, projectName))}
```

   Leave the visible back-label at line 179 (`‹ {projectName || 'projects'}`) and everything else (fetch, SSE subscribe, restore modal, `<svelte:head>`) byte-for-byte unchanged.

- [ ] **Step 3.2: Replace the route page with a thin wrapper**

Overwrite `ui/src/routes/sessions/[id]/+page.svelte` with exactly:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import SessionDetail from '$lib/ui/SessionDetail.svelte';

  const sessionId = $derived($page.params.id ?? '');
</script>

<SessionDetail {sessionId} basePath="" />
```

- [ ] **Step 3.3: Type/svelte check**

Run: `cd ui && npm run check`
Expected: PASS — no new errors.

- [ ] **Step 3.4: Build**

Run: `cd ui && npm run build`
Expected: PASS — `/sessions/[id]` route compiles.

- [ ] **Step 3.5: Run the test suite (regression)**

Run: `cd ui && npm run test`
Expected: PASS — all suites green.

- [ ] **Step 3.6: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/lib/ui/SessionDetail.svelte ui/src/routes/sessions/\[id\]/+page.svelte
git commit -m "ui: extract SessionDetail component; route becomes a wrapper

Session detail logic moves verbatim into lib/ui/SessionDetail.svelte
with {sessionId, basePath} props; back + after-delete targets now use
sessionBackHref(basePath, projectName). Work-tab /sessions/[id]
behaviour unchanged (basePath='').

Spec: docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md"
```

---

## Task 4: Insights sub-routes + repoint Insights links

**Files:**
- Create: `ui/src/routes/insights/projects/[name]/+page.svelte`
- Create: `ui/src/routes/insights/sessions/[id]/+page.svelte`
- Modify: `ui/src/routes/insights/+page.svelte` (lines 286, 326, 341)

- [ ] **Step 4.1: Create the Insights project sub-route wrapper**

Create `ui/src/routes/insights/projects/[name]/+page.svelte` with exactly:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import ProjectDetail from '$lib/ui/ProjectDetail.svelte';

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const projectName = $derived(decodeURIComponent(($page.params as any)['name'] ?? ''));
</script>

<ProjectDetail {projectName} basePath="/insights" backHref="/insights" />
```

- [ ] **Step 4.2: Create the Insights session sub-route wrapper**

Create `ui/src/routes/insights/sessions/[id]/+page.svelte` with exactly:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import SessionDetail from '$lib/ui/SessionDetail.svelte';

  const sessionId = $derived($page.params.id ?? '');
</script>

<SessionDetail {sessionId} basePath="/insights" />
```

- [ ] **Step 4.3: Repoint the three Insights links**

In `ui/src/routes/insights/+page.svelte`:

**Line 286** — replace:

```svelte
                onclick={(e) => { e.stopPropagation(); goto(`/projects/${encodeURIComponent(p.name)}`); }}
```

with:

```svelte
                onclick={(e) => { e.stopPropagation(); goto(`/insights/projects/${encodeURIComponent(p.name)}`); }}
```

**Line 326** — replace:

```svelte
                            <button class="btn btn--ghost btn--sm" onclick={(e) => { e.stopPropagation(); goto(`/sessions/${encodeURIComponent(s.session_id)}`); }}>open →</button>
```

with:

```svelte
                            <button class="btn btn--ghost btn--sm" onclick={(e) => { e.stopPropagation(); goto(`/insights/sessions/${encodeURIComponent(s.session_id)}`); }}>open →</button>
```

**Line 341** — replace:

```svelte
                        onclick={() => goto(`/projects/${encodeURIComponent(p.name)}`)}
```

with:

```svelte
                        onclick={() => goto(`/insights/projects/${encodeURIComponent(p.name)}`)}
```

(These are the only three `goto` calls in `insights/+page.svelte`; verify with `grep -n "goto(" ui/src/routes/insights/+page.svelte` → exactly 3 matches, all now `/insights/...`.)

- [ ] **Step 4.4: Type/svelte check**

Run: `cd ui && npm run check`
Expected: PASS — no new errors; the two new route files type-check.

- [ ] **Step 4.5: Build**

Run: `cd ui && npm run build`
Expected: PASS — SvelteKit registers `/insights/projects/[name]` and `/insights/sessions/[id]`.

- [ ] **Step 4.6: Run the full test suite**

Run: `cd ui && npm run test`
Expected: PASS — all suites green.

- [ ] **Step 4.7: Manual browser verification (required)**

Build+install the app and exercise the flow:

```bash
cd ~/Desktop/Project/klyne && make install
```

Then in the running klyne web UI:
1. Open the **Insights** tab.
2. Expand a project, click the per-row "open →" → URL is `/insights/projects/<name>`, **Insights tab stays highlighted**.
3. Click a session row → URL is `/insights/sessions/<id>`, **Insights tab stays highlighted**.
4. Click the session "‹ back" → returns to `/insights/projects/<name>`, Insights still active.
5. Click the project "‹" back → goes to `/insights` (ranked list), Insights still active.
6. From the expanded row, click "Open full project →" and a top-session "open →" → both land under `/insights/...`, Insights active.
7. Browser back button retraces the chain. Hard-refresh on `/insights/projects/<name>` and `/insights/sessions/<id>` → both render correctly.
8. **Regression:** open the **Work** tab → a project → a session. URLs are `/projects/<name>` and `/sessions/<id>`, Work tab stays active, behaviour identical to before. The projects-list page row click still works.

Report any deviation; do not mark complete until all 8 pass.

- [ ] **Step 4.8: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/routes/insights/projects/\[name\]/+page.svelte ui/src/routes/insights/sessions/\[id\]/+page.svelte ui/src/routes/insights/+page.svelte
git commit -m "ui: keep Insights drill-in inside the Insights tab

Adds /insights/projects/[name] + /insights/sessions/[id] wrappers
(reusing ProjectDetail/SessionDetail) and repoints the 3 Insights
goto() calls at them. Insights → project → session → back now stays
on the Insights tab; Work-tab routes untouched; TopNav unchanged.

Closes the spec: docs/superpowers/specs/2026-05-19-insights-self-contained-drill-in-design.md"
```

---

## Self-review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| Goal 1 — clicking into project/session keeps Insights tab active | Task 4 (sub-routes + repointed links); verified Step 4.7 |
| Goal 2 — full project list + session detail reachable from Insights | Task 4 wrappers reuse the extracted components (Tasks 2–3) |
| Goal 3 — full chain stays in Insights, deep-linkable, back works | `basePath`/`backHref` (Task 1 helpers + Tasks 2–3 wiring); Step 4.7 #4–7 |
| Goal 4 — old `/projects` & `/sessions` unchanged | Tasks 2–3 wrappers pass `basePath=""`; Step 4.7 #8 regression |
| Component extraction (no duplication) | Tasks 2 & 3 |
| `basePath` + `backHref` props | Task 1 (helpers), Tasks 2–3 (props), Task 4 (values) |
| No TopNav change | Stated in Background; no task touches TopNav |
| Testing: assert link construction honours basePath/backHref | Task 1 `navlinks.test.ts` (8 assertions) + manual Step 4.7 |
| `sessionBackHref` unknown-project fallback (Work `/projects`, Insights `/insights`) | Task 1 tests cover all 4 cases |

No gaps.

**Placeholder scan:** No TBD/TODO/"similar to"/"handle edge cases". Every code change shown in full or as exact before/after.

**Type consistency:** `navlinks` exports `sessionHref(basePath, sessionId)`, `projectHref(basePath, projectName)`, `sessionBackHref(basePath, projectName)` — identical signatures in test (Task 1), `ProjectDetail` (Task 2 uses `sessionHref`), `SessionDetail` (Task 3 uses `sessionBackHref`). Props: `ProjectDetail {projectName, basePath, backHref}` and `SessionDetail {sessionId, basePath}` are consistent across component definition (Tasks 2–3) and all four route wrappers (Tasks 2–4).

**Scope:** One focused UI refactor, 4 sequential tasks (Task 2 & 3 independent of each other but both depend on Task 1; Task 4 depends on 2 & 3). Fits one plan.

---

## Execution

**Plan complete and saved to `docs/superpowers/plans/2026-05-19-insights-self-contained-drill-in.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task, two-stage review between tasks.

**2. Inline Execution** — execute tasks here with checkpoints.

**Which approach?**
