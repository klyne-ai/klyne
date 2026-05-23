<!--
  TabWorklog — Worklog tab for a project.
  Fetches /worklog/items/project for the selected project and renders reflections.
  Empty state shows the /klyne:reflect command. "See full →" links to /worklog/project.
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import type { WorklogProjectResponse, Reflection } from '$lib/types.js';
  import { fetchWorklogProject } from '$lib/api.js';
  import { relAgo } from '$lib/format.js';
  import { onMount } from 'svelte';

  interface Props {
    project: ProjectAggregate;
  }
  const { project }: Props = $props();

  let resp = $state<WorklogProjectResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let copied = $state(false);

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      resp = await fetchWorklogProject(project.project_path);
    } catch (e) {
      error = e instanceof Error ? e.message : 'failed to load worklog';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  // Re-fetch when project changes
  $effect(() => {
    void project.project_path;
    void load();
  });

  function reflectCmd(): string {
    return `cd "${project.project_path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'`;
  }

  async function copyCmd(): Promise<void> {
    try {
      await navigator.clipboard.writeText(reflectCmd());
      copied = true;
      setTimeout(() => { copied = false; }, 2000);
    } catch {
      // clipboard may be denied; cmd is visible
    }
  }

  function reflectionAge(r: Reflection): string {
    return `reflected ${relAgo(Date.now() - r.ts)}`;
  }
</script>

<div style="display: flex; flex-direction: column; gap: 14px;">
  {#if loading && !resp}
    <div style="padding: 20px; text-align: center; color: var(--ad-faint); font-size: 13px;">Loading…</div>
  {:else if error}
    <div style="padding: 20px; color: var(--ad-error, #c33); font-size: 13px;">⚠ {error}</div>
  {:else if resp}
    {@const rollup = resp.project}
    {@const reflections = resp.reflections}

    <!-- Stale banner -->
    {#if rollup.stale && rollup.latest_reflection}
      <div
        style="
          padding: 10px 14px;
          background: color-mix(in oklch, var(--ad-warn, #d97a4a) 10%, var(--ad-panel));
          border: 1px solid color-mix(in oklch, var(--ad-warn, #d97a4a) 30%, transparent);
          border-radius: 8px;
          display: flex;
          align-items: center;
          justify-content: space-between;
          gap: 10px;
        "
      >
        <div style="display: flex; align-items: center; gap: 10px;">
          <span class="ad-dot ad-dot--idle" style="flex-shrink: 0;"></span>
          <span style="font-size: 12.5px; color: var(--ad-fg);">
            <strong>{rollup.pending_entries} new {rollup.pending_entries === 1 ? 'entry' : 'entries'}</strong>
            since last reflection — run to refresh:
          </span>
        </div>
        <button
          type="button"
          onclick={copyCmd}
          style="
            padding: 4px 10px;
            background: var(--ad-bg-2);
            border: 1px solid var(--ad-border);
            border-radius: 6px;
            color: var(--ad-fg);
            font-size: 11px;
            cursor: pointer;
            white-space: nowrap;
            flex-shrink: 0;
          "
        >
          {copied ? '✓ copied' : 'copy command'}
        </button>
      </div>
    {/if}

    <!-- Cold / no reflections -->
    {#if reflections.length === 0}
      <div style="display: flex; flex-direction: column; gap: 12px;">
        <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">No reflections yet for this project</div>
        <p style="margin: 0; font-size: 13px; color: var(--ad-faint);">
          Reflections are synthesized inside a CLI session — never by the klyne daemon.
          Run <span class="ad-mono">/klyne:reflect</span> in this project to capture today.
        </p>
        <div
          style="
            background: var(--ad-bg-2);
            border: 1px solid var(--ad-border);
            border-radius: 8px;
            padding: 10px 14px;
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 10px;
          "
        >
          <code class="ad-mono" style="font-size: 11.5px; color: var(--ad-fg-2, var(--ad-faint)); word-break: break-all; flex: 1;">
            {reflectCmd()}
          </code>
          <button
            type="button"
            onclick={copyCmd}
            style="
              padding: 4px 10px;
              background: var(--ad-panel);
              border: 1px solid var(--ad-border);
              border-radius: 6px;
              color: var(--ad-fg);
              font-size: 11px;
              cursor: pointer;
              white-space: nowrap;
              flex-shrink: 0;
            "
          >
            {copied ? '✓ copied' : 'copy'}
          </button>
        </div>
      </div>
    {:else}
      <!-- Reflection cards -->
      {#each reflections as r (r.id)}
        <div
          style="
            background: var(--ad-panel);
            border: 1px solid var(--ad-border);
            border-radius: 8px;
            padding: 16px 18px;
          "
        >
          <div style="display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 10px;">
            <h3 style="margin: 0; font-size: 14px; color: var(--ad-fg); font-weight: 500;">
              {r.title || 'Daily reflection'}
            </h3>
            <span class="ad-mono" style="font-size: 10.5px; color: var(--ad-faint); flex-shrink: 0; margin-left: 8px;">
              {reflectionAge(r)} · {r.evidence_entry_ids.length} evidence
            </span>
          </div>
          <div
            style="
              white-space: pre-wrap;
              word-break: break-word;
              color: var(--ad-faint);
              font-size: 12.5px;
              line-height: 1.6;
            "
          >{r.body_md}</div>
        </div>
      {/each}
    {/if}

    <!-- "See full worklog →" always visible (ISO-week drill-in handles empty states) -->
    <div style="padding: 8px 0; text-align: right;">
      <a
        class="see-full mono"
        href={`/worklog/project?path=${encodeURIComponent(project.project_path)}`}
        style="font-size: 12px; color: var(--ad-faint); text-decoration: none;"
      >See full worklog →</a>
    </div>
  {/if}
</div>
