<!--
  Worklog drill-in — every daily reflection for one project, grouped by
  ISO-week. Driven by ?path=<abs-project-path>. Linked from the top-level
  /worklog cards.
-->
<script lang="ts">
  import { page } from '$app/stores';
  import { fetchWorklogProject } from '$lib/api.js';
  import type { Reflection, WorklogProjectResponse } from '$lib/types.js';
  import { isoWeek, relTime } from '$lib/format.js';

  let resp = $state<WorklogProjectResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let collapsed = $state<Record<string, boolean>>({});

  const path = $derived($page.url.searchParams.get('path') ?? '');

  async function load(): Promise<void> {
    if (!path) {
      error = 'missing ?path=<abs-project-path>';
      loading = false;
      return;
    }
    loading = true;
    try {
      resp = await fetchWorklogProject(path);
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load project';
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    // Read path inside the effect so it becomes a tracked dep; SvelteKit
    // treats /worklog/project?path=/a and ?path=/b as the same route, so
    // without this re-run we'd leave stale `resp` on screen across nav.
    // Resetting collapsed prevents week-state from carrying across
    // projects (week labels may collide).
    void path;
    collapsed = {};
    void load();
  });

  // Bucket reflections by ISO-week, newest week first. Within a week we
  // keep the server's newest-first order (which is already by ts DESC).
  function byWeek(rs: Reflection[]): { week: string; rows: Reflection[] }[] {
    const order: string[] = [];
    const map = new Map<string, Reflection[]>();
    for (const r of rs) {
      const w = isoWeek(r.ts);
      if (!map.has(w)) { map.set(w, []); order.push(w); }
      map.get(w)!.push(r);
    }
    return order.map((w) => ({ week: w, rows: map.get(w)! }));
  }

  function toggle(week: string): void {
    collapsed = { ...collapsed, [week]: !collapsed[week] };
  }
</script>

<div class="page">
  <header class="head">
    <p class="muted small"><a href="/worklog">← Worklog</a></p>
    <h1>{resp?.project.name ?? path}</h1>
    <p class="path muted small"><code>{path}</code></p>

    {#if resp}
      <p class="meta muted">
        {resp.reflections.length} daily reflection{resp.reflections.length === 1 ? '' : 's'}
        {#if resp.project.latest_entry_ts > 0}
          · last activity {relTime(resp.project.latest_entry_ts)}
        {/if}
        {#if resp.project.stale && resp.project.latest_reflection}
          · <span class="status stale">{resp.project.pending_entries} pending</span>
        {/if}
      </p>
    {/if}
  </header>

  {#if loading && !resp}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">⚠ {error}</p>
  {:else if resp}
    {#if resp.reflections.length === 0}
      <p class="muted empty">
        No daily reflections for this project yet. Run
        <code>cd "{path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'</code>
        to synthesize the first one.
      </p>
    {:else}
      {#each byWeek(resp.reflections) as bucket (bucket.week)}
        <section class="week">
          <button
            class="week-head"
            type="button"
            aria-expanded={!collapsed[bucket.week]}
            onclick={() => toggle(bucket.week)}
          >
            <span class="caret" aria-hidden="true">{collapsed[bucket.week] ? '▸' : '▾'}</span>
            <span class="week-label">{bucket.week}</span>
            <span class="muted small">{bucket.rows.length} day{bucket.rows.length === 1 ? '' : 's'}</span>
          </button>
          {#if !collapsed[bucket.week]}
            <div class="day-list">
              {#each bucket.rows as r (r.id)}
                <article class="day-card">
                  <header class="day-head">
                    <h2>{r.title}</h2>
                    <span class="muted small">{relTime(r.ts)}</span>
                  </header>
                  <pre class="body">{r.body_md}</pre>
                  <p class="muted small footer">
                    {r.evidence_entry_ids.length} evidence · tier {r.tier} · {r.summary_source}
                  </p>
                </article>
              {/each}
            </div>
          {/if}
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.5rem; max-width: 1100px; margin: 0 auto; }
  .head h1 { margin: 0.25rem 0; }
  .muted { color: var(--text-muted, #888); }
  .small { font-size: 0.85em; }
  .error { color: var(--text-error, #c33); }
  .empty { text-align: center; padding: 2rem; }
  .path { font-family: monospace; word-break: break-all; }
  .meta { margin: 0.5rem 0; }
  .status.stale { color: #e6a878; }

  .week { margin: 1.25rem 0 0.5rem; }
  .week-head {
    display: flex; align-items: baseline; gap: 0.5rem;
    width: 100%;
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-radius: 6px;
    padding: 0.5rem 0.75rem;
    color: var(--text, #ddd);
    cursor: pointer;
    text-align: left;
    font-size: 0.95em;
  }
  .week-head:hover { background: var(--border, #2a2a2a); }
  .caret { width: 1em; }
  .week-label { font-weight: 600; }

  .day-list { display: flex; flex-direction: column; gap: 0.75rem; margin: 0.5rem 0 0; padding-left: 1rem; }

  .day-card {
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-left: 3px solid #4a9d5b;
    border-radius: 6px;
    padding: 0.75rem 1rem;
  }
  .day-head {
    display: flex; align-items: baseline; gap: 0.75rem; flex-wrap: wrap;
  }
  .day-head h2 { margin: 0; font-size: 1rem; }

  .body {
    white-space: pre-wrap; word-break: break-word;
    background: var(--surface-2, #0d0d0d);
    padding: 0.6rem 0.85rem; border-radius: 4px;
    font-size: 0.9em; margin: 0.5rem 0 0;
    line-height: 1.55;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  }
  .footer { margin: 0.35rem 0 0; }
</style>
