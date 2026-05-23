<!--
  ProjectsList — left-column list of all projects for the Projects master/detail view.
  Props: sorted filtered list from parent + which project is selected.
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { relAgo, kfmt } from '$lib/format.js';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';

  interface Props {
    projects: ProjectAggregate[];
    selectedName: string | null;
    sort: string;
  }
  const { projects, selectedName, sort }: Props = $props();

  function dotClass(p: ProjectAggregate): string {
    if (p.status === 'active') return 'ad-dot ad-dot--active';
    if (p.status === 'compacted') return 'ad-dot ad-dot--compacted';
    return 'ad-dot ad-dot--idle';
  }

  function navigateTo(p: ProjectAggregate): void {
    const search = $page.url.search;
    void goto(`/projects/${encodeURIComponent(p.name)}${search}`);
  }
</script>

<section
  class="ad-card"
  style="overflow: hidden; display: flex; flex-direction: column; max-height: 75vh;"
>
  <div
    style="
      padding: 10px 14px;
      border-bottom: 1px solid var(--ad-border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      flex-shrink: 0;
    "
  >
    <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">
      {projects.length} project{projects.length === 1 ? '' : 's'}
    </span>
    <span class="ad-mono" style="font-size: 10px; color: var(--ad-faint);">sort: {sort}</span>
  </div>

  <div style="overflow-y: auto; flex: 1;">
    {#each projects as p (p.project_path)}
      {@const isSelected = p.name === selectedName}
      <button
        type="button"
        onclick={() => navigateTo(p)}
        style="
          width: 100%;
          text-align: left;
          padding: 12px 14px;
          border: none;
          border-bottom: 1px solid var(--ad-border);
          border-left: 2px solid {isSelected ? 'var(--ad-accent)' : 'transparent'};
          background: {isSelected ? 'color-mix(in oklch, var(--ad-accent) 7%, var(--ad-panel))' : 'transparent'};
          cursor: pointer;
          display: flex;
          flex-direction: column;
          gap: 6px;
        "
      >
        <!-- Row 1: dot + name + last-ago -->
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px;">
          <div style="display: flex; align-items: center; gap: 8px; min-width: 0; flex: 1;">
            <span class={dotClass(p)} style="flex-shrink: 0;"></span>
            <span
              style="
                color: var(--ad-fg);
                font-weight: 500;
                font-size: 13px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                min-width: 0;
              "
            >{p.name}</span>
          </div>
          <span class="ad-mono" style="font-size: 10.5px; color: var(--ad-faint); flex-shrink: 0;">
            {relAgo(p.lastMsAgo)}
          </span>
        </div>

        <!-- Row 2: CLI pills + sessions/tokens -->
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px;">
          <div style="display: flex; gap: 4px; flex-wrap: wrap;">
            {#each p.clis as c}
              <span
                class="ad-pill {c === 'claude' ? 'ad-pill--claude' : 'ad-pill--codex'}"
                style="font-size: 10px; padding: 1px 6px;"
              >{c}</span>
            {/each}
          </div>
          <span class="ad-mono" style="font-size: 10.5px; color: var(--ad-faint); flex-shrink: 0;">
            {p.sessions} sessions · {kfmt(p.tokensIn)}
          </span>
        </div>
      </button>
    {/each}

    {#if projects.length === 0}
      <div style="padding: 24px; text-align: center; color: var(--ad-faint); font-size: 13px;">
        No projects match this filter.
      </div>
    {/if}
  </div>
</section>
