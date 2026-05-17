<!--
  Memory view — review-focused card grid over the /memory/items endpoint.
  Filter input, scope toggle (All / Global / Project), tag chips for
  drilling in, group-by control (scope / project / tag / none), and a
  per-card delete affordance.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { fetchMemory, deleteMemory } from '$lib/api.js';
  import type { Decision, MemoryResponse } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  type Scope = 'all' | 'global' | 'project';
  type GroupBy = 'scope' | 'project' | 'tag' | 'none';

  interface MemoryEntry extends Decision {
    /** Synthetic scope tag derived from project_path. */
    scope: 'global' | 'project';
    /** Project basename for display when scope === 'project'. */
    project_name: string;
    /** First line of text — used as the card title. */
    title: string;
    /** Remaining text after the title. */
    body: string;
  }

  let resp = $state<MemoryResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let query = $state('');
  let scope = $state<Scope>('all');
  let groupBy = $state<GroupBy>('scope');
  let activeTag = $state<string | null>(null);
  // '' = all projects. Initialized from ?project=... so URLs are shareable.
  let selectedProject = $state<string>('');

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchMemory();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load memories';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    selectedProject = $page.url.searchParams.get('project') ?? '';
    void load();
  });

  function onSelectProject(next: string): void {
    selectedProject = next;
    const url = new URL(window.location.href);
    if (next) url.searchParams.set('project', next);
    else url.searchParams.delete('project');
    void goto(url.pathname + url.search, {
      replaceState: true,
      noScroll: true,
      keepFocus: true,
    });
  }

  function basename(p: string): string {
    if (!p) return '';
    const stripped = p.replace(/\/$/, '');
    const i = stripped.lastIndexOf('/');
    return i >= 0 ? stripped.slice(i + 1) : stripped;
  }

  function enrich(d: Decision): MemoryEntry {
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
      body
    };
  }

  const entries: MemoryEntry[] = $derived.by(() => {
    if (!resp) return [];
    const flat: Decision[] = [];
    for (const g of resp.global) flat.push(g);
    for (const grp of resp.by_project) for (const m of grp.memories) flat.push(m);
    return flat.map(enrich);
  });

  const allTags: string[] = $derived.by(() =>
    Array.from(new Set(entries.flatMap((e) => e.tags ?? []))).sort()
  );

  // Distinct project_paths across loaded memories, alphabetized. Global memories
  // (project === '') are excluded — they're addressed via the scope toggle.
  const projectOptions: string[] = $derived.by(() =>
    Array.from(new Set(entries.map((e) => e.project_path ?? '').filter((p) => p !== ''))).sort()
  );

  const filtered: MemoryEntry[] = $derived.by(() => {
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
    items: MemoryEntry[];
  }

  const groups: Group[] = $derived.by(() => {
    if (groupBy === 'none') return [{ label: 'All memories', items: filtered }];
    const map = new Map<string, MemoryEntry[]>();
    for (const m of filtered) {
      let key: string;
      if (groupBy === 'scope') key = m.scope === 'global' ? 'Global (apply everywhere)' : 'Project-scoped';
      else if (groupBy === 'project') key = m.project_name || 'Global (no project)';
      else key = (m.tags ?? [])[0] ?? 'untagged';
      const arr = map.get(key) ?? [];
      arr.push(m);
      map.set(key, arr);
    }
    return Array.from(map.entries()).map(([label, items]) => ({ label, items }));
  });

  async function onDelete(id: string): Promise<void> {
    if (!confirm('Delete this memory? This cannot be undone.')) return;
    try {
      await deleteMemory(id);
      await load();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to delete memory';
    }
  }
</script>

<svelte:head><title>klyne — Memory</title></svelte:head>

<div class="page">
  <div class="page-hd">
    <h1>
      Memory
      {#if resp}<span class="count">({filtered.length})</span>{/if}
    </h1>
    <div class="row">
      <button class="btn btn--ghost btn--sm">Export markdown</button>
    </div>
  </div>
  <p class="page-sub">
    Decisions and runbooks klyne is remembering. Add via Claude Code:&nbsp;
    <code>klyne remember this …</code> (project-scoped) or
    <code>klyne remember this globally …</code>. Recall with
    <code>refer klyne …</code>. Use the Project dropdown to scope to a single repository.
  </p>

  <div class="toolbar">
    <input placeholder="Search title, body, tag, project…" bind:value={query} />
    <div class="seg">
      <button class:active={scope === 'all'} onclick={() => (scope = 'all')}>All</button>
      <button class:active={scope === 'global'} onclick={() => (scope = 'global')}>Global</button>
      <button class:active={scope === 'project'} onclick={() => (scope = 'project')}>Project</button>
    </div>
    <select
      class="proj-select"
      title={selectedProject || 'All projects'}
      value={selectedProject}
      onchange={(e) => onSelectProject((e.currentTarget as HTMLSelectElement).value)}
    >
      <option value="">All projects</option>
      {#each projectOptions as p (p)}
        <option value={p} title={p}>{basename(p)}</option>
      {/each}
    </select>
    <span class="spacer"></span>
    <span class="faint mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.08em;">group by</span>
    <div class="seg">
      <button class:active={groupBy === 'scope'} onclick={() => (groupBy = 'scope')}>scope</button>
      <button class:active={groupBy === 'project'} onclick={() => (groupBy = 'project')}>project</button>
      <button class:active={groupBy === 'tag'} onclick={() => (groupBy = 'tag')}>tag</button>
      <button class:active={groupBy === 'none'} onclick={() => (groupBy = 'none')}>none</button>
    </div>
  </div>

  {#if allTags.length > 0}
    <div class="toolbar" style="margin-top: -6px;">
      <span class="faint mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.08em; margin-right: 4px;">tags</span>
      {#each allTags as t (t)}
        <span class="tag" class:active={activeTag === t} onclick={() => (activeTag = activeTag === t ? null : t)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') activeTag = activeTag === t ? null : t; }}>{t}</span>
      {/each}
      {#if activeTag}
        <button class="btn btn--ghost btn--sm" onclick={() => (activeTag = null)}>clear</button>
      {/if}
    </div>
  {/if}

  {#if loading}
    <div style="padding: 40px 20px; text-align: center; color: var(--ad-faint);">Loading…</div>
  {:else if error}
    <div style="padding: 16px; border: 1px solid var(--ad-danger); border-radius: 8px; color: var(--ad-danger); margin-bottom: 16px;">{error}</div>
  {/if}

  {#if !loading && filtered.length === 0}
    <div style="padding: 40px 20px; text-align: center; color: var(--ad-faint);">
      No memories match. Try clearing filters.
    </div>
  {/if}

  {#each groups as g (g.label)}
    <section style="margin-bottom: 28px;">
      <header style="display: flex; align-items: baseline; gap: 8px; margin-bottom: 10px;">
        <h2 style="margin: 0; font-size: 15px; font-weight: 600; letter-spacing: -0.01em;">{g.label}</h2>
        <span class="muted" style="font-size: 12px;">{g.items.length} {g.items.length === 1 ? 'memory' : 'memories'}</span>
      </header>
      <div class="mem-grid">
        {#each g.items as m (m.id)}
          <article class="mem-card">
            <div class="mem-card-hd">
              <span class="scope {m.scope}">{m.scope === 'global' ? 'global' : m.project_name}</span>
              <span class="id">{m.id.slice(0, 12)}</span>
              <span class="when">{relTime(m.ts)}</span>
            </div>
            <div class="title">{m.title}</div>
            {#if m.body}<pre>{m.body}</pre>{/if}
            <div class="mem-card-foot">
              {#each m.tags ?? [] as t (t)}
                <span class="tag" class:active={activeTag === t} onclick={() => (activeTag = activeTag === t ? null : t)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') activeTag = activeTag === t ? null : t; }}>{t}</span>
              {/each}
              <span class="spacer"></span>
              <button class="btn btn--ghost btn--sm" style="color: var(--ad-danger);" onclick={() => onDelete(m.id)}>delete</button>
            </div>
          </article>
        {/each}
      </div>
    </section>
  {/each}
</div>

<style>
  /* Project filter dropdown — mirrors the .toolbar input styling so it sits
     naturally next to the scope segment buttons. */
  .proj-select {
    padding: 7px 10px;
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    color: var(--ad-fg);
    border-radius: 7px;
    font-size: 12.5px;
    outline: none;
    font-family: var(--ad-font);
    max-width: 200px;
    cursor: pointer;
  }
  .proj-select:focus {
    border-color: var(--ad-accent);
    box-shadow: 0 0 0 3px color-mix(in oklch, var(--ad-accent) 16%, transparent);
  }
</style>
