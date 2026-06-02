<script lang="ts">
  import LiveTile from './LiveTile.svelte';
  import Icon from '$lib/dashboard/Icon.svelte';
  import Terminal from '$lib/ui/Terminal.svelte';
  import { cockpitStore } from '$lib/cockpit.svelte';
  import { hiddenSessionIds } from '$lib/hidden-sessions.svelte';
  import { fetchMessages } from '$lib/api';
  import type { CockpitThread, Message } from '$lib/types';

  const ACTIVE_MS = 30 * 60_000;

  let filter = $state('');
  let cli = $state<'all' | 'claude' | 'codex'>('all');
  let showIdle = $state(false);
  let focusSessionId = $state<string | null>(null);

  // Lazy-load tail messages per visible thread; cached by session_id.
  let recents = $state<Record<string, Message[]>>({});
  // The thread's msg_count at the time we cached recents[id]. We refetch
  // when the live msg_count advances past this, so the preview tracks the
  // stream instead of freezing on the first tail we ever fetched.
  const recentsKey: Record<string, number> = {};
  const inFlight = new Set<string>();

  $effect(() => {
    const threads = cockpitStore.threads;
    const currentIds = new Set(threads.map((t) => t.session_id));

    // Prune entries for sessions that have aged out of the cockpit window.
    const pruned: Record<string, Message[]> = {};
    for (const id of currentIds) if (recents[id]) pruned[id] = recents[id];
    if (Object.keys(pruned).length !== Object.keys(recents).length) recents = pruned;
    for (const id of Object.keys(recentsKey)) if (!currentIds.has(id)) delete recentsKey[id];

    // Fetch tail messages for sessions we haven't fetched yet, or whose
    // thread has advanced (msg_count grew) since our last fetch.
    for (const t of threads) {
      const id = t.session_id;
      const fresh = recents[id] && recentsKey[id] >= t.msg_count;
      if (fresh || inFlight.has(id)) continue;
      inFlight.add(id);
      const seenCount = t.msg_count;
      // Fetch conversational turns only (server-side filter): codex/Claude
      // sessions interleave many tool/system/empty-assistant rows between
      // prose, so a raw 10-row tail can be all noise → "no recent turns".
      // 20 newest turns is enough for the 3-message preview + an accurate
      // "expand · +N" hidden count.
      void fetchMessages(id, { limit: 20, order: 'desc', conversational: true })
        .then((r) => {
          if (!cockpitStore.threads.some((th) => th.session_id === id)) return;
          recentsKey[id] = seenCount;
          recents = { ...recents, [id]: r.messages };
        })
        .finally(() => { inFlight.delete(id); });
    }
  });

  const visible = $derived(
    cockpitStore.threads
      .filter((t) => !hiddenSessionIds().has(t.session_id))
      .filter((t) => (cli === 'all' ? true : t.cli === cli))
      .filter((t) => (showIdle ? true : cockpitStore.tick - t.last_msg_at < ACTIVE_MS))
      .filter((t) => {
        if (!filter) return true;
        const projectName =
          t.project_path.split('/').filter(Boolean).pop() ?? t.project_path;
        return (projectName + ' ' + t.session_id + ' ' + (t.git_branch ?? ''))
          .toLowerCase()
          .includes(filter.toLowerCase());
      })
  );

  // Header counts mirror what the grid actually shows: hidden sessions
  // (via the eye-off toggle) are excluded from both buckets so "N live · M idle"
  // stays consistent with the visible tile count. Without this, hiding the
  // only live session left the header reading "1 live" over an empty grid.
  const headerThreads = $derived(
    cockpitStore.threads.filter((t) => !hiddenSessionIds().has(t.session_id))
  );
  const liveCount = $derived(
    headerThreads.filter((t) => cockpitStore.tick - t.last_msg_at < ACTIVE_MS).length
  );
  const idleCount = $derived(headerThreads.length - liveCount);

  const focusThread = $derived(
    focusSessionId
      ? (cockpitStore.threads.find((t) => t.session_id === focusSessionId) ?? null)
      : null
  );

  // Esc closes the Live focus modal only — the SessionDrawer's Esc handler
  // lives in Shell.svelte and runs independently. The `&& focusSessionId`
  // guard keeps both handlers idempotent when both are unmounted/null.
  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && focusSessionId) {
      e.preventDefault();
      focusSessionId = null;
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="live-page">
  <!-- Page header -->
  <header class="page-head">
    <div>
      <h1>Live</h1>
      <p class="lede">
        Every AI terminal across every project, in one window. Multiple agents running in parallel
        stop falling off your radar — see which ones are awaiting your input, which are streaming,
        and what each one just said.
      </p>
    </div>
    <div class="actions">
      <span class="mono" style:color="var(--ok)" style:font-size="11px" style:white-space="nowrap">
        <span class="dot dot--live" style:display="inline-block" style:margin-right="6px"></span>
        {liveCount} live
      </span>
      <span class="mono dim" style:font-size="11px" style:white-space="nowrap">· {idleCount} idle</span>
    </div>
  </header>

  <!-- Toolbar -->
  <div class="toolbar">
    <div class="field" style:min-width="240px">
      <Icon name="search" />
      <input
        placeholder="Filter by project, branch, or session…"
        bind:value={filter}
      />
    </div>
    <div class="field">
      <span class="lbl">cli</span>
      <select bind:value={cli}>
        <option value="all">all</option>
        <option value="claude">claude</option>
        <option value="codex">codex</option>
      </select>
    </div>
    <label class="live-toolbar-check">
      <input type="checkbox" bind:checked={showIdle} />
      show idle
    </label>
    <div class="grow"></div>
    <span class="mono dim" style:font-size="11px" style:white-space="nowrap">
      30m recency window · sorted by activity
    </span>
  </div>

  <!-- Tile grid -->
  <div class="live-grid">
    {#each visible as t (t.session_id)}
      <LiveTile
        thread={t}
        recent={recents[t.session_id] ?? []}
        tickMs={cockpitStore.tick}
        onFocus={(id) => (focusSessionId = id)}
      />
    {/each}
    {#if visible.length === 0}
      <div class="card" style:padding="40px 20px" style:text-align="center" style:color="var(--fg-muted)">
        No sessions match these filters.
      </div>
    {/if}
  </div>
</div>

<!-- Focus modal -->
{#if focusThread}
  <div
    class="overlay"
    onclick={() => (focusSessionId = null)}
    role="presentation"
  >
    <div
      class="search-modal focus-wrap"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-label="Focused terminal"
    >
      <Terminal
        thread={focusThread}
        tickMs={cockpitStore.tick}
        onFocus={() => (focusSessionId = null)}
        onInfo={() => {}}
      />
    </div>
  </div>
{/if}

<style>
  .live-page {
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .live-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 14px;
    margin-top: 0;
  }

  .focus-wrap {
    width: min(960px, 92vw);
    height: 78vh;
    display: flex;
    flex-direction: column;
  }

  /* `.live-toolbar-check`, `.field`, `.lbl`, `.grow` are defined globally
     in $lib/styles/dashboard.css so they reach the markup despite Svelte's
     scoped styles. Keep page-only styles in this block. */
</style>
