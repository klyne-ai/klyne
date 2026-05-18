<!--
  Work view — terminal grid of currently-active sessions.

  Ordering rule (deliberately boring): stable insertion order, newest
  session at the top. Once a tile is placed it never moves while it's
  visible. Only two events change position:
    - a new session appears → prepends at the top
    - a session falls past the 30-min recency window → drops out

  Streaming messages NEVER re-sort the grid. Column count is derived
  from tile count: 1 tile fills the view, 2 split half/half, 3 or
  more land in a 3-column grid (additional tiles wrap onto new rows).
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { fetchCockpitThreads } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import type { CockpitThread, MsgNew } from '$lib/types.js';
  import Terminal from '$lib/ui/Terminal.svelte';
  import AdvisorModal from '$lib/components/AdvisorModal.svelte';

  let advisorSession = $state<string | null>(null);
  let focusSessionId = $state<string | null>(null);

  // Recent (kept visible) = within 30 min. Sessions outside the
  // 30-min window drop out of the grid.
  const RECENT_THRESHOLD_MS = 30 * 60 * 1000;
  const SINCE_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

  // tick drives recency-window recomputation without re-fetching the
  // server. Updated every 5s.
  let tick = $state(Date.now());

  // Session-level data for the grid. /cockpit/threads returns one row
  // per session_id — already the shape we want here.
  let threads = $state<Record<string, CockpitThread>>({});

  // Stable insertion order for the visible grid. Newest session at
  // index 0, oldest at the end. A session only enters this list when
  // first seen, and only leaves when last_msg_at falls past
  // RECENT_THRESHOLD_MS. Streaming messages never re-sort.
  let sessionOrder = $state<string[]>([]);

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

  // Derive column count from tile count: 1 = full width, 2 = half/half,
  // 3+ = three columns (extra tiles wrap onto new rows).
  const cols = $derived<1 | 2 | 3>(
    visibleThreads.length <= 1 ? 1 : visibleThreads.length === 2 ? 2 : 3
  );

  const focusThread: CockpitThread | null = $derived(
    focusSessionId ? threads[focusSessionId] ?? null : null
  );

  // Esc closes the focus modal; F-key fullscreen toggle lives in TopNav.
  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && focusSessionId) {
      e.preventDefault();
      focusSessionId = null;
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
    return () => {
      sseUnsub?.();
      if (tickHandle !== null) clearInterval(tickHandle);
      if (refreshHandle !== null) clearInterval(refreshHandle);
      window.removeEventListener('keydown', onKey);
    };
  });
</script>

<svelte:head><title>klyne — Work</title></svelte:head>

<div class="work">
  <main class="center">
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

  {#if focusThread}
    <div class="overlay" onclick={() => (focusSessionId = null)} role="presentation">
      <div class="search-modal term-focus-wrap" style="width: min(960px, 92vw); height: 78vh; display: flex; flex-direction: column;" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} role="dialog" tabindex="-1" aria-modal="true" aria-label="Focused terminal">
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
