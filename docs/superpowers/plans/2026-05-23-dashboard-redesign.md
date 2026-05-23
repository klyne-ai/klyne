# Dashboard Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace today's 15 SvelteKit routes with a unified 7-surface dashboard shell that matches the approved design bundle, without removing any existing feature.

**Architecture:** Single `Shell` layout (sidebar + topbar + ⌘K palette + slide-over SessionDrawer) renders all surfaces. Pages are SvelteKit routes; the drawer and palette are overlays mounted in `+layout.svelte` and tracked via URL search params (`?session=`, `?palette=1`, `?tab=`). Existing API endpoints, SSE, and Productivity components are reused unchanged. A short-lived `PUBLIC_DASHBOARD_V2` env toggle lets the new shell land alongside the old one until cutover (Task 10).

**Tech Stack:** SvelteKit 2 / Svelte 5 (runes), Vite, TypeScript, Vitest. Visual tokens via `src/lib/styles/tokens.css`. No new runtime deps.

**Spec:** `docs/superpowers/specs/2026-05-23-dashboard-redesign-design.md`

**Design source-of-truth (read before each page task):** `docs/design/2026-05-23-dashboard/dashboard/*.jsx` + `shell.css` + `tokens.css`. The prototype is React; port behaviour, not structure.

---

## Pre-flight

- [ ] **Step P1: Confirm worktree + clean tree**

```bash
git rev-parse --git-common-dir   # expect ../../.git (linked worktree)
git branch --show-current        # expect feat/dashboard-redesign
git status                       # expect clean
```

- [ ] **Step P2: Install deps + baseline test run**

```bash
cd ui
npm install
npm run check    # svelte-check
npm test         # vitest run
```

Expected: deps install, all current tests pass. If any test fails on `init`, stop and report — the redesign cannot proceed without a green baseline.

- [ ] **Step P3: Verify design bundle is present**

```bash
ls docs/design/2026-05-23-dashboard/dashboard/ | wc -l   # expect 12 (jsx + css)
cat docs/design/2026-05-23-dashboard/README.md            # confirms intent
```

---

## Task 1: Foundation — shell + tokens + layout cutover env

**Goal:** Stand up the new chrome (Sidebar, Topbar, SearchPalette, SessionDrawer) as empty scaffolds; flip via `PUBLIC_DASHBOARD_V2`. No existing route touched yet.

**Files:**
- Create: `ui/src/lib/styles/tokens.css`
- Create: `ui/src/lib/dashboard/Shell.svelte`
- Create: `ui/src/lib/dashboard/Sidebar.svelte`
- Create: `ui/src/lib/dashboard/Topbar.svelte`
- Create: `ui/src/lib/dashboard/SearchPalette.svelte`
- Create: `ui/src/lib/dashboard/SessionDrawer.svelte`
- Create: `ui/src/lib/dashboard/icons.ts`
- Create: `ui/src/lib/dashboard/nav.ts`
- Create: `ui/src/lib/dashboard/url-state.ts`
- Create: `ui/src/lib/dashboard/url-state.test.ts`
- Modify: `ui/src/routes/+layout.svelte`
- Modify: `ui/src/app.css` (import tokens.css)

- [ ] **Step 1.1: Move tokens.css into `lib/styles/`**

```bash
git mv ui/src/routes/productivity/tokens.css ui/src/lib/styles/tokens.css 2>/dev/null || cp docs/design/2026-05-23-dashboard/tokens.css ui/src/lib/styles/tokens.css
```

If the source file lives elsewhere today, use `grep -rln 'tokens.css' ui/src` to locate and `git mv` it. Then update every import to `$lib/styles/tokens.css`.

- [ ] **Step 1.2: Wire tokens into `app.css`**

In `ui/src/app.css`, add at the very top:

```css
@import '$lib/styles/tokens.css';
```

Remove any tokens-related @import elsewhere.

- [ ] **Step 1.3: Write failing test for url-state helpers**

Create `ui/src/lib/dashboard/url-state.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { sessionUrl, paletteUrl, tabUrl, parseDashboardSearch } from './url-state';

describe('dashboard url-state', () => {
  it('sessionUrl appends ?session=<id> preserving existing params', () => {
    expect(sessionUrl('/projects?tab=worklog', 'abc123')).toBe('/projects?tab=worklog&session=abc123');
  });

  it('sessionUrl replaces an existing session param', () => {
    expect(sessionUrl('/insights?session=old', 'new1')).toBe('/insights?session=new1');
  });

  it('paletteUrl toggles ?palette=1', () => {
    expect(paletteUrl('/projects?tab=overview', true)).toBe('/projects?tab=overview&palette=1');
    expect(paletteUrl('/projects?palette=1&tab=overview', false)).toBe('/projects?tab=overview');
  });

  it('tabUrl swaps the tab param', () => {
    expect(tabUrl('/insights', 'daily')).toBe('/insights?tab=daily');
    expect(tabUrl('/insights?tab=overview', 'models')).toBe('/insights?tab=models');
  });

  it('parseDashboardSearch returns drawer + palette state', () => {
    const u = new URL('http://x/insights?session=abc&palette=1&tab=daily');
    expect(parseDashboardSearch(u)).toEqual({ session: 'abc', palette: true, tab: 'daily' });
  });
});
```

- [ ] **Step 1.4: Run test — expect FAIL (module missing)**

```bash
cd ui && npx vitest run src/lib/dashboard/url-state.test.ts
```

Expected: 5 failed, "Cannot find module './url-state'".

- [ ] **Step 1.5: Implement url-state helpers**

Create `ui/src/lib/dashboard/url-state.ts`:

```ts
export interface DashboardSearch {
  session: string | null;
  palette: boolean;
  tab: string | null;
}

export function parseDashboardSearch(u: URL): DashboardSearch {
  return {
    session: u.searchParams.get('session'),
    palette: u.searchParams.get('palette') === '1',
    tab: u.searchParams.get('tab'),
  };
}

function rebuild(pathAndSearch: string, mutate: (sp: URLSearchParams) => void): string {
  const [path, search = ''] = pathAndSearch.split('?');
  const sp = new URLSearchParams(search);
  mutate(sp);
  const out = sp.toString();
  return out ? `${path}?${out}` : path;
}

export function sessionUrl(current: string, id: string | null): string {
  return rebuild(current, (sp) => {
    if (id) sp.set('session', id);
    else sp.delete('session');
  });
}

export function paletteUrl(current: string, open: boolean): string {
  return rebuild(current, (sp) => {
    if (open) sp.set('palette', '1');
    else sp.delete('palette');
  });
}

export function tabUrl(current: string, tab: string | null): string {
  return rebuild(current, (sp) => {
    if (tab) sp.set('tab', tab);
    else sp.delete('tab');
  });
}
```

- [ ] **Step 1.6: Run test — expect PASS**

```bash
cd ui && npx vitest run src/lib/dashboard/url-state.test.ts
```

Expected: 5 passed.

- [ ] **Step 1.7: Define nav model**

Create `ui/src/lib/dashboard/nav.ts`:

```ts
export type NavId = 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'advisors' | 'worklog';

export interface NavItem {
  id: NavId;
  label: string;
  href: string;
  icon: 'live' | 'productivity' | 'projects' | 'insights' | 'runbooks' | 'flame' | 'branch';
  section: 'workspace' | 'capture';
}

export const NAV: readonly NavItem[] = [
  { id: 'live',         label: 'Live',         href: '/',             icon: 'live',         section: 'workspace' },
  { id: 'productivity', label: 'Productivity', href: '/productivity', icon: 'productivity', section: 'workspace' },
  { id: 'projects',     label: 'Projects',     href: '/projects',     icon: 'projects',     section: 'workspace' },
  { id: 'insights',     label: 'Insights',     href: '/insights',     icon: 'insights',     section: 'workspace' },
  { id: 'runbooks',     label: 'Runbooks',     href: '/runbooks',     icon: 'runbooks',     section: 'workspace' },
  { id: 'advisors',     label: 'Advisors',     href: '/advisors',     icon: 'flame',        section: 'capture'   },
  { id: 'worklog',      label: 'Worklog',      href: '/worklog',      icon: 'branch',       section: 'capture'   },
] as const;

export function navIdForPath(pathname: string): NavId {
  if (pathname.startsWith('/productivity')) return 'productivity';
  if (pathname.startsWith('/projects'))     return 'projects';
  if (pathname.startsWith('/insights'))     return 'insights';
  if (pathname.startsWith('/runbooks'))     return 'runbooks';
  if (pathname.startsWith('/advisors'))     return 'advisors';
  if (pathname.startsWith('/worklog'))      return 'worklog';
  return 'live';
}

export function crumbsForNavId(id: NavId): readonly string[] {
  const section = NAV.find((n) => n.id === id)?.section ?? 'workspace';
  const label = NAV.find((n) => n.id === id)?.label ?? 'Live';
  return [section === 'workspace' ? 'Workspace' : 'Capture', label];
}
```

- [ ] **Step 1.8: Port icons.jsx to icons.ts**

Create `ui/src/lib/dashboard/icons.ts`:

```ts
// SVG path data for the 12-icon set. Each value is the raw inner SVG
// markup of a 16x16 viewBox, stroked with currentColor at width 1.4.
export const ICON_PATHS = {
  live:         '<circle cx="8" cy="8" r="2.2"/><path d="M4 4.5a5 5 0 0 0 0 7M12 4.5a5 5 0 0 1 0 7"/>',
  productivity: '<path d="M2.5 12.5V9M6 12.5V5M9.5 12.5V7.5M13 12.5V3.5"/>',
  projects:     '<path d="M2.5 4.5a1 1 0 0 1 1-1h3l1.2 1.4h4.8a1 1 0 0 1 1 1V12a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1Z"/>',
  insights:     '<path d="M2.5 13h11"/><path d="M3.5 10.5l3-3 2.5 2 4-4.5"/><circle cx="13" cy="5" r="0.8"/>',
  runbooks:     '<path d="M3.5 2.8h7a1 1 0 0 1 1 1v9.4a1 1 0 0 1-1 1h-7Z"/><path d="M3.5 2.8v11.4"/><path d="M5.5 5.5h4M5.5 7.5h4M5.5 9.5h3"/>',
  search:       '<circle cx="7" cy="7" r="3.6"/><path d="M10 10l3 3"/>',
  refresh:      '<path d="M3 8a5 5 0 0 1 8.5-3.5L13 6M13 3v3h-3"/><path d="M13 8a5 5 0 0 1-8.5 3.5L3 10M3 13v-3h3"/>',
  x:            '<path d="M4 4l8 8M12 4l-8 8"/>',
  chev:         '<path d="M6 4l4 4-4 4"/>',
  open:         '<path d="M6 3h7v7"/><path d="M13 3l-7 7"/><path d="M11 9v4H3V5h4"/>',
  filter:       '<path d="M2.5 4h11l-4 5v4l-3-1V9Z"/>',
  copy:         '<rect x="5" y="5" width="8" height="8" rx="1.2"/><path d="M5 9V3h6"/>',
  sparkles:     '<path d="M8 2.5v11M2.5 8h11M5 5l6 6M11 5l-6 6"/>',
  inbox:        '<path d="M2.5 9.5l1.5-6h8l1.5 6v3a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1Z"/><path d="M2.5 9.5h3l1 2h3l1-2h3"/>',
  flame:        '<path d="M8 13.5c2 0 3.5-1.5 3.5-3.5 0-2-2-2.5-2-5.5 0 1.5-2 2-2 4 0-1.5-1.5-2-1.5-1 0 1-1.5 1.5-1.5 3 0 2 1.5 3 3.5 3Z"/>',
  branch:       '<circle cx="4" cy="4" r="1.4"/><circle cx="4" cy="12" r="1.4"/><circle cx="12" cy="6" r="1.4"/><path d="M4 5.5v5M4.7 11.6c4-1 7.3-2.4 7.3-5"/>',
} as const;

export type IconName = keyof typeof ICON_PATHS;
```

Create a tiny renderer component `ui/src/lib/dashboard/Icon.svelte`:

```svelte
<script lang="ts">
  import { ICON_PATHS, type IconName } from './icons';
  interface Props { name: IconName; size?: number; }
  const { name, size = 16 }: Props = $props();
</script>

<svg
  width={size} height={size} viewBox="0 0 16 16"
  fill="none" stroke="currentColor" stroke-width="1.4"
  stroke-linecap="round" stroke-linejoin="round"
>
  {@html ICON_PATHS[name]}
</svg>
```

(`@html` here is safe — values come from a static constant the user cannot reach.)

- [ ] **Step 1.9: Sidebar component**

Port from `docs/design/2026-05-23-dashboard/dashboard/shell.jsx` (the `Sidebar` React function) and `shell.css` (lines 26–160).

Create `ui/src/lib/dashboard/Sidebar.svelte`:

```svelte
<script lang="ts">
  import Icon from './Icon.svelte';
  import { NAV, type NavId } from './nav';
  import { goto } from '$app/navigation';

  interface Props {
    current: NavId;
    collapsed: boolean;
    liveCount: number;
    advisorCount: number;
    projectCount: number;
    onToggleCollapse: () => void;
    onOpenSearch: () => void;
  }
  const { current, collapsed, liveCount, advisorCount, projectCount, onToggleCollapse, onOpenSearch }: Props = $props();

  function meta(id: NavId): { text: string; kind: '' | 'live' | 'alert' } {
    switch (id) {
      case 'live': return liveCount > 0 ? { text: `${liveCount} live`, kind: 'live' } : { text: '', kind: '' };
      case 'productivity': return { text: 'today', kind: '' };
      case 'projects': return { text: String(projectCount), kind: '' };
      case 'insights': return { text: '1d', kind: '' };
      case 'runbooks': return { text: '', kind: '' };
      case 'advisors': return { text: String(advisorCount), kind: advisorCount > 0 ? 'alert' : '' };
      case 'worklog': return { text: '', kind: '' };
    }
  }
</script>

<aside class="sidebar">
  <div class="sidebar-brand">
    <div class="mark">&gt;K</div>
    {#if !collapsed}<div class="name">klyne<em>·local</em></div>{/if}
    <button class="sidebar-toggle" onclick={onToggleCollapse} title={collapsed ? 'Expand' : 'Collapse'}>
      {collapsed ? '›' : '‹'}
    </button>
  </div>

  <button class="sidebar-search" onclick={onOpenSearch} title="Search messages…">
    <Icon name="search" />
    {#if !collapsed}
      <span>Search messages…</span>
      <span class="key">⌘K</span>
    {/if}
  </button>

  {#each ['workspace', 'capture'] as section}
    {#if !collapsed}
      <div class="sidebar-section">{section === 'workspace' ? 'Workspace' : 'Capture'}</div>
    {/if}
    <nav class="sidebar-nav">
      {#each NAV.filter((n) => n.section === section) as item}
        {@const m = meta(item.id)}
        <a
          href={item.href}
          class="sidebar-nav-item"
          class:active={current === item.id}
          title={collapsed ? item.label : undefined}
          onclick={(e) => { e.preventDefault(); void goto(item.href); }}
        >
          <span class="icon"><Icon name={item.icon} /></span>
          {#if !collapsed}<span class="label">{item.label}</span>{/if}
          {#if m.text}<span class="meta" class:live={m.kind === 'live'} class:alert={m.kind === 'alert'}>{m.text}</span>{/if}
        </a>
      {/each}
    </nav>
  {/each}

  <div class="sidebar-foot">
    <div class="avatar">M</div>
    {#if !collapsed}
      <div class="who">
        <div class="name">Local user</div>
        <div class="sub">127.0.0.1:7878</div>
      </div>
      <div class="status" title="daemon running"></div>
    {/if}
  </div>
</aside>

<style>
  /* Copy verbatim from docs/design/2026-05-23-dashboard/dashboard/shell.css
     lines 26–160 (`.sidebar*` rules). Convert `:hover` and `.active`
     correctly. Component-scoped via Svelte's <style>. */
</style>
```

The executor must paste the matching CSS rules from `shell.css` into the `<style>` block.

- [ ] **Step 1.10: Topbar component**

Create `ui/src/lib/dashboard/Topbar.svelte`:

```svelte
<script lang="ts">
  interface Props {
    crumbs: readonly string[];
    daemonStatus: string;
    rightSlot?: import('svelte').Snippet;
  }
  const { crumbs, daemonStatus, rightSlot }: Props = $props();
</script>

<div class="topbar">
  <div class="crumbs">
    {#each crumbs as c, i}
      <span class:here={i === crumbs.length - 1}>{c}</span>
      {#if i < crumbs.length - 1}<span class="sep">/</span>{/if}
    {/each}
  </div>
  <div class="grow"></div>
  {#if rightSlot}{@render rightSlot()}{/if}
  <div class="meta">
    <span class="status-pill"><span class="dot"></span>daemon · {daemonStatus}</span>
  </div>
</div>

<style>
  /* Copy `.topbar*` rules from shell.css lines 162–200. */
</style>
```

- [ ] **Step 1.11: SearchPalette skeleton (full implementation in Task 9)**

Create `ui/src/lib/dashboard/SearchPalette.svelte`:

```svelte
<script lang="ts">
  interface Props { onClose: () => void; }
  const { onClose }: Props = $props();
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onClose()} />

<div class="palette-scrim" onclick={onClose} role="presentation">
  <div class="palette" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <input autofocus placeholder="Search messages, sessions, projects…" />
    <div class="group">
      <div class="group-label">Type to search</div>
    </div>
  </div>
</div>

<style>
  /* Copy `.palette*` rules from shell.css lines 432–478. */
</style>
```

- [ ] **Step 1.12: SessionDrawer skeleton (full implementation in Task 8)**

Create `ui/src/lib/dashboard/SessionDrawer.svelte`:

```svelte
<script lang="ts">
  import Icon from './Icon.svelte';
  interface Props { sessionId: string; onClose: () => void; }
  const { sessionId, onClose }: Props = $props();
</script>

<div class="drawer-scrim" onclick={onClose} role="presentation"></div>
<aside class="drawer" role="dialog" aria-modal="true" aria-label="Session detail">
  <header class="drawer-head">
    <span class="kicker">Session</span>
    <span class="mono">{sessionId}</span>
    <button class="x" onclick={onClose} aria-label="Close"><Icon name="x" /></button>
  </header>
  <div class="drawer-body">
    <p>Session detail wiring lands in Task 8.</p>
  </div>
</aside>

<style>
  /* Copy `.drawer*` rules from shell.css lines 298–333. */
</style>
```

- [ ] **Step 1.13: Shell component (composes Sidebar + Topbar + overlays)**

Create `ui/src/lib/dashboard/Shell.svelte`:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Sidebar from './Sidebar.svelte';
  import Topbar from './Topbar.svelte';
  import SearchPalette from './SearchPalette.svelte';
  import SessionDrawer from './SessionDrawer.svelte';
  import { navIdForPath, crumbsForNavId } from './nav';
  import { parseDashboardSearch, paletteUrl, sessionUrl } from './url-state';
  import { cockpitStore } from '$lib/cockpit.svelte.js';
  import { projectsStore } from '$lib/projects.svelte.js';
  import type { Snippet } from 'svelte';

  interface Props { children: Snippet; rightSlot?: Snippet; }
  const { children, rightSlot }: Props = $props();

  const navId = $derived(navIdForPath($page.url.pathname));
  const crumbs = $derived(crumbsForNavId(navId));
  const search = $derived(parseDashboardSearch($page.url));

  // Auto-collapse on Live unless the user has manually toggled this session.
  let userToggled = $state(false);
  let collapsed = $state(navId === 'live');
  $effect(() => { if (!userToggled) collapsed = navId === 'live'; });

  function navigate(url: string): void { void goto(url); }
  function toggleCollapse(): void { userToggled = true; collapsed = !collapsed; }
  function openPalette(): void { navigate(paletteUrl($page.url.pathname + $page.url.search, true)); }
  function closePalette(): void { navigate(paletteUrl($page.url.pathname + $page.url.search, false)); }
  function closeDrawer(): void { navigate(sessionUrl($page.url.pathname + $page.url.search, null)); }

  function onKey(e: KeyboardEvent): void {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); openPalette(); }
    else if (e.key === '/') { e.preventDefault(); openPalette(); }
  }

  const liveCount = $derived(cockpitStore.liveCount);
  const projectCount = $derived(projectsStore.projects?.length ?? 0);
  const advisorCount = $derived(0); // wired in Task 7 once advisor store lands
</script>

<svelte:window onkeydown={onKey} />

<div class="app" class:collapsed>
  <Sidebar
    current={navId}
    {collapsed}
    {liveCount}
    {advisorCount}
    {projectCount}
    onToggleCollapse={toggleCollapse}
    onOpenSearch={openPalette}
  />
  <div class="main">
    <Topbar {crumbs} daemonStatus={cockpitStore.daemonStatus ?? 'running · 127.0.0.1:7878'} {rightSlot} />
    <div class="page">{@render children()}</div>
  </div>
</div>

{#if search.session}<SessionDrawer sessionId={search.session} onClose={closeDrawer} />{/if}
{#if search.palette}<SearchPalette onClose={closePalette} />{/if}

<style>
  /* Copy `.app`, `.app.collapsed`, `.main`, `.page` rules from
     shell.css lines 15–24 and 162–208. */
</style>
```

If `cockpitStore.liveCount` / `daemonStatus` / `projectsStore.projects` don't exist with those names, add the derived getters in `cockpit.svelte.ts` / `projects.svelte.ts` and keep the API stable for downstream tasks.

- [ ] **Step 1.14: Swap layout based on env**

Modify `ui/src/routes/+layout.svelte`. Add at the top of the script:

```ts
import { env } from '$env/dynamic/public';
import Shell from '$lib/dashboard/Shell.svelte';
const useNewShell = env.PUBLIC_DASHBOARD_V2 === '1';
```

Wrap the existing layout body in an `{#if useNewShell}` / `{:else}` pair:

```svelte
{#if useNewShell}
  <Shell>{@render children()}</Shell>
{:else}
  <!-- existing TopNav + children — UNCHANGED -->
  <div style="height: 100vh; display: flex; flex-direction: column;">
    <TopNav onsearch={() => (searchOpen = true)} />
    <div style="flex: 1; min-height: 0; overflow: auto;">{@render children()}</div>
  </div>
  <SearchOverlay open={searchOpen} onClose={() => (searchOpen = false)} />
{/if}
```

- [ ] **Step 1.15: Verify both shells render**

Start the daemon + dev server in two terminals:

```bash
# Terminal A: daemon (handles API + SSE)
make daemon

# Terminal B: UI with new shell ON
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
```

Visit `http://127.0.0.1:5173/` — the new sidebar/topbar must render around an unchanged page body. Stop the dev server and re-run without the env var; the old shell must still render. Run:

```bash
cd ui && npm run check && npm test
```

Expected: 0 type errors, all tests pass.

- [ ] **Step 1.16: Commit**

```bash
git add ui/src/lib/dashboard ui/src/lib/styles ui/src/app.css ui/src/routes/+layout.svelte
git commit -m "$(cat <<'EOF'
chore(ui): introduce dashboard shell scaffold

Adds Shell + Sidebar + Topbar + SearchPalette + SessionDrawer skeletons
under $lib/dashboard, the icon set and url-state helpers, and a
PUBLIC_DASHBOARD_V2 env toggle so the new chrome can land alongside the
old layout. No routes touched.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Live page (replaces `/` + `/cockpit`)

**Goal:** Render today's `cockpitStore.threads` as the 3-col tile grid from `docs/design/2026-05-23-dashboard/dashboard/page-live.jsx`. Adopt tail-of-3 content + fullscreen focus modal + hidden-session toggle.

**Files:**
- Create: `ui/src/lib/dashboard/pages/Live.svelte`
- Create: `ui/src/lib/dashboard/pages/LiveTile.svelte`
- Modify: `ui/src/routes/+page.svelte` (becomes thin router shell that mounts `Live`)
- Reuse: `Terminal.svelte` (focus modal), `hidden-sessions.svelte.ts` store, `cockpit.svelte.ts`, SSE plumbing

- [ ] **Step 2.1: Read the design source**

```bash
cat docs/design/2026-05-23-dashboard/dashboard/page-live.jsx
```

Note: `SessionTile` renders header (dot · project · short id · CLI pill · last-ago), branch + tokens + ctx% row, body of 3 tail messages with role kicker and line-clamp 3, foot with state + open chevron.

- [ ] **Step 2.2: Port `SessionTile` to `LiveTile.svelte`**

Create `ui/src/lib/dashboard/pages/LiveTile.svelte`:

```svelte
<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl } from '$lib/dashboard/url-state';
  import { relAgo, kfmt } from '$lib/format';
  import { isConversationalMessage } from '$lib/messageFilters';
  import { hiddenSessions } from '$lib/hidden-sessions.svelte';
  import type { CockpitThread, Message } from '$lib/types';

  interface Props { thread: CockpitThread; recent: Message[]; tickMs: number; }
  const { thread, recent, tickMs }: Props = $props();

  const tail = $derived(recent.filter(isConversationalMessage).slice(-3));
  const lastAgo = $derived(relAgo(tickMs, thread.last_msg_at));
  const isLive = $derived(tickMs - thread.last_msg_at < 30 * 60 * 1000);
  const ctxColor = $derived(
    thread.ctx_pct > 70 ? 'var(--alert)' : thread.ctx_pct > 50 ? 'var(--warn)' : 'var(--ok)'
  );

  function openDrawer(): void {
    void goto(sessionUrl($page.url.pathname + $page.url.search, thread.session_id));
  }
</script>

<div class="card" class:live={isLive}>
  <header>
    <span class="dot" class:ok={isLive}></span>
    <span class="project">{thread.project_name}</span>
    <span class="id mono">{thread.session_id.slice(0, 8)}</span>
    <span class="cli pill" class:claude={thread.cli === 'claude'} class:codex={thread.cli === 'codex'}>{thread.cli}</span>
    <span class="ago mono">{lastAgo}</span>
    <button class="eye" onclick={() => hiddenSessions.toggle(thread.session_id)} title="Hide this session">
      {hiddenSessions.has(thread.session_id) ? '👁︎' : '👁'}
    </button>
  </header>
  <div class="meta mono">
    <span>{thread.git_branch ?? 'main'}</span>
    <span>{thread.msg_count} · ↓ {kfmt(thread.tokens_out)}</span>
    <span style="color: {ctxColor}">ctx {thread.ctx_pct}%</span>
  </div>
  <div class="tail">
    {#each tail as m (m.id)}
      <div class="msg">
        <div class="role mono">{m.role}</div>
        <p>{m.content_excerpt}</p>
      </div>
    {/each}
    {#if tail.length === 0}<div class="empty mono">no recent turns</div>{/if}
  </div>
  <footer>
    <span class="state mono">{isLive ? 'streaming' : 'idle'} · last msg {lastAgo}</span>
    <button class="open" onclick={openDrawer}>open ›</button>
  </footer>
</div>

<style>
  /* Match page-live.jsx SessionTile inline styles + visual rules.
     Border + box-shadow brighten when .live. Tail messages use a 2px
     left accent rail. Open button is bg-card-2 with hover bg-card. */
</style>
```

If `CockpitThread` is missing fields (`ctx_pct`, `project_name`, `tokens_out`, etc.), add them as optional in `ui/src/lib/types.ts` with safe defaults derived from existing fields.

- [ ] **Step 2.3: Port `PageLive` to `Live.svelte`**

Create `ui/src/lib/dashboard/pages/Live.svelte`:

```svelte
<script lang="ts">
  import LiveTile from './LiveTile.svelte';
  import Icon from '$lib/dashboard/Icon.svelte';
  import { cockpitStore } from '$lib/cockpit.svelte';
  import { hiddenSessions } from '$lib/hidden-sessions.svelte';
  import { fetchMessages } from '$lib/api';
  import Terminal from '$lib/ui/Terminal.svelte';
  import type { Message } from '$lib/types';

  let filter = $state('');
  let cli = $state<'all' | 'claude' | 'codex'>('all');
  let showIdle = $state(true);
  let focusSessionId = $state<string | null>(null);

  // Lazy load tail messages per visible thread; cached by session_id.
  let recents = $state<Record<string, Message[]>>({});
  $effect(() => {
    for (const t of cockpitStore.threads) {
      if (recents[t.session_id]) continue;
      void fetchMessages({ sessionId: t.session_id, limit: 10 }).then((r) => {
        recents = { ...recents, [t.session_id]: r.messages };
      });
    }
  });

  const visible = $derived(
    cockpitStore.threads
      .filter((t) => !hiddenSessions.has(t.session_id))
      .filter((t) => cli === 'all' ? true : t.cli === cli)
      .filter((t) => showIdle ? true : cockpitStore.tick - t.last_msg_at < 30 * 60 * 1000)
      .filter((t) => !filter ||
        `${t.project_name} ${t.session_id} ${t.git_branch ?? ''}`.toLowerCase().includes(filter.toLowerCase())
      )
  );

  const liveCount = $derived(visible.filter((t) => cockpitStore.tick - t.last_msg_at < 30 * 60 * 1000).length);
  const idleCount = $derived(visible.length - liveCount);

  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && focusSessionId) { e.preventDefault(); focusSessionId = null; }
  }
</script>

<svelte:window onkeydown={onKey} />

<header class="page-head">
  <div>
    <h1>Live</h1>
    <p class="lede">Every AI terminal across every project, in one window. Multiple agents running in parallel stop falling off your radar.</p>
  </div>
  <div class="actions mono">
    <span class="ok"><span class="dot ok"></span>{liveCount} live</span>
    <span class="dim">· {idleCount} idle</span>
  </div>
</header>

<div class="toolbar">
  <div class="field"><Icon name="search" /><input placeholder="Filter project / branch / session" bind:value={filter} /></div>
  <div class="field">
    <span class="lbl">cli</span>
    <select bind:value={cli}><option value="all">all</option><option value="claude">claude</option><option value="codex">codex</option></select>
  </div>
  <label class="row"><input type="checkbox" bind:checked={showIdle} /> show idle</label>
  <div class="grow"></div>
  <span class="mono dim">30m recency window · sorted by activity</span>
</div>

<div class="grid">
  {#each visible as t (t.session_id)}
    <LiveTile thread={t} recent={recents[t.session_id] ?? []} tickMs={cockpitStore.tick} />
  {/each}
  {#if visible.length === 0}<div class="card empty">No sessions match these filters.</div>{/if}
</div>

{#if focusSessionId}
  <div class="overlay" onclick={() => (focusSessionId = null)} role="presentation">
    <div class="focus" onclick={(e) => e.stopPropagation()} role="dialog" tabindex="-1" aria-modal="true">
      {#each cockpitStore.threads.filter((t) => t.session_id === focusSessionId) as t}
        <Terminal thread={t} tickMs={cockpitStore.tick} onFocus={() => (focusSessionId = null)} onInfo={() => {}} />
      {/each}
    </div>
  </div>
{/if}

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 14px; }
  /* + the rest, ported from page-live.jsx inline styles. */
</style>
```

- [ ] **Step 2.4: Replace `routes/+page.svelte` body with Live**

Modify `ui/src/routes/+page.svelte`. Replace the entire `work` view with:

```svelte
<script lang="ts">
  import Live from '$lib/dashboard/pages/Live.svelte';
</script>

<svelte:head><title>klyne — Live</title></svelte:head>

<Live />
```

The old terminal-grid implementation is deleted because `Live.svelte` now owns that layout and `cockpit.svelte.ts` already owns the session order + 30-min recency rule. (If retained logic differs — e.g. ordering — port it into `Live.svelte` first.)

- [ ] **Step 2.5: Delete `routes/cockpit/+page.svelte`**

```bash
git rm ui/src/routes/cockpit/+page.svelte
rmdir ui/src/routes/cockpit
```

Add a redirect in `+layout.svelte` (inside `useNewShell` branch only):

```ts
import { beforeNavigate } from '$app/navigation';
beforeNavigate(({ to, cancel }) => {
  if (!to) return;
  if (to.url.pathname === '/cockpit') { cancel(); void goto('/'); }
});
```

- [ ] **Step 2.6: Verify**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# Visit /, observe the 3-col tile grid, click a tile → drawer URL appears,
# filter by cli / project / show-idle, hide a session, /cockpit redirects to /.
npm run check && npm test
```

- [ ] **Step 2.7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(ui): Live page replaces / and /cockpit

3-col tile grid powered by cockpitStore; each tile shows last 3
conversational messages, status dot, branch, ctx% with alert/warn
thresholds, and a hidden-session toggle. Clicking a tile opens the
SessionDrawer via ?session=. /cockpit redirects to /.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Projects page (replaces `/projects`, `/projects/[name]`, `/insights/projects/[name]`)

**Goal:** Master/detail layout with tabs Overview · Sessions · Worklog · Files. Selecting a project updates URL to `/projects/<name>`. Reuses `projects.svelte.ts`, `fetchProjectInsights`, `fetchWorklogProject`, `AgentMixDonut`, `DailyActivityChart`.

**Files:**
- Create: `ui/src/lib/dashboard/pages/Projects.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/ProjectsList.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/ProjectPanel.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/TabOverview.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/TabSessions.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/TabWorklog.svelte`
- Create: `ui/src/lib/dashboard/pages/projects/TabFiles.svelte`
- Modify: `ui/src/routes/projects/+page.svelte`
- Modify: `ui/src/routes/projects/[name]/+page.svelte`
- Delete: `ui/src/routes/insights/projects/[name]/+page.svelte`

- [ ] **Step 3.1: Read design source**

```bash
cat docs/design/2026-05-23-dashboard/dashboard/page-projects.jsx
```

Note master/detail proportions (280–320 px list · flex panel), state dot colour rules (fresh/stale/cold), tab `count` pill behaviour, `SessionRowSlim` grid template (60 / 1fr / 80 / 70 / 70 / 16).

- [ ] **Step 3.2: ProjectsList component**

Create `ui/src/lib/dashboard/pages/projects/ProjectsList.svelte`. Port `PageProjects` left-rail body from `page-projects.jsx` lines 73–116. Bind selection via callback prop:

```svelte
<script lang="ts">
  import { projectsStore } from '$lib/projects.svelte';
  interface Props {
    selected: string | null;
    onSelect: (name: string) => void;
    filter: string;
    cli: 'all' | 'claude' | 'codex';
    sort: 'recent' | 'sessions' | 'messages' | 'name';
  }
  const { selected, onSelect, filter, cli, sort }: Props = $props();

  const visible = $derived((projectsStore.projects ?? [])
    .filter((p) => cli === 'all' ? true : (p.clis ?? []).includes(cli))
    .filter((p) => p.name.toLowerCase().includes(filter.toLowerCase()))
    .slice()
    .sort((a, b) => {
      if (sort === 'sessions') return b.sessions - a.sessions;
      if (sort === 'messages') return b.messages - a.messages;
      if (sort === 'name')     return a.name.localeCompare(b.name);
      return (b.last_msg_at ?? 0) - (a.last_msg_at ?? 0);
    }));
</script>

<section class="card list">
  {#each visible as p (p.path)}
    <button
      class="row"
      class:selected={p.name === selected}
      onclick={() => onSelect(p.name)}
    >
      <span class="dot" class:ok={p.reflection_state === 'fresh'} class:warn={p.reflection_state === 'stale'}></span>
      <span class="name">{p.name}</span>
      <span class="ago mono">{p.last_msg_at_label}</span>
      <span class="row-2">
        {#each p.clis ?? [] as c}
          <span class="pill" class:claude={c === 'claude'} class:codex={c === 'codex'}>{c}</span>
        {/each}
        <span class="mono dim">{p.sessions} · {p.tokens_in_label}</span>
      </span>
      {#if p.reflection_state !== 'fresh' && p.new_since != null}
        <span class="pill" class:warn={p.reflection_state === 'stale'}>{p.reflection_state === 'stale' ? `${p.new_since} new since reflection` : 'no reflection yet'}</span>
      {/if}
    </button>
  {/each}
</section>

<style>/* port row visuals — selected row gets 2px left accent border + 7% accent bg */</style>
```

If `projectsStore.projects` fields differ, extend the store types — keep names in `snake_case` to match daemon JSON.

- [ ] **Step 3.3: Each tab component**

For each tab file:
- `TabOverview.svelte` — port `ProjectOverview` from `page-projects.jsx` lines 165–223. The 4 stat tiles + Daily-activity bar chart + Recent sessions list. **Add** an `AgentMixDonut` from `$lib/ui/AgentMixDonut.svelte` driven by `fetchProjectInsights({ name, since, until })` (preserve list).
- `TabSessions.svelte` — port `ProjectSessions` + `SessionRowSlim`, day-grouped. Row click → drawer (`sessionUrl(...)` from `url-state.ts`). Show eye-off toggle on each row.
- `TabWorklog.svelte` — fetch `fetchWorklogProject(p.path)`. Render the inline-reflection card same as the design. "See full →" links to `/worklog/project?path=<encoded>`.
- `TabFiles.svelte` — port `ProjectFilesTools` literally; data driven by `fetchProjectInsights` (`files_top`, `tools_top`) — if those fields don't exist yet, leave the file with a feature-flag-style "data pending" empty state and file an issue.

- [ ] **Step 3.4: ProjectPanel composes header + tabs**

Create `ui/src/lib/dashboard/pages/projects/ProjectPanel.svelte`. Header per `ProjectDetailHeader` (page-projects.jsx lines 147–163). Tab bar reads `?tab=` and uses `tabUrl` from `url-state.ts`. Default tab `overview`.

- [ ] **Step 3.5: Projects.svelte composes list + panel**

Create `ui/src/lib/dashboard/pages/Projects.svelte`:

```svelte
<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import ProjectsList from './projects/ProjectsList.svelte';
  import ProjectPanel from './projects/ProjectPanel.svelte';
  import Icon from '$lib/dashboard/Icon.svelte';
  import { projectsStore } from '$lib/projects.svelte';

  let filter = $state('');
  let cli = $state<'all' | 'claude' | 'codex'>('all');
  let sort = $state<'recent' | 'sessions' | 'messages' | 'name'>('recent');

  const selected = $derived($page.params.name ?? projectsStore.projects?.[0]?.name ?? null);

  function selectProject(name: string): void {
    const qs = $page.url.search; // preserves ?tab=
    void goto(`/projects/${encodeURIComponent(name)}${qs}`);
  }
</script>

<header class="page-head">
  <div>
    <h1>Projects</h1>
    <p class="lede">Every project klyne has indexed under <span class="mono">~/.claude/projects/</span> and <span class="mono">~/.codex/sessions/</span>.</p>
  </div>
  <div class="actions mono dim">{(projectsStore.projects ?? []).length} projects</div>
</header>

<div class="toolbar">
  <div class="field"><Icon name="search" /><input bind:value={filter} placeholder="Filter projects…" /></div>
  <div class="field"><span class="lbl">cli</span><select bind:value={cli}><option value="all">all</option><option value="claude">claude</option><option value="codex">codex</option></select></div>
  <div class="field"><span class="lbl">sort</span><select bind:value={sort}><option value="recent">recent</option><option value="sessions">sessions</option><option value="messages">messages</option><option value="name">name</option></select></div>
</div>

<div class="layout">
  <ProjectsList {selected} {filter} {cli} {sort} onSelect={selectProject} />
  {#if selected}<ProjectPanel projectName={selected} />{:else}<section class="card empty">No projects indexed yet.</section>{/if}
</div>

<style>.layout { display: grid; grid-template-columns: minmax(280px, 320px) minmax(0, 1fr); gap: 18px; align-items: start; }</style>
```

- [ ] **Step 3.6: Wire routes**

Modify `ui/src/routes/projects/+page.svelte` to just render the new component:

```svelte
<script>import Projects from '$lib/dashboard/pages/Projects.svelte';</script>
<svelte:head><title>klyne — Projects</title></svelte:head>
<Projects />
```

Same for `ui/src/routes/projects/[name]/+page.svelte` (also renders `<Projects />`; the component reads `$page.params.name`).

- [ ] **Step 3.7: Redirect old insights project URL**

Add to the `beforeNavigate` block in `+layout.svelte`:

```ts
if (to.url.pathname.startsWith('/insights/projects/')) {
  const name = to.url.pathname.replace('/insights/projects/', '');
  cancel();
  void goto(`/projects/${name}`);
}
```

Then `git rm` `ui/src/routes/insights/projects/[name]/+page.svelte` and the empty directory.

- [ ] **Step 3.8: Verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# Click rows, swap tabs, open a session → drawer, refresh on /projects/klyne?tab=worklog,
# confirm deep-link survives. /insights/projects/klyne redirects to /projects/klyne.
npm run check && npm test
git add -A
git commit -m "feat(ui): Projects merges projects index, detail, and insights drill-in" \
           -m "Master/detail with Overview/Sessions/Worklog/Files tabs. Session click opens SessionDrawer via ?session=. Old /insights/projects/[name] redirects to /projects/[name]." \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Insights page (replaces `/insights` + `/stats`; adds Daily tab)

**Goal:** 6 KPIs + 5 tabs (Overview · Activity · Models · **Daily** · Projects). The Daily tab preserves today's `/stats` per-day table.

**Files:**
- Create: `ui/src/lib/dashboard/pages/Insights.svelte`
- Create: `ui/src/lib/dashboard/pages/insights/TabOverview.svelte`
- Create: `ui/src/lib/dashboard/pages/insights/TabActivity.svelte`
- Create: `ui/src/lib/dashboard/pages/insights/TabModels.svelte`
- Create: `ui/src/lib/dashboard/pages/insights/TabDaily.svelte` (NEW)
- Create: `ui/src/lib/dashboard/pages/insights/TabProjects.svelte`
- Modify: `ui/src/routes/insights/+page.svelte`
- Delete: `ui/src/routes/stats/+page.svelte`

- [ ] **Step 4.1: Read sources**

```bash
cat docs/design/2026-05-23-dashboard/dashboard/page-insights.jsx
sed -n '1,80p' ui/src/routes/stats/+page.svelte
```

- [ ] **Step 4.2: Implement window selector + KPI strip in `Insights.svelte`**

Use `fetchUsageStats({ cli, days, heatmap_weeks: 12 })` for the strip + Activity heatmap. Use `fetchProjectInsights({ since, until })` for Projects + Overview top-projects table. Default window = 1d. Persist window in `?win=`. Use `tabUrl` to swap `?tab=`.

- [ ] **Step 4.3: Implement Overview, Activity, Models, Projects tabs**

Port each function in `page-insights.jsx` to a tab component. Reuse `AgentMixDonut`, `DailyActivityChart`, `SmoothSparkline`, `BarColumns` from `$lib/ui/`.

- [ ] **Step 4.4: Implement Daily tab (preserve `/stats` data)**

Create `ui/src/lib/dashboard/pages/insights/TabDaily.svelte`. Render `UsageStatsResponse.daily: DailyRow[]` as a sortable table:

| Date | Sessions | Turns | Input | Output | Cache read | Cache write | Cost |

```svelte
<script lang="ts">
  import { fetchUsageStats } from '$lib/api';
  import { kfmt, costFmt } from '$lib/format';
  import type { DailyRow, UsageStatsResponse } from '$lib/types';

  interface Props { days: number; cli: 'claude' | 'codex' | ''; }
  const { days, cli }: Props = $props();

  let data = $state<UsageStatsResponse | null>(null);
  $effect(() => { void fetchUsageStats({ cli: cli || undefined, days }).then((r) => (data = r)); });

  type SortKey = 'date' | 'sessions' | 'turns' | 'input' | 'output' | 'cost';
  let sortKey = $state<SortKey>('date');
  let sortDir = $state<'asc' | 'desc'>('desc');

  const rows = $derived((data?.daily ?? []).slice().sort((a, b) => {
    const dir = sortDir === 'asc' ? 1 : -1;
    if (sortKey === 'date')     return a.date.localeCompare(b.date) * dir;
    if (sortKey === 'sessions') return (a.sessions - b.sessions) * dir;
    if (sortKey === 'turns')    return (a.turns - b.turns) * dir;
    if (sortKey === 'input')    return (a.input_tokens - b.input_tokens) * dir;
    if (sortKey === 'output')   return (a.output_tokens - b.output_tokens) * dir;
    return (a.cost_usd - b.cost_usd) * dir;
  }));

  function sortBy(k: SortKey): void {
    if (k === sortKey) sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    else { sortKey = k; sortDir = k === 'date' ? 'desc' : 'desc'; }
  }
</script>

<div class="card">
  <div class="card-head"><div class="title">Daily breakdown <span class="sub">{rows.length} days</span></div></div>
  <table class="tbl">
    <thead>
      <tr>
        <th><button onclick={() => sortBy('date')}>Date</button></th>
        <th class="right"><button onclick={() => sortBy('sessions')}>Sessions</button></th>
        <th class="right"><button onclick={() => sortBy('turns')}>Turns</button></th>
        <th class="right"><button onclick={() => sortBy('input')}>Input</button></th>
        <th class="right"><button onclick={() => sortBy('output')}>Output</button></th>
        <th class="right">Cache read</th>
        <th class="right">Cache write</th>
        <th class="right"><button onclick={() => sortBy('cost')}>Cost</button></th>
      </tr>
    </thead>
    <tbody>
      {#each rows as r (r.date)}
        <tr>
          <td class="mono">{r.date}</td>
          <td class="num">{r.sessions}</td>
          <td class="num">{r.turns}</td>
          <td class="num">{kfmt(r.input_tokens)}</td>
          <td class="num">{kfmt(r.output_tokens)}</td>
          <td class="num">{kfmt(r.cache_read_tokens)}</td>
          <td class="num">{kfmt(r.cache_write_tokens)}</td>
          <td class="num">{costFmt(r.cost_usd)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
```

If `DailyRow` fields differ from the names above, update the `import type` line and the column accessors to match `ui/src/lib/types.ts` exactly. Do not invent fields.

- [ ] **Step 4.5: Route wire-up + redirect**

Replace `ui/src/routes/insights/+page.svelte`:

```svelte
<script>import Insights from '$lib/dashboard/pages/Insights.svelte';</script>
<svelte:head><title>klyne — Insights</title></svelte:head>
<Insights />
```

Delete `ui/src/routes/stats/+page.svelte`. Add to `beforeNavigate`:

```ts
if (to.url.pathname === '/stats') { cancel(); void goto('/insights?tab=daily'); }
```

- [ ] **Step 4.6: Verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# 6 KPIs render, all 5 tabs render, ?tab=daily survives reload, sort headers work,
# /stats redirects to /insights?tab=daily.
npm run check && npm test
git add -A
git commit -m "feat(ui): Insights merges /insights + /stats; adds Daily tab" \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Worklog page (replaces `/worklog`; keep `/worklog/project`)

**Goal:** Port `page-worklog.jsx` to Svelte while reusing today's `fetchWorklog` / `runReflect` plumbing. Cards link to `/worklog/project?path=` (kept).

**Files:**
- Create: `ui/src/lib/dashboard/pages/Worklog.svelte`
- Create: `ui/src/lib/dashboard/pages/worklog/WorklogCard.svelte`
- Modify: `ui/src/routes/worklog/+page.svelte`
- Leave alone: `ui/src/routes/worklog/project/+page.svelte` (still ISO-week drill-in)

- [ ] **Step 5.1: Port `WorklogCard.svelte` from design**

Use Svelte 5 runes for the `running` / `elapsed` state. Wire `▶ run` to the existing `runReflect` API + `AbortController` exactly the way today's `/worklog/+page.svelte` does (steps lifted: create AbortController, start timer, on success/error reset). Use `Icon` for copy / chevron.

- [ ] **Step 5.2: Port `PageWorklog` to `Worklog.svelte`**

Filter chips: all / stale / cold / fresh with counts. Render `WorklogCard` per project sorted by state priority then `lastActivity`. Add `<a href="/worklog/project?path={encodeURIComponent(w.path)}">see full →</a>` in card foot.

- [ ] **Step 5.3: Wire route + verify + commit**

```svelte
<!-- ui/src/routes/worklog/+page.svelte -->
<script>import Worklog from '$lib/dashboard/pages/Worklog.svelte';</script>
<svelte:head><title>klyne — Worklog</title></svelte:head>
<Worklog />
```

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# Run reflect on a stale card → "Running on the daemon · Ns" appears, stop kills it.
# "See full →" navigates to /worklog/project?path= and ISO-week drill-in works.
npm run check && npm test
git add -A
git commit -m "feat(ui): Worklog page rebuilt; ISO-week drill-in retained" \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Runbooks page

**Goal:** Sidebar of Scope/Project/Tags filters + card grid grouped Global / Project · <name>. Port `page-runbooks.jsx`.

**Files:**
- Create: `ui/src/lib/dashboard/pages/Runbooks.svelte`
- Create: `ui/src/lib/dashboard/pages/runbooks/RunbookCard.svelte`
- Modify: `ui/src/routes/runbooks/+page.svelte`

- [ ] **Step 6.1: Port both components**

Use today's runbook API (whatever `+page.svelte` currently calls). Tag toggles state; sidebar buttons set scope/project state.

- [ ] **Step 6.2: Wire route + verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
npm run check && npm test
git add -A
git commit -m "feat(ui): Runbooks page rebuilt with scope/tag sidebar + grouped cards" \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Advisors page

**Goal:** Port `page-advisors.jsx`. Filter chips by kind. Click → drawer.

**Files:**
- Create: `ui/src/lib/dashboard/pages/Advisors.svelte`
- Modify: `ui/src/routes/advisors/+page.svelte`
- Update: `ui/src/lib/dashboard/Shell.svelte` — wire `advisorCount`

- [ ] **Step 7.1: Port the page using `fetchAdvisories`**

Counts map: all / acceleration / topic_shift / stale_context / hard_ceiling. Row click → drawer via `sessionUrl(...)` from `url-state.ts`.

- [ ] **Step 7.2: Wire shell badge**

Add an `advisorsStore` (mirroring `cockpit.svelte.ts`) with a `total` count, refreshed in `+layout.svelte` alongside the existing refreshes. Replace the placeholder `0` in `Shell.svelte`'s `advisorCount`.

- [ ] **Step 7.3: Verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
npm run check && npm test
git add -A
git commit -m "feat(ui): Advisors page + sidebar unread badge" \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: SessionDrawer full body

**Goal:** Fill the drawer skeleton from Task 1 with: stats grid, context bar, token chart, KLYNE_SUMMARY card, advisors-in-session list, full message stream + tool calls, Restore-Context modal, resume command.

**Files:**
- Modify: `ui/src/lib/dashboard/SessionDrawer.svelte`
- Reuse: `MessageBubble.svelte`, `ToolCallBlock.svelte`, `RestoreContext.svelte`, `TokenTimelineChart.svelte`
- Reuse: existing session detail API (whatever `SessionDetail.svelte` calls today)

- [ ] **Step 8.1: Refactor `SessionDetail.svelte` interior into the drawer**

Open `ui/src/lib/components/SessionView.svelte` and `ui/src/lib/ui/SessionDetail.svelte` to identify the API + render pipeline they share. Copy the data-fetch into the drawer's onMount; render via the same `MessageBubble` / `ToolCallBlock` / `RestoreContext` / `TokenTimelineChart` components. Layout the body in the order spelled out in spec §5.8.

- [ ] **Step 8.2: Restore-Context modal**

Wire the `RestoreContext.svelte` affordance: when the session is compacted (`session.is_compacted === true`), surface the banner at the top of the body and let users open the existing restore modal.

- [ ] **Step 8.3: Resume command card**

```svelte
<section class="card">
  <header class="card-head"><div class="title">Resume command</div><button class="k-btn" onclick={copyResume}>copy</button></header>
  <pre class="mono">{cli} --resume {session.id}</pre>
</section>
```

- [ ] **Step 8.4: Delete the old session routes**

```bash
git rm ui/src/routes/sessions/[id]/+page.svelte
git rm ui/src/routes/insights/sessions/[id]/+page.svelte
rmdir ui/src/routes/sessions ui/src/routes/insights/sessions
```

Add redirects in `beforeNavigate`:

```ts
if (to.url.pathname.startsWith('/sessions/')) {
  const id = to.url.pathname.slice('/sessions/'.length);
  cancel(); void goto(`/?session=${encodeURIComponent(id)}`);
}
if (to.url.pathname.startsWith('/insights/sessions/')) {
  const id = to.url.pathname.slice('/insights/sessions/'.length);
  cancel(); void goto(`/insights?session=${encodeURIComponent(id)}`);
}
```

- [ ] **Step 8.5: Verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# Open drawer on a session, scroll the message stream, verify tool calls render,
# verify Restore-Context modal opens on a compacted session, copy resume command,
# /sessions/<id> + /insights/sessions/<id> redirect to ?session=<id>.
npm run check && npm test
git add -A
git commit -m "feat(ui): SessionDrawer renders full message stream + restore-context" \
           -m "Replaces /sessions/[id] and /insights/sessions/[id] with a slide-over drawer addressable via ?session=." \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: SearchPalette full body

**Goal:** ⌘K / `/` open palette. Empty state shows Try + Jump-to. Typing fires FTS. Picking a result navigates or opens drawer. Filters live as chips below the input.

**Files:**
- Modify: `ui/src/lib/dashboard/SearchPalette.svelte`
- Delete: `ui/src/routes/search/+page.svelte`
- Reuse: today's FTS endpoint (mirror the call from `routes/search/+page.svelte`)

- [ ] **Step 9.1: Port the palette body**

`palette.jsx` reference is in `docs/design/2026-05-23-dashboard/dashboard/shell.jsx` lines 104–181. Wire to today's search endpoint; pull `searchSuggest` strings from the existing search page.

- [ ] **Step 9.2: Add filter chips (cli, kind, project)**

Hosted directly below the input. State lives in `let cli = $state(...)`, `let kind = $state(...)`, etc. Each chip toggles its predicate.

- [ ] **Step 9.3: Pick handler**

```ts
function pick(hit: SearchHit): void {
  onClose();
  if (hit.kind === 'session') void goto(`/?session=${hit.session_id}`);
  else if (hit.kind === 'runbook') void goto(`/runbooks?id=${hit.id}`);
  else if (hit.project) void goto(`/projects/${hit.project}?session=${hit.session_id ?? ''}`);
}
```

- [ ] **Step 9.4: Delete `/search` + redirect**

```bash
git rm ui/src/routes/search/+page.svelte
rmdir ui/src/routes/search
```

```ts
if (to.url.pathname === '/search') { cancel(); void goto('/?palette=1'); }
```

- [ ] **Step 9.5: Verify + commit**

```bash
cd ui && PUBLIC_DASHBOARD_V2=1 npm run dev
# ⌘K and / open palette from every surface, typing fires FTS, picking jumps,
# Esc closes, /search redirects to /?palette=1.
npm run check && npm test
git add -A
git commit -m "feat(ui): SearchPalette replaces /search; ⌘K and / wired everywhere" \
           -m "Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Cutover + cleanup

**Goal:** Flip `PUBLIC_DASHBOARD_V2` default ON, retire the old shell, delete dead components.

**Files:**
- Modify: `ui/src/routes/+layout.svelte` — remove env branch, render `Shell` unconditionally
- Delete: `ui/src/lib/ui/TopNav.svelte`, `SearchOverlay.svelte`
- Delete: `ui/src/lib/components/SessionList.svelte`, `SessionView.svelte`
- Delete: `ui/src/lib/ui/ProjectRail.svelte`, `ProjectDetail.svelte`, `SessionDetail.svelte`
- Keep: `MessageBubble`, `ToolCallBlock`, `RestoreContext`, `TokenTimelineChart`, `Terminal`, `CliBadge`, `StatusBadge`, `Kbd`, `Inspector`, `AgentMixDonut`, `DailyActivityChart`, `SmoothSparkline`, `Sparkline`, `BarColumns`, all `productivity/*`
- Update or delete: `ui/src/lib/navlinks.ts` (if no longer used after drawer migration; otherwise simplify)

- [ ] **Step 10.1: Remove env branch in `+layout.svelte`**

Replace body with just `<Shell>{@render children()}</Shell>` plus the SSE / projects / cockpit refresh `onMount` logic kept verbatim.

- [ ] **Step 10.2: Delete retired components**

```bash
git rm ui/src/lib/ui/TopNav.svelte ui/src/lib/ui/SearchOverlay.svelte ui/src/lib/components/SessionList.svelte ui/src/lib/components/SessionView.svelte ui/src/lib/ui/ProjectRail.svelte ui/src/lib/ui/ProjectDetail.svelte ui/src/lib/ui/SessionDetail.svelte
```

If anything still imports them, fix the import or delete the import line. Run `npm run check` and resolve every type error before continuing.

- [ ] **Step 10.3: Simplify `navlinks.ts`**

If nothing imports it any more, `git rm` it and its `navlinks.test.ts`. If it's still referenced, keep only the functions still used and update the test file accordingly.

- [ ] **Step 10.4: Update redirects (final form)**

Keep the `beforeNavigate` redirect block for one release so deep-links survive. Wrap it in a comment block:

```ts
// Redirects from the old 15-route shell. Safe to delete once one release
// has shipped to users.
```

- [ ] **Step 10.5: Full sweep**

```bash
cd ui
npm run check                    # 0 errors
npm test                          # all green
npx svelte-check --threshold error
npm run dev                       # start once, click through every surface
```

Manual checklist:
- [ ] Sidebar collapses on /, expands on others (until manually toggled).
- [ ] ⌘K + / open palette from every route.
- [ ] Productivity is visually unchanged from `init`.
- [ ] Click a session row → drawer; refresh page → drawer still open.
- [ ] `/cockpit`, `/search`, `/stats`, `/sessions/<id>`, `/insights/sessions/<id>`, `/insights/projects/<name>` all redirect.
- [ ] `/worklog/project?path=…` still renders ISO-week drill-in.
- [ ] Insights → Daily tab matches `/stats` data 1:1.
- [ ] Hidden-session eye-off toggle works on Live + Projects → Sessions.

- [ ] **Step 10.6: Commit + summary**

```bash
git add -A
git commit -m "$(cat <<'EOF'
chore(ui): cut over to dashboard redesign and retire old shell

PUBLIC_DASHBOARD_V2 is now the only path. Retired TopNav, SearchOverlay,
SessionList, SessionView, ProjectRail, ProjectDetail, SessionDetail.
beforeNavigate redirects for /cockpit, /search, /stats, /sessions/<id>,
/insights/sessions/<id>, /insights/projects/<name> retained for one
release.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 10.7: Open PR**

```bash
git push -u origin feat/dashboard-redesign
gh pr create --base init --title "Dashboard redesign: 15 routes → 7 surfaces" --body "$(cat <<'EOF'
## Summary
- Unified 7-surface shell (Live · Productivity · Projects · Insights · Runbooks · Worklog · Advisors) with ⌘K palette + slide-over SessionDrawer.
- No feature dropped. Daily tab, ISO-week worklog drill-in, agent-mix donut, hidden-session toggle, Live focus modal, Restore-Context modal — all preserved per spec §4.
- Productivity page embedded verbatim — no visual diff.
- Spec: `docs/superpowers/specs/2026-05-23-dashboard-redesign-design.md`
- Plan: `docs/superpowers/plans/2026-05-23-dashboard-redesign.md`

## Test plan
- [ ] `npm run check` clean
- [ ] `npm test` green
- [ ] Click through every surface manually using the checklist in plan Task 10.5
- [ ] Verify every removed route still resolves via redirect

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review summary

- Spec §3.1 (7 surfaces) → Tasks 2–7 (one task per non-Productivity surface; Productivity untouched per spec §5.2).
- Spec §3.1 SessionDrawer → Task 1.12 skeleton, Task 8 full body.
- Spec §3.1 SearchPalette → Task 1.11 skeleton, Task 9 full body.
- Spec §3.3 sidebar collapse + ⌘K + `/` → Task 1.13 (`Shell.svelte` `onKey` + auto-collapse effect).
- Spec §4 locked decisions:
  - Tail-3 tiles + Drawer → Task 2.2 (`LiveTile`) + Task 8.
  - ⌘K + `/` → Task 1.13 + Task 9.
  - Full message stream + Restore-Context → Task 8.1–8.2.
  - Daily subtab → Task 4.4.
  - `/worklog/project?path=` retained → Task 5 leaves the route alone; Task 5.2 links to it.
  - Agent-mix donut on Projects → Overview → Task 3.3.
  - Hidden-session toggle → Task 2.2 (LiveTile) + Task 3.3 (TabSessions).
  - Live fullscreen focus modal → Task 2.3 (`focusSessionId` state + overlay).
- Spec §6 data flow (no backend changes) → all tasks reuse `fetchCockpitThreads`, `fetchAdvisories`, `fetchUsageStats`, `fetchProjectInsights`, `fetchWorklog`, `fetchWorklogProject`, `runReflect`, `fetchMessages`, FTS endpoint, SSE.
- Spec §7.1 commit cadence (10 commits) → Tasks 1–10 each end in a commit matching the spec's prefix.
- Spec §7.3 URL redirects → Tasks 2.5, 3.7, 4.5, 8.4, 9.4, 10.4.
- Spec §8 testing → Task 1.3–1.6 covers url-state TDD; checkpoints at end of every task.
