<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { projectsStore } from '$lib/projects.svelte.js';

  let filter = $state('');
  let collapsed = $state<Record<string, boolean>>({});

  const projects = $derived(projectsStore.items);
  const filtered = $derived(
    filter
      ? projects.filter((p) => p.name.toLowerCase().includes(filter.toLowerCase()))
      : projects
  );

  function currentProjectPath(): string {
    const pathname = $page.url.pathname;
    const m = pathname.match(/^\/projects\/(.+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  const activeProject = $derived(currentProjectPath());

  function totalSessions(): number {
    return projects.reduce((s, p) => s + p.sessions, 0);
  }
</script>

<aside style="width: var(--ad-side-w); border-right: 1px solid var(--ad-border); background: var(--ad-bg-2); display: flex; flex-direction: column; overflow: hidden;">
  <div style="padding: 10px 10px 8px;">
    <input
      class="ad-input"
      placeholder="Filter projects…"
      bind:value={filter}
      style="height: 28px; font-size: 12px;"
    />
  </div>

  <div class="ad-section-h" style="padding: 6px 14px; display: flex; justify-content: space-between;">
    <span>Projects</span><span>{filtered.length}</span>
  </div>

  <div style="overflow-y: auto; flex: 1; padding: 0 6px 12px;">
    {#each filtered as p}
      {@const isActive = activeProject === p.name}
      {@const isOpen = !collapsed[p.name]}
      <div style="margin-bottom: 1px;">
        <button
          style="width: 100%; text-align: left; padding: 6px 8px; border-radius: 4px; background: {isActive ? 'var(--ad-panel-hi)' : 'transparent'}; display: flex; align-items: center; gap: 6px; color: {isActive ? 'var(--ad-fg)' : 'var(--ad-fg-2)'};"
          onclick={() => {
            goto(`/projects/${encodeURIComponent(p.name)}`);
            collapsed = { ...collapsed, [p.name]: !collapsed[p.name] };
          }}
        >
          <span style="font-family: var(--ad-font-mono); font-size: 9px; color: var(--ad-faint); width: 8px; display: inline-block;">{isOpen ? '▾' : '▸'}</span>
          <span class="ad-dot ad-dot--{p.status}" style="flex-shrink: 0;"></span>
          <span class="ad-truncate ad-grow" style="font-weight: 500; font-size: 13px;">{p.name}</span>
          <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint);">{p.sessions}</span>
        </button>
        {#if isOpen && p.sessions > 0}
          <div style="padding-left: 22px; padding-top: 2px; padding-bottom: 4px;">
            <div style="padding: 3px 8px; font-size: 11px; color: var(--ad-muted); font-family: var(--ad-font-mono);">
              {p.sessions} session{p.sessions !== 1 ? 's' : ''}
            </div>
          </div>
        {/if}
      </div>
    {/each}

    {#if projectsStore.loading}
      <div style="padding: 12px; font-size: 11px; color: var(--ad-faint); text-align: center;">Loading…</div>
    {/if}
  </div>

  <div style="padding: 8px 12px; border-top: 1px solid var(--ad-border); font-size: 11px; color: var(--ad-faint); display: flex; justify-content: space-between;">
    <span>{totalSessions()} sessions</span>
    <span class="ad-mono">v1.1</span>
  </div>
</aside>
