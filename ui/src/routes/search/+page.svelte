<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { search as apiSearch, type SearchSort } from '$lib/api.js';
  import { projectsStore } from '$lib/projects.svelte.js';
  import { relTime } from '$lib/format.js';
  import type { SearchHit } from '$lib/types.js';

  const urlQuery = $derived($page.url.searchParams.get('q') ?? '');

  let q = $state(urlQuery);
  let hits = $state<SearchHit[]>([]);
  let loading = $state(false);
  let tookMs = $state(0);
  let sort = $state<SearchSort>(($page.url.searchParams.get('sort') as SearchSort) || 'recent');
  let filters = $state<{ cli: string; role: string; project: string }>({
    cli: 'all',
    role: 'all',
    project: 'all',
  });

  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  const filteredHits = $derived(
    hits.filter((h) => {
      if (filters.cli !== 'all' && h.cli && h.cli !== filters.cli) return false;
      if (filters.role !== 'all' && h.role !== filters.role) return false;
      if (filters.project !== 'all') {
        const pName = h.project_path.split('/').filter(Boolean).pop() ?? '';
        if (pName !== filters.project) return false;
      }
      return true;
    })
  );

  const projectOptions = $derived([
    'all',
    ...projectsStore.items.slice(0, 6).map((p) => p.name),
  ]);

  async function doSearch(query: string): Promise<void> {
    if (!query.trim()) {
      hits = [];
      return;
    }
    loading = true;
    try {
      const res = await apiSearch(query, 50, sort);
      hits = res.hits;
      tookMs = res.took_ms;
    } catch {
      hits = [];
    } finally {
      loading = false;
    }
  }

  function setSort(next: SearchSort): void {
    if (next === sort) return;
    sort = next;
    const url = new URL(window.location.href);
    url.searchParams.set('sort', next);
    void goto(url.pathname + url.search, { replaceState: true, keepFocus: true });
    if (q.trim()) void doSearch(q);
  }

  function handleInput(value: string): void {
    q = value;
    if (debounceTimer !== null) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      const url = new URL(window.location.href);
      if (value.trim()) {
        url.searchParams.set('q', value);
      } else {
        url.searchParams.delete('q');
      }
      void goto(url.pathname + url.search, { replaceState: true, keepFocus: true });
      void doSearch(value);
    }, 300);
  }

  function clearFilters(): void {
    filters = { cli: 'all', role: 'all', project: 'all' };
  }

  const SUGGESTIONS = ['payment', 'race condition', 'refactor', 'stripe', 'redis', 'dedupe'];

  onMount(() => {
    const initialQ = $page.url.searchParams.get('q') ?? '';
    if (initialQ.trim()) {
      q = initialQ;
      void doSearch(initialQ);
    }
  });
</script>

<svelte:head>
  <title>{q ? `Search: ${q}` : 'Search'} — klyne</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1100px;">
  <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0 0 16px;">Search</h1>

  <!-- Search input -->
  <div style="position: relative; margin-bottom: 12px;">
    <input
      class="ad-input"
      autofocus
      value={q}
      oninput={(e) => handleInput((e.target as HTMLInputElement).value)}
      placeholder="payment webhook race condition…"
      style="height: 44px; font-size: 16px; padding-left: 40px; padding-right: 120px;"
    />
    <span style="position: absolute; left: 14px; top: 13px; font-size: 16px; color: var(--ad-faint);">⌕</span>
    <span class="ad-mono ad-faint" style="position: absolute; right: 14px; top: 14px; font-size: 12px;">
      {#if loading}searching…{:else}FTS5 · {filteredHits.length} hits{#if tookMs > 0} in {tookMs}ms{/if}{/if}
    </span>
  </div>

  <!-- Filters -->
  <div style="display: flex; gap: 6px; margin-bottom: 16px; flex-wrap: wrap; align-items: center;">
    {#each [
      { key: 'cli',     opts: ['all', 'claude', 'codex'] },
      { key: 'role',    opts: ['all', 'user', 'assistant', 'tool', 'system'] },
      { key: 'project', opts: projectOptions },
    ] as filterDef}
      <div style="display: flex; align-items: center; gap: 4px; background: var(--ad-bg-2); border: 1px solid var(--ad-border); border-radius: 4px; padding: 0 4px 0 10px; height: 26px;">
        <span style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">{filterDef.key}:</span>
        <select
          value={filters[filterDef.key as keyof typeof filters]}
          onchange={(e) => {
            const k = filterDef.key as keyof typeof filters;
            const v = (e.target as HTMLSelectElement).value;
            filters = { ...filters, [k]: v };
          }}
          style="background: transparent; border: 0; outline: 0; color: var(--ad-fg); font-size: 11px; padding-right: 4px; cursor: pointer; max-width: 140px;"
        >
          {#each filterDef.opts as o}
            <option value={o} style="background: var(--ad-bg);">{o}</option>
          {/each}
        </select>
      </div>
    {/each}
    <button class="ad-btn ad-btn--ghost ad-btn--sm" onclick={clearFilters}>clear filters</button>
    <div style="margin-left: auto; display: flex; align-items: center; gap: 6px;">
      <span style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">sort:</span>
      <div style="display: inline-flex; border: 1px solid var(--ad-border); border-radius: 4px; overflow: hidden;">
        <button
          class="ad-btn ad-btn--ghost ad-btn--sm"
          onclick={() => setSort('recent')}
          style="border-radius: 0; border: 0; background: {sort === 'recent' ? 'var(--ad-panel-hi)' : 'transparent'}; color: {sort === 'recent' ? 'var(--ad-fg)' : 'var(--ad-muted)'}; font-weight: {sort === 'recent' ? 600 : 500};"
        >recent</button>
        <button
          class="ad-btn ad-btn--ghost ad-btn--sm"
          onclick={() => setSort('relevance')}
          style="border-radius: 0; border: 0; border-left: 1px solid var(--ad-border); background: {sort === 'relevance' ? 'var(--ad-panel-hi)' : 'transparent'}; color: {sort === 'relevance' ? 'var(--ad-fg)' : 'var(--ad-muted)'}; font-weight: {sort === 'relevance' ? 600 : 500};"
        >best match</button>
      </div>
    </div>
  </div>

  <!-- Suggestions when empty -->
  {#if q.length === 0}
    <div class="ad-card" style="padding: 16px; margin-bottom: 16px;">
      <div class="ad-section-h" style="margin-bottom: 8px;">Try</div>
      <div style="display: flex; gap: 6px; flex-wrap: wrap;">
        {#each SUGGESTIONS as t}
          <button class="ad-btn ad-btn--ghost ad-btn--sm" onclick={() => { q = t; void doSearch(t); }}>{t}</button>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Results -->
  <div style="display: flex; flex-direction: column; gap: 6px;">
    {#each filteredHits as h, i}
      {@const pName = h.project_path.split('/').filter(Boolean).pop() ?? h.project_path}
      <div
        class="ad-card"
        style="padding: 12px; cursor: pointer; transition: background 80ms;"
        onmouseenter={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel-hi)')}
        onmouseleave={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel)')}
        onclick={() => goto(`/sessions/${encodeURIComponent(h.session_id)}`)}
        role="button"
        tabindex={i}
        onkeydown={(e) => e.key === 'Enter' && goto(`/sessions/${encodeURIComponent(h.session_id)}`)}
      >
        <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 6px; font-size: 12px;">
          <span style="font-weight: 600;">{pName}</span>
          <span class="ad-faint">/</span>
          <span class="ad-mono ad-muted" style="font-size: 11px; overflow: hidden; text-overflow: ellipsis; max-width: 200px;">{h.session_id}</span>
          <span class="ad-faint">·</span>
          <span class="ad-badge ad-badge--ghost" style="font-size: 10px;">{h.role}</span>
          <span style="margin-left: auto;" class="ad-mono ad-faint">{relTime(h.ts)}</span>
        </div>
        <div style="font-size: 13px; color: var(--ad-fg); line-height: 1.55;">
          {@html h.snippet.replaceAll('<mark>', `<mark style="background: var(--ad-claude-bg); color: var(--ad-claude); padding: 0 3px; border-radius: 2px;">`)}
        </div>
      </div>
    {/each}
    {#if filteredHits.length === 0 && q.length > 0 && !loading}
      <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">
        No hits for <span class="ad-mono">{q}</span>. Try clearing filters.
      </div>
    {/if}
  </div>

  <div style="display: flex; gap: 16px; margin-top: 24px; font-size: 11px; color: var(--ad-faint);">
    <span><span class="ad-kbd">↑</span><span class="ad-kbd">↓</span> select</span>
    <span><span class="ad-kbd">Enter</span> open session</span>
    <span><span class="ad-kbd">Esc</span> clear</span>
  </div>
</div>
