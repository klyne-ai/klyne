<!--
  ProjectRail — left rail of every project, with a live dot, filter
  input, and a Live / Idle split. Selecting a project drives the
  inspector panel on the right; the terminal grid is session-keyed
  and doesn't take input from this rail.
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { kfmt, relAgo } from '$lib/format.js';

  interface Props {
    projects: ProjectAggregate[];
    selectedPath: string;
    onSelect: (path: string) => void;
  }
  const { projects, selectedPath, onSelect }: Props = $props();

  let filter = $state('');

  const filtered = $derived(
    filter.trim()
      ? projects.filter((p) => p.name.toLowerCase().includes(filter.toLowerCase().trim()))
      : projects
  );

  const isLive = (p: ProjectAggregate) => p.lastMsAgo < 60_000;

  const live = $derived(filtered.filter(isLive));
  const rest = $derived(filtered.filter((p) => !isLive(p)));

  const liveCount = $derived(projects.filter(isLive).length);
</script>

<aside class="rail">
  <div class="rail-hd">
    <div class="row" style="justify-content: space-between;">
      <h3>Projects ({projects.length})</h3>
      <span class="mono faint" style="font-size: 10.5px;">{liveCount} live</span>
    </div>
    <input class="rail-filter" placeholder="Filter projects…" bind:value={filter} />
  </div>
  <div class="rail-list">
    {#if live.length > 0}
      <div class="rail-section-label">Live</div>
      {#each live as p (p.project_path)}
        <div class="proj" class:selected={selectedPath === p.project_path} onclick={() => onSelect(p.project_path)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') onSelect(p.project_path); }}>
          <span class="dot dot--live"></span>
          <div style="min-width: 0;">
            <div class="proj-name" title={p.project_path}>{p.name}</div>
            <div class="proj-meta">
              <span>{p.sessions} sess</span><span>·</span>
              <span>{kfmt(p.msgs)} msg</span><span>·</span>
              <span>{relAgo(p.lastMsAgo)}</span>
            </div>
          </div>
        </div>
      {/each}
    {/if}

    {#if rest.length > 0}
      <div class="rail-section-label">Idle</div>
      {#each rest as p (p.project_path)}
        <div class="proj" class:selected={selectedPath === p.project_path} onclick={() => onSelect(p.project_path)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') onSelect(p.project_path); }}>
          <span class="dot dot--idle"></span>
          <div style="min-width: 0;">
            <div class="proj-name" title={p.project_path}>{p.name}</div>
            <div class="proj-meta">
              <span>{p.sessions} sess</span><span>·</span>
              <span>{kfmt(p.msgs)} msg</span><span>·</span>
              <span>{relAgo(p.lastMsAgo)}</span>
            </div>
          </div>
        </div>
      {/each}
    {/if}

    {#if filtered.length === 0}
      <div style="padding: 24px 12px; color: var(--ad-faint); text-align: center; font-size: 12px;">
        No projects match.
      </div>
    {/if}
  </div>
</aside>
