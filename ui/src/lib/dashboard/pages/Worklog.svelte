<!--
  Worklog — per-project reflection rollup page.
  Matches page-worklog.jsx PageWorklog design:
    - Page head: H1 + lede + project count
    - Filter chips: all · stale · cold · fresh (persisted in ?state=)
    - WorklogCard per rollup, ordered stale → cold → fresh then by last_activity desc
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { fetchWorklog } from '$lib/api.js';
  import type { WorklogProjectRollup, WorklogResponse } from '$lib/types.js';
  import WorklogCard from './worklog/WorklogCard.svelte';

  type StateFilter = 'all' | 'stale' | 'cold' | 'fresh';

  let resp = $state<WorklogResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Filter state — read from ?state= param, default 'all'
  const activeFilter = $derived.by<StateFilter>(() => {
    const v = $page.url.searchParams.get('state');
    if (v === 'stale' || v === 'cold' || v === 'fresh') return v;
    return 'all';
  });

  function setFilter(f: StateFilter): void {
    const url = new URL($page.url);
    if (f === 'all') url.searchParams.delete('state');
    else url.searchParams.set('state', f);
    void goto(url.toString(), { replaceState: true, keepFocus: true });
  }

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchWorklog();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load worklog';
    } finally {
      loading = false;
    }
  }

  onMount(() => { void load(); });

  // Classify each rollup into a state
  function classify(p: WorklogProjectRollup): 'stale' | 'cold' | 'fresh' {
    if (p.stale && p.latest_reflection) return 'stale';
    if (!p.latest_reflection) return 'cold';
    return 'fresh';
  }

  // Sorted + filtered list: stale → cold → fresh, then by last_activity desc within each group
  const sorted = $derived.by(() => {
    if (!resp) return [];
    const ORDER: Record<string, number> = { stale: 0, cold: 1, fresh: 2 };
    return [...resp.projects]
      .sort((a, b) => {
        const sa = classify(a), sb = classify(b);
        if (sa !== sb) return ORDER[sa] - ORDER[sb];
        return b.latest_entry_ts - a.latest_entry_ts;
      });
  });

  const filtered = $derived(
    sorted.filter((p) => activeFilter === 'all' ? true : classify(p) === activeFilter)
  );

  // Counts per state
  const counts = $derived.by(() => {
    const all = sorted;
    return {
      all:   all.length,
      stale: all.filter((p) => classify(p) === 'stale').length,
      cold:  all.filter((p) => classify(p) === 'cold').length,
      fresh: all.filter((p) => classify(p) === 'fresh').length,
    };
  });

  const CHIPS: { key: StateFilter; label: string; tone: 'warn' | 'ok' | 'muted' | null }[] = [
    { key: 'all',   label: 'all',   tone: null },
    { key: 'stale', label: 'stale', tone: 'warn'  },
    { key: 'cold',  label: 'cold',  tone: 'muted' },
    { key: 'fresh', label: 'fresh', tone: 'ok'    },
  ];
</script>

<div class="worklog-page">
  <header class="page-head">
    <div class="head-left">
      <h1>Worklog</h1>
      <p class="lede">
        Per-project reflection rollup. Each card shows the latest synthesized reflection written by
        <span class="mono">/klyne:reflect</span>.
        When new sessions land on top of the last reflection, the project flags stale — copy the
        command and run it in the project to refresh. The daemon never invokes the LM itself.
      </p>
    </div>
    <div class="head-right">
      {#if resp}
        <span class="mono dim proj-count">{resp.projects.length} project{resp.projects.length === 1 ? '' : 's'}</span>
      {/if}
    </div>
  </header>

  <!-- Filter chips -->
  <div class="chip-row" role="group" aria-label="Filter by reflection state">
    {#each CHIPS as chip}
      <button
        class="k-btn"
        class:k-btn--active={activeFilter === chip.key}
        onclick={() => setFilter(chip.key)}
        aria-pressed={activeFilter === chip.key}
      >
        {#if chip.tone}
          <span class="dot" class:dot-warn={chip.tone === 'warn'} class:dot-ok={chip.tone === 'ok'} class:dot-muted={chip.tone === 'muted'}></span>
        {/if}
        {chip.label}
        <span class="dim chip-count">{counts[chip.key]}</span>
      </button>
    {/each}
  </div>

  <!-- Content -->
  {#if loading && !resp}
    <p class="muted loading-msg">Loading…</p>
  {:else if error}
    <p class="error-msg">⚠ {error}</p>
    <p class="muted">Is the klyne daemon running?</p>
  {:else if resp}
    {#if resp.projects.length === 0}
      <p class="muted empty-msg">
        Nothing here yet. Once klyne's Stop hook captures work in a project, it appears here.
        Then run <span class="mono">/klyne:reflect</span> to synthesize.
      </p>
    {:else if filtered.length === 0}
      <p class="muted empty-msg">No {activeFilter} projects.</p>
    {:else}
      <div class="card-list">
        {#each filtered as w (w.project_path)}
          <WorklogCard {w} onReflected={load} />
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .worklog-page {
    padding: 1.5rem;
    max-width: 1100px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  /* ── Page head ── */
  .page-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }
  .head-left { flex: 1; min-width: 0; }
  .head-right {
    flex-shrink: 0;
    padding-top: 4px;
  }
  h1 {
    margin: 0 0 6px;
    font-size: 22px;
    font-weight: 600;
    color: var(--fg);
    letter-spacing: -0.02em;
  }
  .lede {
    margin: 0;
    font-size: 13px;
    color: var(--fg-muted);
    line-height: 1.55;
    max-width: 680px;
  }
  .proj-count { font-size: 11px; }

  /* ── Chips ── */
  .chip-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .chip-count {
    margin-left: 4px;
    font-size: 0.9em;
  }

  /* State dots */
  .dot {
    display: inline-block;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    margin-right: 4px;
    vertical-align: middle;
  }
  .dot-warn  { background: var(--warn); }
  .dot-ok    { background: var(--ok); }
  .dot-muted { background: var(--fg-muted); }

  /* ── Card list ── */
  .card-list {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  /* ── Status messages ── */
  .loading-msg { font-size: 14px; }
  .error-msg {
    color: var(--alert, #e05b5b);
    font-size: 14px;
    margin: 0;
  }
  .empty-msg {
    padding: 2rem;
    text-align: center;
    font-size: 14px;
  }
  .muted { color: var(--fg-muted); }
  .mono  { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .dim   { color: var(--fg-dim, var(--fg-muted)); opacity: 0.75; }
</style>
