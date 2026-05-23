<!--
  Runbooks — ported to the new dashboard chrome.
  Two-column: 220px sidebar (Scope · Project · Tags) + grouped card grid.
  Groups: "Global" section first, then "Project · <name>" sections.
  Filters persist in URL: ?scope= ?project= ?tag= ?q=
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { fetchMemory } from '$lib/api.js';
  import { deleteMemory as _deleteMemory } from '$lib/api.js';
  import type { Decision, MemoryResponse, MemoryProjectGroup } from '$lib/types.js';
  import { relTime } from '$lib/format.js';
  import Icon from '$lib/dashboard/Icon.svelte';
  import RunbookCard from './runbooks/RunbookCard.svelte';

  // ---------------------------------------------------------------------------
  // Types
  // ---------------------------------------------------------------------------

  type Scope = 'all' | 'global' | 'project';

  interface RunbookEntry extends Decision {
    scope: 'global' | 'project';
    project_name: string;
    title: string;
    body: string;
  }

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  let resp = $state<MemoryResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Filters — read initial values from URL in onMount
  let scope = $state<Scope>('all');
  let selectedProject = $state<string>('');   // '' = all
  let activeTag = $state<string | null>(null);
  let query = $state('');

  // Debounce state for search input
  let rawQuery = $state('');
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // ---------------------------------------------------------------------------
  // Data loading
  // ---------------------------------------------------------------------------

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchMemory();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load runbooks';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    const sp = $page.url.searchParams;
    const s = sp.get('scope');
    if (s === 'global' || s === 'project') scope = s;
    selectedProject = sp.get('project') ?? '';
    activeTag = sp.get('tag');
    rawQuery = sp.get('q') ?? '';
    query = rawQuery;
    void load();
  });

  // ---------------------------------------------------------------------------
  // URL persistence helpers
  // ---------------------------------------------------------------------------

  function updateUrl(): void {
    const url = new URL(window.location.href);
    if (scope !== 'all') url.searchParams.set('scope', scope);
    else url.searchParams.delete('scope');
    if (selectedProject) url.searchParams.set('project', selectedProject);
    else url.searchParams.delete('project');
    if (activeTag) url.searchParams.set('tag', activeTag);
    else url.searchParams.delete('tag');
    if (query) url.searchParams.set('q', query);
    else url.searchParams.delete('q');
    void goto(url.pathname + url.search, { replaceState: true, noScroll: true, keepFocus: true });
  }

  function setScope(s: Scope): void {
    scope = s;
    updateUrl();
  }

  function setProject(p: string): void {
    selectedProject = p;
    updateUrl();
  }

  function toggleTag(t: string): void {
    activeTag = activeTag === t ? null : t;
    updateUrl();
  }

  function handleQueryInput(e: Event): void {
    rawQuery = (e.currentTarget as HTMLInputElement).value;
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      query = rawQuery;
      updateUrl();
    }, 150);
  }

  // ---------------------------------------------------------------------------
  // Derived data
  // ---------------------------------------------------------------------------

  function basename(p: string): string {
    if (!p) return '';
    const stripped = p.replace(/\/$/, '');
    const i = stripped.lastIndexOf('/');
    return i >= 0 ? stripped.slice(i + 1) : stripped;
  }

  function enrich(d: Decision): RunbookEntry {
    const project = d.project_path ?? '';
    const text = d.text ?? '';
    const nl = text.indexOf('\n');
    const title = (nl > 0 ? text.slice(0, nl) : text).trim() || '(untitled)';
    const body = (nl > 0 ? text.slice(nl + 1) : '').trim();
    return {
      ...d,
      scope: project ? 'project' : 'global',
      project_name: project ? basename(project) : '',
      title,
      body,
    };
  }

  const entries = $derived.by<RunbookEntry[]>(() => {
    if (!resp) return [];
    const flat: Decision[] = [];
    for (const g of resp.global) flat.push(g);
    for (const grp of resp.by_project) for (const m of grp.memories) flat.push(m);
    return flat.map(enrich);
  });

  const allTags = $derived.by<string[]>(() =>
    Array.from(new Set(entries.flatMap((e) => e.tags ?? []))).sort()
  );

  const projectOptions = $derived.by<string[]>(() =>
    Array.from(new Set(entries.map((e) => e.project_path ?? '').filter((p) => p !== ''))).sort()
  );

  // Counts for sidebar scope buttons
  const globalCount = $derived(entries.filter((e) => e.scope === 'global').length);
  const projectCount = $derived(entries.filter((e) => e.scope === 'project').length);

  const filtered = $derived.by<RunbookEntry[]>(() => {
    const q = query.trim().toLowerCase();
    return entries.filter((m) => {
      if (scope === 'global' && m.scope !== 'global') return false;
      if (scope === 'project' && m.scope !== 'project') return false;
      if (selectedProject !== '' && (m.project_path ?? '') !== selectedProject) return false;
      if (activeTag && !(m.tags ?? []).includes(activeTag)) return false;
      if (q) {
        const hay = `${m.title}\n${m.body}\n${(m.tags ?? []).join(' ')}\n${m.project_name}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  });

  interface Group {
    label: string;
    key: string;
    items: RunbookEntry[];
  }

  const groups = $derived.by<Group[]>(() => {
    // Build map preserving insertion order: Global always first, then Project · <name>
    const map = new Map<string, RunbookEntry[]>();
    for (const r of filtered) {
      const key = r.scope === 'global' ? 'Global' : `Project · ${r.project_name}`;
      const arr = map.get(key) ?? [];
      arr.push(r);
      map.set(key, arr);
    }
    // Sort so Global always leads
    const sorted: Group[] = [];
    for (const [label, items] of map) {
      sorted.push({ label, key: label, items });
    }
    sorted.sort((a, b) => {
      if (a.label === 'Global') return -1;
      if (b.label === 'Global') return 1;
      return a.label.localeCompare(b.label);
    });
    return sorted;
  });

  // ---------------------------------------------------------------------------
  // Actions
  // ---------------------------------------------------------------------------

  function handleExport(): void {
    // TODO: implement markdown export when API is available
    console.log('TODO: export runbooks as markdown');
  }

  function handleNewRunbook(): void {
    // TODO: implement new runbook creation when API is available
    console.log('TODO: new runbook — use `klyne remember this …` in your terminal');
  }

  onDestroy(() => {
    if (debounceTimer) clearTimeout(debounceTimer);
  });
</script>

<div class="runbooks-page">
  <!-- Page head -->
  <header class="page-head">
    <div class="head-left">
      <h1>Runbooks</h1>
      <p class="lede">
        Pre-execution memory klyne checks before Claude runs operational shell commands.
        Add via <span class="mono">klyne remember this …</span> (project-scoped) or
        <span class="mono">klyne remember this globally …</span>.
        Used to recall a known-good runbook instead of relying on session memory.
      </p>
    </div>
    <div class="head-actions">
      <button class="k-btn" onclick={handleNewRunbook}>+ new runbook</button>
      <button class="k-btn k-btn--ghost" onclick={handleExport}>
        <Icon name="copy" size={13} /> Export
      </button>
    </div>
  </header>

  <!-- Two-column layout -->
  <div class="two-col">
    <!-- Sidebar -->
    <aside class="sidebar card">
      <!-- Scope -->
      <div class="sidebar-section">
        <div class="kicker">Scope</div>
        <div class="scope-btns" role="group" aria-label="Filter by scope">
          {#each ([
            ['all',     `All (${entries.length})`],
            ['global',  `Global (${globalCount})`],
            ['project', `Project (${projectCount})`],
          ] as const) as [k, label] (k)}
            <button
              class="scope-btn"
              class:scope-btn--active={scope === k}
              onclick={() => setScope(k)}
              aria-pressed={scope === k}
            >
              {label}
            </button>
          {/each}
        </div>
      </div>

      <!-- Project -->
      <div class="sidebar-section">
        <div class="kicker">Project</div>
        <select
          class="proj-select"
          value={selectedProject}
          onchange={(e) => setProject((e.currentTarget as HTMLSelectElement).value)}
        >
          <option value="">All projects</option>
          {#each projectOptions as p (p)}
            <option value={p}>{basename(p)}</option>
          {/each}
        </select>
      </div>

      <!-- Tags -->
      {#if allTags.length > 0}
        <div class="sidebar-section">
          <div class="kicker">Tags</div>
          <div class="tag-cloud">
            {#each allTags as t (t)}
              <button
                class="tag-pill"
                class:tag-pill--active={activeTag === t}
                onclick={() => toggleTag(t)}
                aria-pressed={activeTag === t}
              >
                {t}
              </button>
            {/each}
          </div>
        </div>
      {/if}
    </aside>

    <!-- Right column -->
    <section class="content">
      <!-- Toolbar -->
      <div class="toolbar">
        <div class="search-field">
          <Icon name="search" size={14} />
          <input
            class="search-input"
            placeholder="Search title, body, tag…"
            value={rawQuery}
            oninput={handleQueryInput}
            aria-label="Search runbooks"
          />
          {#if rawQuery}
            <button
              class="clear-btn"
              onclick={() => { rawQuery = ''; query = ''; updateUrl(); }}
              aria-label="Clear search"
            >
              <Icon name="x" size={12} />
            </button>
          {/if}
        </div>
        <span class="filter-summary mono">
          {#if !loading}
            {filtered.length} of {entries.length}
          {/if}
        </span>
      </div>

      <!-- Loading / error states -->
      {#if loading && !resp}
        <p class="state-msg muted">Loading…</p>
      {:else if error}
        <p class="state-msg error-msg">⚠ {error}</p>
      {:else if !loading && filtered.length === 0}
        <p class="state-msg muted">No runbooks match. Try clearing filters.</p>
      {/if}

      <!-- Grouped cards -->
      {#each groups as g (g.key)}
        <div class="group">
          <div class="group-head">
            <h3 class="group-label">{g.label}</h3>
            <span class="mono muted group-count">{g.items.length} runbook{g.items.length === 1 ? '' : 's'}</span>
          </div>
          <div class="card-grid">
            {#each g.items as r (r.id)}
              <RunbookCard
                runbook={r}
                {activeTag}
                onTagClick={toggleTag}
                onDeleted={load}
              />
            {/each}
          </div>
        </div>
      {/each}
    </section>
  </div>
</div>

<style>
  .runbooks-page {
    padding: 0;
  }

  /* Page head */
  .page-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    margin-bottom: 24px;
  }

  .head-left {
    flex: 1;
    min-width: 0;
  }

  .page-head h1 {
    margin: 0 0 8px;
    font-size: 20px;
    font-weight: 600;
    letter-spacing: -0.02em;
    color: var(--fg);
  }

  .lede {
    margin: 0;
    font-size: 12.5px;
    color: var(--fg-soft);
    line-height: 1.5;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 11.5px;
  }

  .head-actions {
    display: flex;
    gap: 8px;
    flex-shrink: 0;
    padding-top: 2px;
  }

  /* k-btn base (matches existing design system) */
  .k-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 6px 12px;
    border-radius: 7px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg);
    font-size: 12px;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: background 0.1s;
  }

  .k-btn:hover {
    background: var(--bg-hover);
  }

  .k-btn--ghost {
    background: transparent;
    color: var(--fg-soft);
  }

  .k-btn--ghost:hover {
    background: var(--bg-card-2);
    color: var(--fg);
  }

  /* Two-column layout */
  .two-col {
    display: grid;
    grid-template-columns: 220px 1fr;
    gap: 20px;
    align-items: start;
  }

  /* Sidebar */
  .sidebar {
    position: sticky;
    top: 16px;
    padding: 14px;
    border-radius: 10px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card);
  }

  .sidebar-section {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .sidebar-section + .sidebar-section {
    margin-top: 16px;
  }

  .kicker {
    font-size: 10px;
    font-family: var(--font-mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--fg-muted);
    font-weight: 600;
  }

  .scope-btns {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .scope-btn {
    width: 100%;
    text-align: left;
    padding: 6px 10px;
    border-radius: 6px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--fg-soft);
    font-size: 12px;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: background 0.1s, color 0.1s, border-color 0.1s;
  }

  .scope-btn:hover {
    background: var(--bg-card-2);
    color: var(--fg);
  }

  .scope-btn--active {
    background: var(--bg-card-2);
    color: var(--fg);
    border-color: var(--border-hair);
  }

  .proj-select {
    width: 100%;
    padding: 6px 8px;
    background: var(--bg-card-2);
    color: var(--fg);
    border: 1px solid var(--border-hair);
    border-radius: 6px;
    font-size: 12px;
    font-family: var(--font-sans);
    outline: none;
    cursor: pointer;
  }

  .proj-select:focus {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px color-mix(in oklch, var(--accent) 16%, transparent);
  }

  .tag-cloud {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .tag-pill {
    font-size: 10.5px;
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg-soft);
    cursor: pointer;
    font-family: var(--font-sans);
    transition: background 0.1s, color 0.1s, border-color 0.1s;
  }

  .tag-pill:hover {
    background: var(--bg-hover);
    color: var(--fg);
  }

  .tag-pill--active {
    background: color-mix(in oklch, var(--accent) 18%, var(--bg-card-2));
    color: var(--accent);
    border-color: color-mix(in oklch, var(--accent) 40%, transparent);
  }

  /* Right column */
  .content {
    min-width: 0;
  }

  /* Toolbar */
  .toolbar {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 16px;
  }

  .search-field {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    border-radius: 8px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg-soft);
  }

  .search-field:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px color-mix(in oklch, var(--accent) 16%, transparent);
    color: var(--fg);
  }

  .search-input {
    flex: 1;
    border: none;
    outline: none;
    background: transparent;
    color: var(--fg);
    font-size: 12.5px;
    font-family: var(--font-sans);
  }

  .search-input::placeholder {
    color: var(--fg-muted);
  }

  .clear-btn {
    display: flex;
    align-items: center;
    border: none;
    background: none;
    cursor: pointer;
    color: var(--fg-muted);
    padding: 0;
    line-height: 1;
  }

  .clear-btn:hover {
    color: var(--fg);
  }

  .filter-summary {
    font-size: 11px;
    color: var(--fg-muted);
    white-space: nowrap;
  }

  /* State messages */
  .state-msg {
    padding: 40px 20px;
    text-align: center;
    font-size: 13px;
  }

  .muted {
    color: var(--fg-muted);
  }

  .error-msg {
    color: var(--alert, #e05);
  }

  /* Groups */
  .group {
    margin-bottom: 24px;
  }

  .group-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 10px;
  }

  .group-label {
    margin: 0;
    font-size: 13px;
    font-weight: 500;
    color: var(--fg);
  }

  .group-count {
    font-size: 11px;
  }

  /* Card grid */
  .card-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
    gap: 12px;
  }

  /* card class mirrors existing .card token */
  .card {
    background: var(--bg-card);
    border: 1px solid var(--border-hair);
    border-radius: 10px;
  }
</style>
