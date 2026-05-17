<!--
  Work view — terminal grid of currently-active sessions, with a project
  rail on the left and an inspector on the right.

  Ordering rule (deliberately boring): stable insertion order, newest
  session at the top. Once a tile is placed it never moves while it's
  visible. Only two events change position:
    - a new session appears → prepends at the top
    - a session falls past the 30-min recency window → drops out

  Streaming messages NEVER re-sort the grid. With three sessions
  streaming in parallel, recent-first ordering shuffled tiles every
  few seconds — unreadable. The "● N live" chip in the header is the
  finder for active work; it scrolls the latest-active tile into view
  without changing layout.
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { projectsStore } from '$lib/projects.svelte.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { fetchCockpitThreads } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import type { CockpitThread, MsgNew } from '$lib/types.js';
  import ProjectRail from '$lib/ui/ProjectRail.svelte';
  import Terminal from '$lib/ui/Terminal.svelte';
  import Inspector from '$lib/ui/Inspector.svelte';
  import AdvisorModal from '$lib/components/AdvisorModal.svelte';

  const LS_COLS = 'klyne.work.cols';
  const LS_RAIL = 'klyne.work.rail';
  const LS_INSP = 'klyne.work.inspector';

  function loadCols(): 1 | 2 | 3 {
    if (typeof localStorage === 'undefined') return 2;
    const v = parseInt(localStorage.getItem(LS_COLS) ?? '2', 10);
    return v === 1 || v === 3 ? v : 2;
  }
  function saveCols(c: number): void {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(LS_COLS, String(c));
  }
  function loadBool(key: string, fallback: boolean): boolean {
    if (typeof localStorage === 'undefined') return fallback;
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    return raw === 'true';
  }
  function saveBool(key: string, value: boolean): void {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(key, String(value));
  }

  let selectedPath = $state<string>('');
  let railOpen = $state(loadBool(LS_RAIL, true));
  let inspectorOpen = $state(loadBool(LS_INSP, true));
  let cols = $state<1 | 2 | 3>(loadCols());
  let advisorSession = $state<string | null>(null);
  let focusSessionId = $state<string | null>(null);
  let fullscreen = $state(false);
  let savedRailOpen = $state(true);
  let savedInspectorOpen = $state(true);

  // Live = within 1 min. Recent (kept visible) = within 30 min. Sessions
  // outside the 30-min window drop out of the grid.
  const LIVE_THRESHOLD_MS = 60_000;
  const RECENT_THRESHOLD_MS = 30 * 60 * 1000;
  const SINCE_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

  // tick drives "is this session still recent" recomputation without
  // re-fetching the server. Updated every 5s.
  let tick = $state(Date.now());

  // Session-level data for the grid. /cockpit/threads returns one row
  // per session_id — already the shape we want here.
  let threads = $state<Record<string, CockpitThread>>({});

  // Stable insertion order for the visible grid. Newest session at
  // index 0, oldest at the end. A session only enters this list when
  // first seen, and only leaves when last_msg_at falls past
  // RECENT_THRESHOLD_MS. Streaming messages never re-sort.
  let sessionOrder = $state<string[]>([]);

  const projects = $derived(projectsStore.items);

  // Seed selected project for the rail/inspector from the most-recent
  // project so the inspector isn't empty on first load.
  $effect(() => {
    if (!selectedPath && projects.length > 0) {
      selectedPath = projects[0].project_path;
    }
  });

  async function loadThreads(): Promise<void> {
    try {
      const since = Date.now() - SINCE_WINDOW_MS;
      const resp = await fetchCockpitThreads({ since, limit: 100 });
      const next: Record<string, CockpitThread> = {};
      for (const t of resp.threads) next[t.session_id] = t;
      threads = next;
    } catch {
      // Next periodic refresh will recover.
    }
  }

  // Maintain sessionOrder: prepend newly-seen sessions, drop ones that
  // aged out, keep all others exactly where they were. New sessions
  // arriving in a single fetch are sorted by last_msg_at desc among
  // themselves so a startup burst still lands in a meaningful order.
  $effect(() => {
    const recent = Object.values(threads).filter(
      (t) => tick - t.last_msg_at < RECENT_THRESHOLD_MS
    );
    const recentIds = new Set(recent.map((t) => t.session_id));
    untrack(() => {
      let next = sessionOrder.filter((id) => recentIds.has(id));
      const fresh = recent
        .filter((t) => !next.includes(t.session_id))
        .sort((a, b) => b.last_msg_at - a.last_msg_at)
        .map((t) => t.session_id);
      next = [...fresh, ...next];
      const changed =
        next.length !== sessionOrder.length ||
        next.some((id, i) => id !== sessionOrder[i]);
      if (changed) sessionOrder = next;
    });
  });

  const visibleThreads = $derived(
    sessionOrder
      .map((id) => threads[id])
      .filter((t): t is CockpitThread => t !== undefined)
  );

  const liveCount = $derived(
    visibleThreads.filter((t) => tick - t.last_msg_at < LIVE_THRESHOLD_MS).length
  );

  function selectProject(path: string): void {
    selectedPath = path;
    inspectorOpen = true;
  }

  function setCols(c: 1 | 2 | 3): void { cols = c; saveCols(c); }
  function toggleRail(): void {
    railOpen = !railOpen;
    saveBool(LS_RAIL, railOpen);
  }
  function toggleInspector(): void {
    inspectorOpen = !inspectorOpen;
    saveBool(LS_INSP, inspectorOpen);
  }
  function toggleFullscreen(): void {
    if (!fullscreen) {
      savedRailOpen = railOpen;
      savedInspectorOpen = inspectorOpen;
      railOpen = false;
      inspectorOpen = false;
      fullscreen = true;
      if (typeof document !== 'undefined') {
        const el = document.querySelector('.work') as HTMLElement | null;
        if (el?.requestFullscreen) {
          el.requestFullscreen().catch(() => { /* browser refused */ });
        }
      }
    } else {
      railOpen = savedRailOpen;
      inspectorOpen = savedInspectorOpen;
      fullscreen = false;
      if (typeof document !== 'undefined' && document.fullscreenElement && document.exitFullscreen) {
        document.exitFullscreen().catch(() => { /* already exited */ });
      }
    }
  }
  function onFullscreenChange(): void {
    if (typeof document === 'undefined') return;
    if (!document.fullscreenElement && fullscreen) {
      railOpen = savedRailOpen;
      inspectorOpen = savedInspectorOpen;
      fullscreen = false;
    }
  }

  $effect(() => {
    if (typeof document === 'undefined') return;
    if (fullscreen) {
      document.body.classList.add('klyne-fullscreen');
    } else {
      document.body.classList.remove('klyne-fullscreen');
    }
    return () => document.body.classList.remove('klyne-fullscreen');
  });

  const selectedProject: ProjectAggregate | null = $derived(
    projects.find((p) => p.project_path === selectedPath) ?? null
  );

  const focusThread: CockpitThread | null = $derived(
    focusSessionId ? threads[focusSessionId] ?? null : null
  );

  // Scroll the most-recently-active live tile into view. Position
  // doesn't change — we just bring the user's eye to it.
  function jumpToLive(): void {
    const liveT = visibleThreads
      .filter((t) => tick - t.last_msg_at < LIVE_THRESHOLD_MS)
      .sort((a, b) => b.last_msg_at - a.last_msg_at);
    const target = liveT[0];
    if (!target) return;
    const el = document.querySelector<HTMLElement>(
      `[data-session-id="${target.session_id}"]`
    );
    if (el?.scrollIntoView) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }

  function onKey(e: KeyboardEvent): void {
    const target = e.target as HTMLElement | null;
    const inField = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable);
    if (e.key === 'Escape') {
      if (focusSessionId) { e.preventDefault(); focusSessionId = null; return; }
      if (fullscreen) { e.preventDefault(); toggleFullscreen(); return; }
    }
    if (!inField && (e.key === 'f' || e.key === 'F') && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      toggleFullscreen();
    }
  }

  let sseUnsub: (() => void) | null = null;
  let tickHandle: ReturnType<typeof setInterval> | null = null;
  let refreshHandle: ReturnType<typeof setInterval> | null = null;

  // Patch last_msg_at in place for known sessions so the live indicator
  // updates immediately. Position never changes — the order list keys
  // off sessionOrder, not last_msg_at. Unknown session ids trigger a
  // refresh so the full thread row (project_path, cli, etc.) lands.
  function onMsgNew(ev: MsgNew): void {
    const existing = threads[ev.session_id];
    if (existing) {
      threads = {
        ...threads,
        [ev.session_id]: { ...existing, last_msg_at: ev.ts }
      };
    } else {
      void loadThreads();
    }
  }

  onMount(() => {
    void loadThreads();
    sseUnsub = subscribe({ onMsgNew });
    tickHandle = setInterval(() => { tick = Date.now(); }, 5_000);
    refreshHandle = setInterval(() => { void loadThreads(); }, 60_000);
    window.addEventListener('keydown', onKey);
    if (typeof document !== 'undefined') {
      document.addEventListener('fullscreenchange', onFullscreenChange);
    }
    return () => {
      sseUnsub?.();
      if (tickHandle !== null) clearInterval(tickHandle);
      if (refreshHandle !== null) clearInterval(refreshHandle);
      window.removeEventListener('keydown', onKey);
      if (typeof document !== 'undefined') {
        document.removeEventListener('fullscreenchange', onFullscreenChange);
      }
    };
  });
</script>

<svelte:head><title>klyne — Work</title></svelte:head>

<div
  class="work"
  class:inspector-collapsed={!inspectorOpen}
  class:rail-collapsed={!railOpen}
  class:work-fullscreen={fullscreen}
>
  <ProjectRail
    projects={projects}
    selectedPath={selectedPath}
    onSelect={selectProject}
  />

  <main class="center">
    <div class="center-hd">
      <button
        class="btn btn--ghost btn--sm btn--icon"
        title={railOpen ? 'Hide projects rail' : 'Show projects rail'}
        aria-label={railOpen ? 'Hide projects rail' : 'Show projects rail'}
        onclick={toggleRail}
      >{railOpen ? '‹' : '›'}</button>
      <h2>Terminals</h2>
      <span class="sub">
        {visibleThreads.length} session{visibleThreads.length === 1 ? '' : 's'}
        ·
        <button
          class="btn btn--ghost btn--sm live-chip"
          disabled={liveCount === 0}
          title={liveCount > 0 ? 'Scroll to the latest active session' : 'No sessions are currently streaming'}
          aria-label="Jump to latest live session"
          onclick={jumpToLive}
          style="display: inline-flex; align-items: center; gap: 4px; padding: 0 6px; font-size: 12px; color: {liveCount > 0 ? 'var(--ad-active, #10b981)' : 'var(--ad-faint)'};"
        ><span style="font-size: 9px;">●</span> {liveCount} live</button>
      </span>
      <div class="right">
        <div class="seg" role="group" aria-label="Terminal columns">
          <button class:active={cols === 1} onclick={() => setCols(1)}>1 col</button>
          <button class:active={cols === 2} onclick={() => setCols(2)}>2 col</button>
          <button class:active={cols === 3} onclick={() => setCols(3)}>3 col</button>
        </div>
        <button
          class="btn btn--ghost btn--sm btn--icon"
          title={fullscreen ? 'Exit fullscreen (Esc or F)' : 'Fullscreen (F)'}
          aria-label={fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
          onclick={toggleFullscreen}
        >⛶</button>
        <button class="btn btn--ghost btn--sm" onclick={toggleInspector}>
          {inspectorOpen ? 'hide inspector ›' : '‹ inspector'}
        </button>
      </div>
    </div>

    {#if visibleThreads.length === 0}
      <div class="term-grid cols-1" style="padding: 32px;">
        <div class="term-empty">
          <div>
            <h3>No active sessions</h3>
            <p>
              Nothing has been active in the last 30 minutes. Start a session
              in any CLI and it will appear here as its own tile.
            </p>
          </div>
        </div>
      </div>
    {:else}
      <div class="term-grid cols-{cols}">
        {#each visibleThreads as t (t.session_id)}
          <Terminal
            thread={t}
            tickMs={tick}
            onFocus={(id) => (focusSessionId = id)}
            onInfo={(sid) => (advisorSession = sid)}
          />
        {/each}
      </div>
    {/if}
  </main>

  {#if inspectorOpen && selectedProject}
    <Inspector project={selectedProject} onClose={() => (inspectorOpen = false)} />
  {/if}

  {#if focusThread}
    <div class="overlay" onclick={() => (focusSessionId = null)} role="presentation">
      <div class="search-modal" style="width: min(960px, 92vw); height: 78vh; display: flex; flex-direction: column;" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} role="dialog" tabindex="-1" aria-modal="true" aria-label="Focused terminal">
        <Terminal
          thread={focusThread}
          tickMs={tick}
          onFocus={() => (focusSessionId = null)}
          onInfo={(sid) => (advisorSession = sid)}
        />
      </div>
    </div>
  {/if}

  {#if advisorSession}
    <AdvisorModal sessionId={advisorSession} onClose={() => (advisorSession = null)} />
  {/if}
</div>
