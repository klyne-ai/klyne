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
  let showIdle = $state(true);
  let focusSessionId = $state<string | null>(null);

  // Lazy-load tail messages per visible thread; cached by session_id.
  let recents = $state<Record<string, Message[]>>({});
  const inFlight = new Set<string>();

  $effect(() => {
    const currentIds = new Set(cockpitStore.threads.map((t) => t.session_id));

    // Prune entries for sessions that have aged out of the cockpit window.
    const pruned: Record<string, Message[]> = {};
    for (const id of currentIds) if (recents[id]) pruned[id] = recents[id];
    if (Object.keys(pruned).length !== Object.keys(recents).length) recents = pruned;

    // Lazily fetch tail messages for sessions we haven't fetched yet.
    for (const id of currentIds) {
      if (recents[id] || inFlight.has(id)) continue;
      inFlight.add(id);
      void fetchMessages(id, { limit: 10, order: 'desc' })
        .then((r) => {
          if (!cockpitStore.threads.some((t) => t.session_id === id)) return;
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

  const liveCount = $derived(
    cockpitStore.threads.filter((t) => cockpitStore.tick - t.last_msg_at < ACTIVE_MS).length
  );
  const idleCount = $derived(cockpitStore.threads.length - liveCount);

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
        <span class="dot ok" style:display="inline-block" style:margin-right="6px"></span>
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
    <label class="show-idle">
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

  .show-idle {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    padding: 6px 10px;
    background: var(--bg-card);
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    font-size: 12px;
  }
</style>
