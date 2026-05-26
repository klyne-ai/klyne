<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { search as apiSearch } from '$lib/api.js';
  import { projectsStore } from '$lib/projects.svelte.js';
  import { relTime } from '$lib/format.js';
  import { sessionMessageUrl } from './url-state.js';
  import type { SearchHit } from '$lib/types.js';
  import DOMPurify from 'dompurify';

  interface Props { onClose: () => void; }
  const { onClose }: Props = $props();

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  let q = $state('');
  let hits = $state<SearchHit[]>([]);
  let loading = $state(false);
  let selectedIndex = $state(-1);
  let inputEl = $state<HTMLInputElement | null>(null);
  let listEl = $state<HTMLElement | null>(null);
  let paletteEl = $state<HTMLElement | null>(null);

  // Filter state
  let filterCli = $state<'all' | 'claude' | 'codex'>('all');
  let filterRole = $state<'all' | 'user' | 'assistant'>('all');
  let filterProject = $state<string>('all');

  // Stale-fetch guard
  let generation = 0;

  // Debounce handle
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // ---------------------------------------------------------------------------
  // Derived
  // ---------------------------------------------------------------------------

  const SUGGESTIONS = ['payment webhook', 'race condition', 'refactor', 'stripe', 'redis', 'deadlock', 'retry'];

  const JUMP_LINKS: Array<{ label: string; url: string }> = [
    { label: 'Productivity · today', url: '/productivity' },
    { label: 'Projects', url: '/projects' },
    { label: 'Runbooks', url: '/runbooks' },
    { label: 'Insights', url: '/insights' },
  ];

  const projectOptions = $derived([
    'all',
    ...projectsStore.items.slice(0, 8).map((p) => p.name),
  ]);

  // TODO(backend): collapse into msg/session/runbook groups once SearchHit carries a 'kind' field.
  const filteredHits = $derived(
    hits.filter((h) => {
      if (filterCli !== 'all' && h.cli !== filterCli) return false;
      if (filterRole !== 'all' && h.role !== filterRole) return false;
      if (filterProject !== 'all') {
        const pName = h.project_path.split('/').filter(Boolean).pop() ?? '';
        if (pName !== filterProject) return false;
      }
      return true;
    })
  );

  // Reset cursor when hit list changes
  $effect(() => {
    if (filteredHits.length === 0 || selectedIndex >= filteredHits.length) {
      selectedIndex = -1;
    }
  });

  // ---------------------------------------------------------------------------
  // Search
  // ---------------------------------------------------------------------------

  function triggerSearch(value: string): void {
    if (debounceTimer !== null) clearTimeout(debounceTimer);
    if (!value.trim()) { hits = []; loading = false; return; }
    debounceTimer = setTimeout(() => { void doSearch(value); }, 200);
  }

  async function doSearch(value: string): Promise<void> {
    const gen = ++generation;
    loading = true;
    try {
      // Sort by recency (ts DESC), not FTS BM25 relevance. Users
      // searching this palette expect "show me what I worked on" — most
      // recent first — not "show me whatever the FTS scorer thinks is
      // the closest lexical match." The previous 'relevance' value
      // surfaced 12-day-old hits above 3-day-old ones for the same query.
      const res = await apiSearch(value, 25, 'recent');
      if (gen !== generation) return; // stale
      hits = res.hits;
    } catch {
      if (gen !== generation) return;
      hits = [];
    } finally {
      if (gen === generation) loading = false;
    }
  }

  function handleInput(e: Event): void {
    const value = (e.target as HTMLInputElement).value;
    q = value;
    selectedIndex = -1;
    triggerSearch(value);
  }

  // ---------------------------------------------------------------------------
  // Navigation helpers
  // ---------------------------------------------------------------------------

  function navigateAndClose(url: string): void {
    onClose();
    void goto(url);
  }

  function pickHit(hit: SearchHit): void {
    // Carry the message_id through so the SessionDrawer can scroll/
    // highlight the exact hit instead of dumping the user at the top
    // of a long session and making them re-find their search term.
    const current = $page.url.pathname + $page.url.search;
    const url = sessionMessageUrl(current, hit.session_id, hit.message_id);
    onClose();
    void goto(url);
  }

  function pickSuggestion(s: string): void {
    q = s;
    void doSearch(s);
  }

  // ---------------------------------------------------------------------------
  // Keyboard navigation
  // ---------------------------------------------------------------------------

  function scrollItemIntoView(idx: number): void {
    if (!listEl) return;
    const el = listEl.querySelector<HTMLElement>(`[data-idx="${idx}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') { e.preventDefault(); onClose(); return; }

    if (q.trim() && filteredHits.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        selectedIndex = (selectedIndex + 1) % filteredHits.length;
        scrollItemIntoView(selectedIndex);
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        selectedIndex = selectedIndex <= 0 ? filteredHits.length - 1 : selectedIndex - 1;
        scrollItemIntoView(selectedIndex);
        return;
      }
      if (e.key === 'Enter' && selectedIndex >= 0) {
        e.preventDefault();
        const hit = filteredHits[selectedIndex];
        if (hit) pickHit(hit);
        return;
      }
    }

    if (e.key === 'Tab') {
      if (!paletteEl) return;
      const focusables = paletteEl.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'
      );
      if (focusables.length === 0) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (e.shiftKey && active === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && active === last) { e.preventDefault(); first.focus(); }
    }
  }

  // ---------------------------------------------------------------------------
  // Snippet sanitization — only allow <mark> from FTS5 highlighting
  // ---------------------------------------------------------------------------

  function escapeRegExp(s: string): string {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  function renderSnippet(raw: string, query: string): string {
    if (!raw) return '';
    // Highlight every case-insensitive match of any query word.
    const words = query.trim().split(/\s+/).filter((w) => w.length >= 2);
    let highlighted = raw;
    for (const w of words) {
      const re = new RegExp(`(${escapeRegExp(w)})`, 'gi');
      highlighted = highlighted.replace(re, '<mark>$1</mark>');
    }
    return DOMPurify.sanitize(highlighted, { ALLOWED_TAGS: ['mark'], ALLOWED_ATTR: [] });
  }

  function projectName(path: string): string {
    return path.split('/').filter(Boolean).pop() ?? path;
  }

  function shortId(id: string): string {
    return id.slice(0, 8);
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="palette-scrim" onclick={onClose} role="presentation">
  <div
    bind:this={paletteEl}
    class="palette"
    onclick={(e) => e.stopPropagation()}
    onkeydown={(e) => e.stopPropagation()}
    role="dialog"
    aria-modal="true"
    aria-label="Search"
    tabindex="-1"
  >
    <!-- Input -->
    <div class="palette-input-wrap">
      <span class="palette-search-icon" aria-hidden="true">⌕</span>
      <input
        bind:this={inputEl}
        class="palette-input"
        autofocus
        autocomplete="off"
        spellcheck={false}
        aria-label="Search"
        placeholder="Search messages, sessions, projects…"
        value={q}
        oninput={handleInput}
      />
      {#if loading}
        <span class="palette-status" aria-live="polite">searching…</span>
      {:else if q.trim() && filteredHits.length > 0}
        <span class="palette-status">{filteredHits.length} hits</span>
      {/if}
    </div>

    <!-- Filter chips -->
    <div class="palette-filters" role="group" aria-label="Filters">
      <!-- CLI chips -->
      {#each (['all', 'claude', 'codex'] as const) as chip}
        <button
          class="k-chip"
          class:active={filterCli === chip}
          aria-pressed={filterCli === chip}
          onclick={() => { filterCli = chip; }}
        >{chip === 'all' ? 'Any CLI' : chip}</button>
      {/each}

      <span class="chip-divider" aria-hidden="true"></span>

      <!-- Role chips -->
      {#each (['all', 'user', 'assistant'] as const) as chip}
        <button
          class="k-chip"
          class:active={filterRole === chip}
          aria-pressed={filterRole === chip}
          onclick={() => { filterRole = chip; }}
        >{chip === 'all' ? 'Any role' : chip}</button>
      {/each}

      <span class="chip-divider" aria-hidden="true"></span>

      <!-- Project select -->
      <select
        class="k-chip k-chip-select"
        value={filterProject}
        onchange={(e) => { filterProject = (e.target as HTMLSelectElement).value; }}
        aria-label="Filter by project"
      >
        {#each projectOptions as opt}
          <option value={opt}>{opt === 'all' ? 'Any project' : opt}</option>
        {/each}
      </select>
    </div>

    <!-- Body -->
    {#if q.trim() === ''}
      <!-- Empty state: Try + Jump to -->
      <div class="palette-body">
        <div class="group">
          <div class="group-label">Try</div>
          {#each SUGGESTIONS as s}
            <button class="row" onclick={() => pickSuggestion(s)}>
              <span class="row-icon" aria-hidden="true">⌕</span>
              <span class="row-label">{s}</span>
              <span class="row-hint">search</span>
            </button>
          {/each}
        </div>

        <div class="group">
          <div class="group-label">Jump to</div>
          {#each JUMP_LINKS as link}
            <button class="row" onclick={() => navigateAndClose(link.url)}>
              <span class="row-icon" aria-hidden="true">›</span>
              <span class="row-label">{link.label}</span>
              <span class="row-hint">enter</span>
            </button>
          {/each}
        </div>
      </div>

    {:else if loading && hits.length === 0}
      <!-- Loading skeleton -->
      <div class="palette-body" aria-busy="true" aria-label="Searching">
        <div class="group">
          <div class="group-label">Searching…</div>
          {#each { length: 4 } as _}
            <div class="row skeleton">
              <span class="skel-icon"></span>
              <span class="skel-text"></span>
            </div>
          {/each}
        </div>
      </div>

    {:else if filteredHits.length === 0}
      <!-- No results -->
      <div class="palette-body">
        <div class="empty-state">
          No hits for <span class="mono">"{q}"</span>
          {#if filterCli !== 'all' || filterRole !== 'all' || filterProject !== 'all'}
            — try clearing filters
          {/if}
        </div>
      </div>

    {:else}
      <!-- Results -->
      <div
        bind:this={listEl}
        class="palette-body results-list"
        role="listbox"
        aria-label="Search results"
        aria-activedescendant={selectedIndex >= 0 ? `sr-${selectedIndex}` : undefined}
        tabindex="-1"
      >
        <div class="group">
          <div class="group-label">{filteredHits.length} hits{loading ? ' · refreshing' : ''}</div>
          {#each filteredHits as hit, i}
            {@const isSelected = i === selectedIndex}
            <button
              id="sr-{i}"
              data-idx={i}
              class="row result-row"
              class:selected={isSelected}
              role="option"
              aria-selected={isSelected}
              onclick={() => pickHit(hit)}
              onmouseenter={() => { selectedIndex = i; }}
            >
              <span class="row-icon" aria-hidden="true">
                {hit.role === 'assistant' ? '⬡' : hit.role === 'user' ? '⬟' : '⬢'}
              </span>
              <div class="result-body">
                <div class="result-snippet">
                  {@html renderSnippet(hit.snippet, q)}
                </div>
                <div class="result-meta">
                  <span class="meta-project">{projectName(hit.project_path)}</span>
                  <span class="meta-sep">·</span>
                  <span class="meta-session mono">{shortId(hit.session_id)}</span>
                  <span class="meta-sep">·</span>
                  <span class="meta-role">{hit.role}</span>
                  <span class="meta-sep">·</span>
                  <span class="meta-cli">{hit.cli}</span>
                  <span class="meta-ago">{relTime(hit.ts)}</span>
                </div>
              </div>
              <span class="row-hint" aria-hidden="true">↵</span>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Footer hint bar -->
    <div class="palette-footer" aria-hidden="true">
      <span><kbd>↑</kbd><kbd>↓</kbd> navigate</span>
      <span><kbd>↵</kbd> open session</span>
      <span><kbd>Esc</kbd> close</span>
    </div>
  </div>
</div>

<style>
  /* Scrim */
  .palette-scrim {
    position: fixed; inset: 0;
    background: color-mix(in oklch, var(--bg-inset, #0a0a0f) 70%, transparent);
    backdrop-filter: blur(2px);
    z-index: 60;
    display: grid; place-items: start center;
    padding-top: 10vh;
    animation: k-fadein .1s ease both;
  }

  /* Dialog */
  .palette {
    width: min(640px, 92vw);
    background: var(--bg, #111118);
    border: 1px solid var(--border-soft, rgba(255,255,255,0.08));
    border-radius: 12px;
    box-shadow: 0 20px 60px color-mix(in oklch, black 60%, transparent);
    overflow: hidden;
    display: flex; flex-direction: column;
    max-height: 72vh;
  }

  /* Input row */
  .palette-input-wrap {
    display: flex; align-items: center; gap: 10px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border-hair, rgba(255,255,255,0.05));
    flex-shrink: 0;
  }
  .palette-search-icon {
    font-size: 16px; color: var(--fg-dim, #555); flex-shrink: 0;
  }
  .palette-input {
    flex: 1;
    background: transparent; border: 0; outline: none;
    color: var(--fg, #e8e8f0);
    font-family: var(--font-sans, system-ui); font-size: 15px;
    min-width: 0;
  }
  .palette-input::placeholder { color: var(--fg-dim, #555); }
  .palette-status {
    font-size: 11px; color: var(--fg-muted, #888);
    font-family: var(--font-mono, monospace);
    flex-shrink: 0; white-space: nowrap;
  }

  /* Filters */
  .palette-filters {
    display: flex; align-items: center; gap: 4px;
    padding: 6px 14px;
    border-bottom: 1px solid var(--border-hair, rgba(255,255,255,0.05));
    flex-wrap: wrap;
    flex-shrink: 0;
  }
  .k-chip {
    background: transparent;
    border: 1px solid var(--border-soft, rgba(255,255,255,0.08));
    border-radius: 4px;
    color: var(--fg-muted, #888);
    cursor: pointer;
    font-size: 11px;
    font-family: var(--font-sans, system-ui);
    padding: 2px 8px;
    transition: background 80ms, color 80ms, border-color 80ms;
  }
  .k-chip:hover { background: var(--bg-card, #1a1a24); color: var(--fg, #e8e8f0); }
  .k-chip.active {
    background: var(--ad-claude-bg, #f0eeff);
    border-color: var(--ad-claude, #6d5aef);
    color: var(--ad-claude, #6d5aef);
  }
  .k-chip-select {
    appearance: none; cursor: pointer; outline: none; max-width: 120px;
  }
  .chip-divider {
    width: 1px; height: 14px;
    background: var(--border-soft, rgba(255,255,255,0.08));
    margin: 0 2px;
  }

  /* Body */
  .palette-body {
    overflow-y: auto; flex: 1;
    min-height: 0;
  }

  /* Groups */
  .group { padding: 6px 0; }
  .group + .group { border-top: 1px solid var(--border-hair, rgba(255,255,255,0.05)); }
  .group-label {
    padding: 6px 16px 4px;
    font-family: var(--font-mono, monospace);
    font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase;
    color: var(--fg-dim, #555);
  }

  /* Rows */
  .row {
    width: 100%;
    display: grid; grid-template-columns: 18px 1fr auto;
    gap: 10px; align-items: center;
    padding: 7px 16px;
    background: transparent; border: 0;
    cursor: pointer; text-align: left;
    transition: background 60ms;
    color: var(--fg, #e8e8f0);
    font-family: var(--font-sans, system-ui);
  }
  .row:hover, .row.selected { background: var(--bg-card, #1a1a24); }
  .row-icon {
    color: var(--fg-dim, #555); font-size: 13px;
    display: flex; align-items: center; justify-content: center;
  }
  .row-label { font-size: 13px; color: var(--fg, #e8e8f0); }
  .row-hint {
    font-size: 11px; color: var(--fg-dim, #555);
    font-family: var(--font-mono, monospace);
  }

  /* Result rows */
  .result-row { align-items: flex-start; }
  .result-body { min-width: 0; }
  .result-snippet {
    font-size: 13px; line-height: 1.5;
    color: var(--fg, #e8e8f0);
    overflow: hidden; text-overflow: ellipsis;
    display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;
    white-space: normal;
  }
  .result-meta {
    display: flex; align-items: center; gap: 4px;
    font-size: 11px; color: var(--fg-dim, #555);
    margin-top: 3px; flex-wrap: wrap;
  }
  .meta-project { font-weight: 600; color: var(--fg-muted, #888); }
  .mono { font-family: var(--font-mono, monospace); }
  .meta-session { font-family: var(--font-mono, monospace); font-size: 10px; }
  .meta-sep { opacity: 0.4; }
  .meta-ago { margin-left: auto; }

  /* Skeleton */
  .skeleton { pointer-events: none; }
  .skel-icon {
    width: 12px; height: 12px;
    border-radius: 2px;
    background: var(--border-soft, rgba(255,255,255,0.08));
    animation: k-pulse 1.2s ease-in-out infinite;
  }
  .skel-text {
    height: 12px; border-radius: 4px;
    background: var(--border-soft, rgba(255,255,255,0.08));
    animation: k-pulse 1.2s ease-in-out infinite;
    width: 60%;
  }

  /* Empty state */
  .empty-state {
    padding: 28px 20px;
    text-align: center;
    font-size: 13px; color: var(--fg-muted, #888);
  }
  .empty-state .mono { font-family: var(--font-mono, monospace); }

  /* Footer */
  .palette-footer {
    display: flex; gap: 16px; align-items: center;
    padding: 7px 16px;
    border-top: 1px solid var(--border-hair, rgba(255,255,255,0.05));
    flex-shrink: 0;
  }
  .palette-footer span {
    font-size: 11px; color: var(--fg-dim, #555);
    display: flex; align-items: center; gap: 4px;
  }
  kbd {
    display: inline-flex; align-items: center; justify-content: center;
    background: var(--bg-card, #1a1a24);
    border: 1px solid var(--border-soft, rgba(255,255,255,0.1));
    border-radius: 3px;
    font-family: var(--font-mono, monospace);
    font-size: 10px; color: var(--fg-muted, #888);
    padding: 1px 4px; min-width: 18px;
  }

  @keyframes k-fadein { from { opacity: 0; transform: translateY(-6px); } to { opacity: 1; transform: none; } }
  @keyframes k-pulse { 0%, 100% { opacity: 0.4; } 50% { opacity: 0.7; } }
</style>
