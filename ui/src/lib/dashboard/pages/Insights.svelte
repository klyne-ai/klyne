<script lang="ts">
  /**
   * Insights — merged /insights + /stats analytics surface.
   *
   * Five tabs: Overview · Activity · Models · Daily · Projects
   * Driven by ?tab=, ?win=, ?cli= URL params.
   *
   * Window selector: primary buttons 1h · 3h · 6h · 1d; dropdown for 7d / 30d / 90d.
   * Six always-visible KPIs: Spend · Input · Output · Sessions · Messages · Projects.
   */
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { kfmt, costFmt } from '$lib/format.js';
  import { fetchUsageStats, fetchProjectInsights } from '$lib/api.js';
  import { tabUrl } from '$lib/dashboard/url-state.js';
  import type { CLI, UsageStatsResponse, ProjectInsightsResponse } from '$lib/types.js';

  import TabOverview from './insights/TabOverview.svelte';
  import TabActivity from './insights/TabActivity.svelte';
  import TabModels from './insights/TabModels.svelte';
  import TabDaily from './insights/TabDaily.svelte';
  import TabProjects from './insights/TabProjects.svelte';

  type TabId = 'overview' | 'activity' | 'models' | 'daily' | 'projects';
  type WinId = '1h' | '3h' | '6h' | '1d' | '7d' | '30d' | '90d';

  const TABS: { id: TabId; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'activity', label: 'Activity' },
    { id: 'models', label: 'Models' },
    { id: 'daily', label: 'Daily' },
    { id: 'projects', label: 'Projects' },
  ];

  const PRIMARY_WINS: WinId[] = ['1h', '3h', '6h', '1d'];
  const DROPDOWN_WINS: { value: WinId; label: string }[] = [
    { value: '7d', label: '7 days' },
    { value: '30d', label: '30 days' },
    { value: '90d', label: '90 days' },
  ];

  // Win → lookback days for /usage/stats
  const WIN_DAYS: Record<WinId, number> = {
    '1h': 1,
    '3h': 1,
    '6h': 1,
    '1d': 1,
    '7d': 7,
    '30d': 30,
    '90d': 90,
  };

  // Win → milliseconds for project insights since param
  const WIN_MS: Record<WinId, number> = {
    '1h':  1 * 3_600_000,
    '3h':  3 * 3_600_000,
    '6h':  6 * 3_600_000,
    '1d':  1 * 86_400_000,
    '7d':  7 * 86_400_000,
    '30d': 30 * 86_400_000,
    '90d': 90 * 86_400_000,
  };

  // Derive tab from URL
  const activeTab = $derived.by<TabId>(() => {
    const t = $page.url.searchParams.get('tab');
    if (t && TABS.some((x) => x.id === t)) return t as TabId;
    return 'overview';
  });

  // Derive window from URL
  const activeWin = $derived.by<WinId>(() => {
    const w = $page.url.searchParams.get('win');
    if (w && (PRIMARY_WINS.includes(w as WinId) || DROPDOWN_WINS.some((d) => d.value === w))) {
      return w as WinId;
    }
    return '1d';
  });

  // Derive CLI filter from URL
  const cliFilter = $derived.by<CLI | ''>(() => {
    const c = $page.url.searchParams.get('cli');
    if (c === 'claude' || c === 'codex') return c;
    return '';
  });

  // Data state
  let statsData = $state<UsageStatsResponse | null>(null);
  let projectsData = $state<ProjectInsightsResponse | null>(null);
  let loadingStats = $state(true);
  let loadingProjects = $state(true);
  let statsError = $state<string | null>(null);
  let projectsError = $state<string | null>(null);

  async function loadStats(win: WinId, cli: CLI | ''): Promise<void> {
    loadingStats = true;
    statsError = null;
    try {
      statsData = await fetchUsageStats({
        cli: cli || undefined,
        days: WIN_DAYS[win],
        heatmap_weeks: 14,
      });
    } catch (e: unknown) {
      statsError = e instanceof Error ? e.message : 'failed to load stats';
      statsData = null;
    } finally {
      loadingStats = false;
    }
  }

  async function loadProjects(win: WinId): Promise<void> {
    loadingProjects = true;
    projectsError = null;
    try {
      const now = Date.now();
      projectsData = await fetchProjectInsights({
        since: now - WIN_MS[win],
        until: now,
      });
    } catch (e: unknown) {
      projectsError = e instanceof Error ? e.message : 'failed to load project insights';
      projectsData = null;
    } finally {
      loadingProjects = false;
    }
  }

  // Reload when win or cli changes
  $effect(() => {
    void loadStats(activeWin, cliFilter);
    void loadProjects(activeWin);
  });

  // Navigate helpers
  function setTab(id: TabId): void {
    void goto(tabUrl($page.url.pathname + $page.url.search, id), { replaceState: true });
  }

  function setWin(w: WinId): void {
    const u = $page.url;
    const sp = new URLSearchParams(u.search);
    sp.set('win', w);
    void goto(`${u.pathname}?${sp.toString()}`, { replaceState: true });
  }

  function setCli(c: string): void {
    const u = $page.url;
    const sp = new URLSearchParams(u.search);
    if (c && c !== 'both') sp.set('cli', c);
    else sp.delete('cli');
    void goto(`${u.pathname}?${sp.toString()}`, { replaceState: true });
  }

  // KPI derivations from stats + projects
  const totalCost = $derived(statsData?.total_cost_usd ?? 0);
  const totalInput = $derived(statsData?.total_input ?? 0);
  const totalOutput = $derived(statsData?.total_output ?? 0);
  const totalSessions = $derived(statsData?.total_sessions ?? 0);
  const totalMessages = $derived(statsData?.total_messages ?? 0);
  const totalCacheRead = $derived(statsData?.total_cache_read ?? 0);
  const cacheReusePct = $derived(
    totalInput > 0 ? ((totalCacheRead / totalInput) * 100).toFixed(0) + '% cached' : ''
  );
  const projectCount = $derived(projectsData?.projects.length ?? 0);

  const loading = $derived(loadingStats || loadingProjects);

  // Is the dropdown win active (not a primary win)?
  const isDropdownWin = $derived(DROPDOWN_WINS.some((d) => d.value === activeWin));
</script>

<svelte:head>
  <title>klyne · Insights</title>
</svelte:head>

<div style="padding: 20px 24px; max-width: 1200px; margin: 0 auto;">
  <!-- Header -->
  <div style="display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; gap: 16px; flex-wrap: wrap;">
    <div>
      <h1 style="font-size: 18px; font-weight: 600; margin: 0;">Insights</h1>
      <p style="font-size: 12px; color: var(--ad-muted); margin: 4px 0 0; max-width: 560px;">
        Cross-session token economics, model usage and activity rhythm — all derived locally from your JSONL via the cost engine.
      </p>
    </div>

    <!-- Controls -->
    <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
      <!-- CLI filter -->
      <label style="display: flex; align-items: center; gap: 6px; font-size: 12px;">
        <span style="color: var(--ad-muted);">cli</span>
        <select
          class="ad-input"
          style="height: 28px; font-size: 12px;"
          value={cliFilter || 'both'}
          onchange={(e) => setCli((e.target as HTMLSelectElement).value)}
        >
          <option value="both">both</option>
          <option value="claude">claude</option>
          <option value="codex">codex</option>
        </select>
      </label>

      <!-- Primary window buttons -->
      <div style="display: flex; gap: 2px;">
        {#each PRIMARY_WINS as w (w)}
          <button
            class="ad-btn"
            style="padding: 4px 10px; font-size: 12px; border-radius: 6px; background: {activeWin === w ? 'var(--ad-claude)' : 'var(--ad-bg-2)'}; color: {activeWin === w ? '#fff' : 'var(--ad-fg-2)'}; border: 1px solid {activeWin === w ? 'var(--ad-claude)' : 'var(--ad-border-soft)'};"
            onclick={() => setWin(w)}
          >{w}</button>
        {/each}
      </div>

      <!-- Dropdown for longer windows -->
      <select
        class="ad-input"
        style="height: 28px; font-size: 12px;"
        value={isDropdownWin ? activeWin : 'more'}
        onchange={(e) => { const v = (e.target as HTMLSelectElement).value; if (v !== 'more') setWin(v as WinId); }}
      >
        <option value="more" disabled>more…</option>
        {#each DROPDOWN_WINS as d (d.value)}
          <option value={d.value}>{d.label}</option>
        {/each}
      </select>
    </div>
  </div>

  <!-- KPI strip — always visible -->
  <div class="ad-card" style="display: grid; grid-template-columns: repeat(6, 1fr); padding: 0; margin-bottom: 16px;">
    <div style="padding: 12px 14px; border-right: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h" style="margin-bottom: 4px;">Spend</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{costFmt(totalCost, totalCost > 0)}</div>
      <div class="mono" style="font-size: 10px; color: var(--ad-faint); margin-top: 3px;">subscription</div>
    </div>
    <div style="padding: 12px 14px; border-right: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h" style="margin-bottom: 4px;">↑ Input</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{kfmt(totalInput)}</div>
      {#if cacheReusePct}
        <div class="mono" style="font-size: 10px; color: var(--ad-faint); margin-top: 3px;">{cacheReusePct}</div>
      {/if}
    </div>
    <div style="padding: 12px 14px; border-right: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h" style="margin-bottom: 4px;">↓ Output</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{kfmt(totalOutput)}</div>
    </div>
    <div style="padding: 12px 14px; border-right: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h" style="margin-bottom: 4px;">Sessions</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{totalSessions}</div>
    </div>
    <div style="padding: 12px 14px; border-right: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h" style="margin-bottom: 4px;">Messages</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{kfmt(totalMessages)}</div>
    </div>
    <div style="padding: 12px 14px;">
      <div class="ad-section-h" style="margin-bottom: 4px;">Projects</div>
      <div class="mono ad-tnum" style="font-size: 17px; font-weight: 600; color: var(--ad-fg);">{projectCount}</div>
    </div>
  </div>

  <!-- Tab bar -->
  <div style="display: flex; gap: 4px; border-bottom: 1px solid var(--ad-border); margin-bottom: 16px;">
    {#each TABS as t (t.id)}
      {@const isActive = activeTab === t.id}
      <a
        href={tabUrl($page.url.pathname + $page.url.search, t.id)}
        style="border-radius: 0; border: none; border-bottom: 2px solid {isActive ? 'var(--ad-claude)' : 'transparent'}; padding: 8px 14px; font-size: 13px; color: {isActive ? 'var(--ad-fg)' : 'var(--ad-fg-2)'}; background: transparent; text-decoration: none; cursor: pointer;"
        onclick={(e) => { e.preventDefault(); setTab(t.id); }}
      >
        {t.label}
      </a>
    {/each}
  </div>

  <!-- Tab content -->
  {#if loading && !statsData && !projectsData}
    <div class="ad-card" style="padding: 32px; text-align: center; color: var(--ad-muted);">Loading…</div>
  {:else if statsError && projectsError}
    <div class="ad-card" style="padding: 24px; color: var(--ad-danger, #f87171);">
      {statsError}
    </div>
  {:else}
    {#if activeTab === 'overview'}
      <TabOverview stats={statsData} projects={projectsData} />
    {:else if activeTab === 'activity'}
      <TabActivity stats={statsData} />
    {:else if activeTab === 'models'}
      <TabModels stats={statsData} />
    {:else if activeTab === 'daily'}
      <TabDaily stats={statsData} />
    {:else if activeTab === 'projects'}
      <TabProjects projects={projectsData} />
    {/if}
  {/if}
</div>
