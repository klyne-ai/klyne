<!--
  ProjectPanel — right-column detail panel for a selected project.
  Header + 4 tabs: Overview · Sessions · Worklog · Files.
  Tab reads/updates ?tab= query param via tabUrl (no full reload).
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import type { ProjectInsight, Session } from '$lib/types.js';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { tabUrl } from '$lib/dashboard/url-state.js';
  import TabOverview from './TabOverview.svelte';
  import TabSessions from './TabSessions.svelte';
  import TabWorklog from './TabWorklog.svelte';
  import TabFiles from './TabFiles.svelte';

  interface Props {
    project: ProjectAggregate;
    insight: ProjectInsight | null;
    sessions: Session[];
  }
  const { project, insight, sessions }: Props = $props();

  type Tab = 'overview' | 'sessions' | 'worklog' | 'files';
  interface TabDef { id: Tab; label: string; count?: number | null }
  const tabs: TabDef[] = $derived.by(() => [
    { id: 'overview' as Tab, label: 'Overview' },
    { id: 'sessions' as Tab, label: 'Sessions', count: project.sessions },
    { id: 'worklog' as Tab, label: 'Worklog' },
    { id: 'files' as Tab, label: 'Files' },
  ]);

  const activeTab = $derived.by((): Tab => {
    const t = $page.url.searchParams.get('tab');
    if (t === 'sessions' || t === 'worklog' || t === 'files') return t;
    return 'overview';
  });

  function switchTab(id: Tab): void {
    void goto(tabUrl($page.url.pathname + $page.url.search, id), { replaceState: true });
  }

  function dotClass(p: ProjectAggregate): string {
    if (p.status === 'active') return 'ad-dot ad-dot--active';
    if (p.status === 'compacted') return 'ad-dot ad-dot--compacted';
    return 'ad-dot ad-dot--idle';
  }
</script>

<section class="ad-card" style="overflow: hidden;">
  <!-- Header -->
  <div style="padding: 16px 18px; border-bottom: 1px solid var(--ad-border);">
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px;">
      <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
        <span class={dotClass(project)} style="flex-shrink: 0;"></span>
        <h2 style="margin: 0; font-size: 18px; color: var(--ad-fg); font-weight: 500; letter-spacing: -0.01em; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
          {project.name}
        </h2>
        {#each project.clis as c}
          <span
            class="ad-pill {c === 'claude' ? 'ad-pill--claude' : 'ad-pill--codex'}"
            style="flex-shrink: 0;"
          >{c}</span>
        {/each}
      </div>
    </div>
    <div class="ad-mono" style="font-size: 11px; color: var(--ad-faint); word-break: break-all;">
      {project.project_path}
    </div>
  </div>

  <!-- Tab bar -->
  <div
    style="
      padding: 0 18px;
      border-bottom: 1px solid var(--ad-border);
      display: flex;
      gap: 0;
    "
  >
    {#each tabs as t}
      {@const count = t.count}
      <button
        type="button"
        onclick={() => switchTab(t.id)}
        style="
          padding: 10px 14px;
          border: none;
          border-bottom: 2px solid {activeTab === t.id ? 'var(--ad-accent)' : 'transparent'};
          background: none;
          color: {activeTab === t.id ? 'var(--ad-fg)' : 'var(--ad-faint)'};
          font-size: 13px;
          font-weight: {activeTab === t.id ? '500' : '400'};
          cursor: pointer;
          display: flex;
          align-items: center;
          gap: 6px;
          margin-bottom: -1px;
        "
      >
        {t.label}
        {#if count != null}
          <span
            style="
              background: var(--ad-bg-2);
              border: 1px solid var(--ad-border);
              border-radius: 10px;
              padding: 1px 6px;
              font-size: 10px;
              color: var(--ad-faint);
            "
          >{count}</span>
        {/if}
      </button>
    {/each}
  </div>

  <!-- Tab content -->
  <div style="padding: 16px 18px 20px; overflow-y: auto; max-height: calc(75vh - 130px);">
    {#if activeTab === 'overview'}
      <TabOverview {project} {insight} {sessions} />
    {:else if activeTab === 'sessions'}
      <TabSessions sessions={sessions} />
    {:else if activeTab === 'worklog'}
      <TabWorklog {project} />
    {:else if activeTab === 'files'}
      <TabFiles {insight} />
    {/if}
  </div>
</section>
