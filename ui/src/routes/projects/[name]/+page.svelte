<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { fetchSessions } from '$lib/api.js';
  import { projectsStore, refreshProjects } from '$lib/projects.svelte.js';
  import { kfmt, costFmt, relAgo, dayLabel } from '$lib/format.js';
  import type { Session } from '$lib/types.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import StatusBadge from '$lib/ui/StatusBadge.svelte';

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const projectName = $derived(decodeURIComponent(($page.params as any)['name'] ?? ''));

  const project = $derived(
    projectsStore.items.find((p) => p.name === projectName) ?? null
  );

  let sessions = $state<Session[]>([]);
  let loadError = $state<string | null>(null);

  // Group sessions by day
  const grouped = $derived.by(() => {
    const g = new Map<string, Session[]>();
    for (const s of sessions) {
      const label = dayLabel(s.last_msg_at);
      const existing = g.get(label) ?? [];
      existing.push(s);
      g.set(label, existing);
    }
    return g;
  });

  async function loadSessions(): Promise<void> {
    loadError = null;
    try {
      const resp = await fetchSessions({ limit: 200 });
      // Filter by project path matching the project name
      sessions = resp.sessions.filter(
        (s) => s.project_path.endsWith('/' + projectName) || s.project_path === projectName
      );
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load sessions';
    }
  }

  onMount(() => {
    if (projectsStore.items.length === 0) void refreshProjects();
    void loadSessions();
  });
</script>

<svelte:head>
  <title>agentdeck — {projectName}</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1280px;">
  <button onclick={() => goto('/projects')} class="ad-btn ad-btn--ghost" style="margin-bottom: 12px; padding-left: 4px;">
    ‹ projects
  </button>

  {#if project}
    <div style="display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px;">
      <div>
        <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 6px;">
          <span class="ad-dot ad-dot--{project.status}"></span>
          <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0;">{project.name}</h1>
          <CliBadge cli={project.cli} />
        </div>
        <div class="ad-mono ad-muted" style="font-size: 12px;">{project.project_path}</div>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="ad-btn ad-btn--ghost">Rename label</button>
        <button class="ad-btn">Open in CLI</button>
      </div>
    </div>

    <!-- Stats strip -->
    <div class="ad-card" style="display: grid; grid-template-columns: repeat(5, 1fr); padding: 0; margin-bottom: 20px;">
      {#each [
        ['Sessions', String(project.sessions)],
        ['Messages', String(project.msgs)],
        ['↑ tokens in', kfmt(project.tokensIn)],
        ['↓ tokens out', kfmt(project.tokensOut)],
        ['Cost', costFmt(project.cost, project.priced)],
      ] as [label, value], i}
        <div style="padding: 14px 16px; border-right: {i < 4 ? '1px solid var(--ad-border-soft)' : 'none'};">
          <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">{label}</div>
          <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{value}</div>
        </div>
      {/each}
    </div>
  {:else}
    <div style="margin-bottom: 20px; color: var(--ad-muted);">Loading project…</div>
  {/if}

  <!-- Sessions grouped by day -->
  {#if loadError}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error);">{loadError}</div>
  {:else if sessions.length === 0}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">No sessions found for this project.</div>
  {:else}
    {#each [...grouped.entries()] as [day, list]}
      <div style="margin-bottom: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px; padding: 0 4px;">{day}</div>
        <div class="ad-card" style="overflow: hidden;">
          {#each list as s, i}
            <div
              onclick={() => goto(`/sessions/${encodeURIComponent(s.id)}`)}
              style="display: grid; grid-template-columns: 1fr 80px 90px 70px 110px 20px; align-items: center; gap: 12px; padding: 10px 14px; border-bottom: {i < list.length - 1 ? '1px solid var(--ad-border-soft)' : 'none'}; font-size: 13px; cursor: pointer;"
              onmouseenter={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel-hi)')}
              onmouseleave={(e) => ((e.currentTarget as HTMLElement).style.background = 'transparent')}
            >
              <span class="ad-mono ad-truncate" style="color: var(--ad-fg-2);">{s.id}</span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: var(--ad-fg-2);">{s.msg_count} msgs</span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: var(--ad-muted);">↓ {kfmt(s.tokens_out)}</span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: {s.cost_usd > 0 ? 'var(--ad-active)' : 'var(--ad-faint)'};">{costFmt(s.cost_usd, s.cost_usd > 0)}</span>
              <span><StatusBadge status={s.status} /></span>
              <span style="color: var(--ad-faint);">›</span>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>
