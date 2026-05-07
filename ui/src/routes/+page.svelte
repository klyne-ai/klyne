<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchCostSummary } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import { projectsStore, refreshProjects } from '$lib/projects.svelte.js';
  import { kfmt, costFmt, relAgo } from '$lib/format.js';
  import Sparkline from '$lib/ui/Sparkline.svelte';
  import BarColumns from '$lib/ui/BarColumns.svelte';
  import StatusBadge from '$lib/ui/StatusBadge.svelte';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import Kbd from '$lib/ui/Kbd.svelte';

  // Cost series from /cost/summary?group=day
  let costByDay = $state<{ cost: number; tokensOut: number }[]>([]);

  const projects = $derived(projectsStore.items);

  const recent = $derived(
    [...projects].sort((a, b) => a.lastMsAgo - b.lastMsAgo).slice(0, 6)
  );

  const activeProj = $derived(
    projects.find((p) => p.status === 'active') ?? projects[0]
  );

  const totals = $derived(
    projects.reduce(
      (acc, p) => ({
        sessions: acc.sessions + p.sessions,
        msgs: acc.msgs + p.msgs,
        tokensOut: acc.tokensOut + p.tokensOut,
        cost: acc.cost + p.cost,
      }),
      { sessions: 0, msgs: 0, tokensOut: 0, cost: 0 }
    )
  );

  const costSeries = $derived(costByDay.map((d) => d.cost));
  const tokenSeries = $derived(costByDay.map((d) => d.tokensOut));
  const totalTokens = $derived(tokenSeries.reduce((a, b) => a + b, 0));

  // Load cost data
  async function loadCostData(): Promise<void> {
    try {
      const since = Date.now() - 14 * 86_400_000;
      const resp = await fetchCostSummary({ group: 'day', since });
      costByDay = resp.buckets.map((b) => ({ cost: b.cost_usd, tokensOut: b.tokens_out }));
    } catch {
      // silently ignore — cost chart is optional
    }
  }

  let unsubscribe: (() => void) | null = null;

  onMount(() => {
    void loadCostData();
    if (projectsStore.items.length === 0) void refreshProjects();

    unsubscribe = subscribe({
      onMsgNew: () => void refreshProjects(),
      onSessionUpdate: () => void refreshProjects(),
    });
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
  });
</script>

<svelte:head>
  <title>agentdeck — Dashboard</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1280px;">
  <!-- Header -->
  <div style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 4px;">
    <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0;">Dashboard</h1>
    <span class="ad-mono" style="font-size: 12px; color: var(--ad-faint);">
      {projects.length} projects · {totals.sessions} sessions · 35-day window
    </span>
  </div>
  <p class="ad-muted" style="margin-top: 4px; margin-bottom: 24px; font-size: 13px;">
    What you were working on, what's running now.
  </p>

  <!-- Active now -->
  <section style="margin-bottom: 24px;">
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px;">
      <h2 class="ad-section-h" style="margin: 0;">Active now</h2>
      <span class="ad-mono ad-faint" style="font-size: 11px;">via SSE · live</span>
    </div>
    {#if activeProj}
      <div class="ad-card" style="padding: 16px; display: flex; align-items: center; gap: 16px;">
        <span class="ad-dot ad-dot--active" style="width: 8px; height: 8px;"></span>
        <div style="flex: 1; min-width: 0;">
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
            <button
              style="font-weight: 600; font-size: 15px; color: var(--ad-fg);"
              onclick={() => goto(`/projects/${encodeURIComponent(activeProj.name)}`)}
            >{activeProj.name}</button>
            <CliBadge cli={activeProj.cli} />
            <span class="ad-badge ad-badge--ghost ad-mono" style="font-size: 11px;">{activeProj.model}</span>
          </div>
          <div style="display: flex; gap: 16px; font-size: 12px; color: var(--ad-muted);">
            <span><span class="ad-mono ad-tnum">{activeProj.msgs}</span> msgs</span>
            <span>↑ <span class="ad-mono ad-tnum">{kfmt(activeProj.tokensIn)}</span></span>
            <span>↓ <span class="ad-mono ad-tnum">{kfmt(activeProj.tokensOut)}</span></span>
            <span>last msg <span style="color: var(--ad-active);">{relAgo(activeProj.lastMsAgo)}</span></span>
          </div>
        </div>
        <button
          class="ad-btn ad-btn--primary"
          onclick={() => goto(`/projects/${encodeURIComponent(activeProj.name)}`)}
        >Open project →</button>
      </div>
    {:else if projectsStore.loading}
      <div class="ad-card" style="padding: 16px; color: var(--ad-muted); font-size: 13px;">Loading…</div>
    {:else}
      <div class="ad-card" style="padding: 16px; color: var(--ad-muted); font-size: 13px;">No active sessions</div>
    {/if}
  </section>

  <!-- Recent projects -->
  <section style="margin-bottom: 24px;">
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px;">
      <h2 class="ad-section-h" style="margin: 0;">Recent projects</h2>
      <button class="ad-btn ad-btn--ghost" onclick={() => goto('/projects')}>view all {projects.length} →</button>
    </div>
    <div style="display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px;">
      {#each recent as p}
        <button
          class="ad-card"
          onclick={() => goto(`/projects/${encodeURIComponent(p.name)}`)}
          style="padding: 14px; text-align: left; display: block; cursor: pointer; transition: background 80ms; width: 100%;"
          onmouseenter={(e) => (e.currentTarget.style.background = 'var(--ad-panel-hi)')}
          onmouseleave={(e) => (e.currentTarget.style.background = 'var(--ad-panel)')}
        >
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
            <span class="ad-dot ad-dot--{p.status}"></span>
            <span style="font-weight: 600; font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{p.name}</span>
            <CliBadge cli={p.cli} />
          </div>
          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 4px 12px; font-size: 12px;">
            <div>
              <div class="ad-mono ad-tnum" style="font-size: 14px; font-weight: 600; color: var(--ad-fg);">{p.sessions}</div>
              <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">sessions</div>
            </div>
            <div>
              <div class="ad-mono ad-tnum" style="font-size: 14px; font-weight: 600; color: var(--ad-fg);">{p.msgs}</div>
              <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">msgs</div>
            </div>
            <div>
              <div class="ad-mono ad-tnum" style="font-size: 14px; font-weight: 600; color: var(--ad-fg);">{kfmt(p.tokensOut)}</div>
              <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">↓ tokens</div>
            </div>
            <div>
              <div class="ad-mono ad-tnum" style="font-size: 14px; font-weight: 600; color: {p.cost > 0 ? 'var(--ad-active)' : 'var(--ad-fg)'};">{costFmt(p.cost, p.priced)}</div>
              <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">cost</div>
            </div>
          </div>
          <div style="font-size: 11px; color: var(--ad-faint); margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--ad-border-soft);">
            {relAgo(p.lastMsAgo)} · <span class="ad-mono">{p.model}</span>
          </div>
        </button>
      {/each}
    </div>
  </section>

  <!-- Cost + activity charts -->
  <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
    <div class="ad-card" style="padding: 16px;">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <span class="ad-section-h">Cost · last 14 days</span>
        <button class="ad-btn ad-btn--ghost ad-btn--sm">drill in →</button>
      </div>
      <div style="font-size: 28px; font-weight: 600; font-family: var(--ad-font-mono); margin-bottom: 2px;">
        ${totals.cost.toFixed(2)}
      </div>
      <div class="ad-muted" style="font-size: 12px; margin-bottom: 16px;">
        across all priced models
      </div>
      {#if costSeries.length > 0}
        <Sparkline data={costSeries} height={48} />
      {:else}
        <div style="height: 48px; background: var(--ad-bg-2); border-radius: 4px;"></div>
      {/if}
    </div>

    <div class="ad-card" style="padding: 16px;">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <span class="ad-section-h">Activity · output tokens</span>
        <span class="ad-mono ad-faint" style="font-size: 11px;">14d</span>
      </div>
      <div style="font-size: 28px; font-weight: 600; font-family: var(--ad-font-mono); margin-bottom: 2px;">
        {kfmt(totalTokens || totals.tokensOut)}
      </div>
      <div class="ad-muted" style="font-size: 12px; margin-bottom: 16px;">
        {totals.msgs} messages
      </div>
      {#if tokenSeries.length > 0}
        <BarColumns data={tokenSeries} height={48} />
      {:else}
        <div style="height: 48px; background: var(--ad-bg-2); border-radius: 4px;"></div>
      {/if}
    </div>
  </div>

  <!-- Keyboard hints -->
  <div style="display: flex; gap: 16px; margin-top: 24px; font-size: 11px; color: var(--ad-faint); flex-wrap: wrap;">
    <span><Kbd>/</Kbd> focus search</span>
    <span><Kbd>g</Kbd><Kbd>p</Kbd> projects</span>
    <span><Kbd>g</Kbd><Kbd>s</Kbd> settings</span>
    <span><Kbd>g</Kbd><Kbd>/</Kbd> search</span>
  </div>
</div>
