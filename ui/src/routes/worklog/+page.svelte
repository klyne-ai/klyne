<!--
  Worklog view — project-scoped browser over the /worklog/items endpoint.
  Card grid showing every stop_summaries row including suppressed (recap_visible=0)
  ones, so users can audit klyne's worklog signal-vs-noise behavior in one glance.

  v1 scope: project filter dropdown only. No tag chips, group-by, search, or delete.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { fetchWorklog } from '$lib/api.js';
  import type { WorklogEntry, WorklogProjectGroup, WorklogResponse } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  let resp = $state<WorklogResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let selectedProject = $state<string>(''); // '' = all
  let expanded = $state<Record<string, boolean>>({});

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchWorklog(selectedProject || undefined);
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load worklog';
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
    window.history.replaceState({}, '', url.toString());
    void load();
  }

  function entryKey(e: WorklogEntry): string {
    return `${e.session_id}:${e.ts}`;
  }

  function toggle(e: WorklogEntry): void {
    const k = entryKey(e);
    expanded[k] = !expanded[k];
  }

  function title(e: WorklogEntry): string {
    const t = (e.last_user || e.summary || '(no prompt)').trim();
    return t.length > 120 ? t.slice(0, 117) + '…' : t;
  }

  // Flatten by_project + global into a single list of [groupName, entries] pairs
  // so the template stays simple. Global comes first if present.
  function buckets(r: WorklogResponse): WorklogProjectGroup[] {
    const out: WorklogProjectGroup[] = [];
    if (r.global.length > 0) {
      out.push({ project_path: '', name: 'Global', entries: r.global, count: r.global.length });
    }
    for (const g of r.by_project) out.push(g);
    return out;
  }

  // Project options for the dropdown — current response groups + an "All" choice.
  function projectOptions(r: WorklogResponse): { value: string; label: string }[] {
    return [
      { value: '', label: 'All projects' },
      ...r.by_project.map((g) => ({ value: g.project_path, label: g.name })),
    ];
  }
</script>

<div class="worklog-page">
  <header class="worklog-header">
    <h1>Worklog</h1>
    <p class="muted">
      Everything klyne's Stop hook captured. Suppressed rows are shown so you can
      see what signal vs noise the suppression rules filter.
    </p>
  </header>

  {#if loading && !resp}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">⚠ {error}</p>
    <p class="muted">Is the klyne daemon running?</p>
  {:else if resp}
    <div class="filter-bar">
      <label>
        Project:
        <select value={selectedProject} onchange={(e) => onSelectProject((e.currentTarget as HTMLSelectElement).value)}>
          {#each projectOptions(resp) as opt}
            <option value={opt.value}>{opt.label}</option>
          {/each}
        </select>
      </label>
      <span class="muted">{resp.total} {resp.total === 1 ? 'entry' : 'entries'}</span>
    </div>

    {#if resp.total === 0}
      <p class="muted empty">No worklog entries yet for this scope.</p>
    {:else}
      {#each buckets(resp) as group (group.project_path || '__global__')}
        <section class="group">
          <h2>{group.name} <span class="count">({group.count})</span></h2>
          <div class="grid">
            {#each group.entries as entry (entryKey(entry))}
              <article class="card" class:suppressed={entry.recap_visible === 0}>
                <header class="card-head">
                  <span class="badge cli">[{entry.cli}]</span>
                  <span class="muted">{relTime(entry.ts)}</span>
                  <span class="dot">·</span>
                  <span class="muted">imp {entry.importance}</span>
                  {#if entry.recap_visible === 1}
                    <span class="pill visible">★ visible</span>
                  {:else}
                    <span class="pill suppressed">✗ suppressed</span>
                  {/if}
                </header>
                <p class="title">{title(entry)}</p>
                <button class="expand" onclick={() => toggle(entry)} aria-expanded={!!expanded[entryKey(entry)]}>
                  {expanded[entryKey(entry)] ? '▾ Hide' : '▸ Expand'}
                </button>
                {#if expanded[entryKey(entry)]}
                  <div class="card-detail">
                    {#if entry.last_bash}
                      <p class="muted small">Last bash:</p>
                      <pre>{entry.last_bash}</pre>
                    {/if}
                    {#if entry.files.length > 0}
                      <p class="muted small">Files touched:</p>
                      <ul class="files">
                        {#each entry.files as f}<li><code>{f}</code></li>{/each}
                      </ul>
                    {/if}
                    <p class="muted small">Summary:</p>
                    <pre class="summary">{entry.summary}</pre>
                    <p class="muted small footer">
                      <code>{entry.session_id}</code>
                      {#if entry.signature}· sig <code>{entry.signature}</code>{/if}
                    </p>
                  </div>
                {/if}
              </article>
            {/each}
          </div>
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .worklog-page { padding: 1.5rem; max-width: 1400px; margin: 0 auto; }
  .worklog-header h1 { margin: 0 0 0.25rem; }
  .muted { color: var(--text-muted, #888); }
  .small { font-size: 0.85em; }
  .error { color: var(--text-error, #c33); }
  .filter-bar {
    display: flex; align-items: center; gap: 1rem;
    padding: 0.75rem 0; border-bottom: 1px solid var(--border, #2a2a2a); margin-bottom: 1rem;
  }
  .filter-bar select {
    padding: 0.25rem 0.5rem; background: var(--surface, #1a1a1a);
    color: var(--text, #eee); border: 1px solid var(--border, #2a2a2a);
    border-radius: 4px;
  }
  .group { margin-bottom: 2rem; }
  .group h2 { margin: 0 0 0.75rem; font-size: 1.1rem; }
  .count { color: var(--text-muted, #888); font-weight: normal; font-size: 0.9em; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 1rem;
  }
  .card {
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-radius: 6px;
    padding: 0.75rem;
  }
  .card.suppressed { opacity: 0.65; }
  .card-head {
    display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap;
    font-size: 0.85em; margin-bottom: 0.5rem;
  }
  .badge.cli {
    padding: 0.1rem 0.4rem; border-radius: 3px;
    background: var(--surface-2, #222); font-family: monospace;
  }
  .dot { color: var(--text-muted, #666); }
  .pill {
    padding: 0.1rem 0.4rem; border-radius: 999px; font-size: 0.75em;
  }
  .pill.visible { background: #1e3a1e; color: #8ec98e; }
  .pill.suppressed { background: #3a1e1e; color: #c98e8e; }
  .title {
    margin: 0.25rem 0;
    font-weight: 500;
    line-height: 1.4;
  }
  .expand {
    background: none; border: none; color: var(--text-muted, #888);
    padding: 0.25rem 0; cursor: pointer; font-size: 0.85em;
  }
  .expand:hover { color: var(--text, #eee); }
  .card-detail {
    border-top: 1px solid var(--border, #2a2a2a);
    margin-top: 0.5rem; padding-top: 0.5rem;
  }
  pre {
    white-space: pre-wrap; word-break: break-word;
    background: var(--surface-2, #0d0d0d);
    padding: 0.5rem; border-radius: 4px; font-size: 0.85em;
    margin: 0.25rem 0 0.5rem;
  }
  .summary { max-height: 300px; overflow: auto; }
  .files { margin: 0.25rem 0 0.5rem 1rem; padding: 0; }
  .files li { list-style: disc; }
  .footer { margin-top: 0.5rem; }
  .empty { text-align: center; padding: 2rem; }
</style>
