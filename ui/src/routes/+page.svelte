<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchCostSummary } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import { projectsStore, refreshProjects } from '$lib/projects.svelte.js';
  import { kfmt, relAgo } from '$lib/format.js';
  import BarColumns from '$lib/ui/BarColumns.svelte';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import Kbd from '$lib/ui/Kbd.svelte';

  // Activity series (output tokens per day) from /cost/summary?group=day.
  // We piggy-back on the cost-summary endpoint because it already buckets by
  // day and returns tokens_out; the dollar-cost field is intentionally ignored
  // — flat-subscription users don't care about provider compute prices.
  let tokensByDay = $state<number[]>([]);

  const projects = $derived(projectsStore.items);

  const recent = $derived(
    [...projects].sort((a, b) => a.lastMsAgo - b.lastMsAgo).slice(0, 6)
  );

  // Tick every 5s so 'active' status / 'last msg N seconds ago' stay live
  // without depending on a fresh server fetch.
  let tick = $state(Date.now());

  const activeProjs = $derived(
    projects
      .filter((p) => tick - p.lastMsAt < 60_000)
      .sort((a, b) => a.lastMsAt > b.lastMsAt ? -1 : 1)
  );

  const totals = $derived(
    projects.reduce(
      (acc, p) => ({
        sessions: acc.sessions + p.sessions,
        msgs: acc.msgs + p.msgs,
        tokensIn: acc.tokensIn + p.tokensIn,
        tokensOut: acc.tokensOut + p.tokensOut,
      }),
      { sessions: 0, msgs: 0, tokensIn: 0, tokensOut: 0 }
    )
  );

  const totalTokens = $derived(tokensByDay.reduce((a, b) => a + b, 0));

  async function loadActivityData(): Promise<void> {
    try {
      const since = Date.now() - 14 * 86_400_000;
      const resp = await fetchCostSummary({ group: 'day', since });
      tokensByDay = resp.buckets.map((b) => b.tokens_out);
    } catch {
      // chart is optional; silent ignore
    }
  }

  let unsubscribe: (() => void) | null = null;
  let tickHandle: ReturnType<typeof setInterval> | null = null;
  let refreshHandle: ReturnType<typeof setTimeout> | null = null;

  // Coalesce SSE bursts: a single live CLI session emits MsgNew at high
  // frequency. Refreshing on every event hammers /sessions and flickers the
  // list; debouncing to 800ms lands new sessions promptly without thrash.
  function scheduleRefresh(): void {
    if (refreshHandle !== null) return;
    refreshHandle = setTimeout(() => {
      refreshHandle = null;
      void refreshProjects();
    }, 800);
  }

  onMount(() => {
    void loadActivityData();
    if (projectsStore.items.length === 0) void refreshProjects();

    unsubscribe = subscribe({
      onMsgNew: () => scheduleRefresh(),
      onSessionUpdate: () => scheduleRefresh(),
    });

    tickHandle = setInterval(() => { tick = Date.now(); }, 5_000);
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
    if (tickHandle !== null) { clearInterval(tickHandle); tickHandle = null; }
    if (refreshHandle !== null) { clearTimeout(refreshHandle); refreshHandle = null; }
  });
</script>

<svelte:head>
  <title>klyne — Dashboard</title>
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
      <h2 class="ad-section-h" style="margin: 0;">Active now {#if activeProjs.length > 0}<span class="ad-faint" style="font-weight: 400;">· {activeProjs.length}</span>{/if}</h2>
      <span class="ad-mono ad-faint" style="font-size: 11px;">via SSE · live · idle &gt; 60s</span>
    </div>
    {#if activeProjs.length > 0}
      <div style="display: flex; flex-direction: column; gap: 8px;">
        {#each activeProjs as p}
          <div class="ad-card" style="padding: 16px; display: flex; align-items: center; gap: 16px;">
            <span class="ad-dot ad-dot--active" style="width: 8px; height: 8px;"></span>
            <div style="flex: 1; min-width: 0;">
              <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
                <button
                  style="font-weight: 600; font-size: 15px; color: var(--ad-fg);"
                  onclick={() => goto(`/projects/${encodeURIComponent(p.name)}`)}
                >{p.name}</button>
                {#each p.clis as c}
                  <CliBadge cli={c} />
                {/each}
                <span class="ad-badge ad-badge--ghost ad-mono" style="font-size: 11px;">{p.model}</span>
              </div>
              <div style="display: flex; gap: 16px; font-size: 12px; color: var(--ad-muted);">
                <span><span class="ad-mono ad-tnum">{p.msgs}</span> msgs</span>
                <span>↑ <span class="ad-mono ad-tnum">{kfmt(p.tokensIn)}</span></span>
                <span>↓ <span class="ad-mono ad-tnum">{kfmt(p.tokensOut)}</span></span>
                <span>last msg <span style="color: var(--ad-active);">{relAgo(p.lastMsAgo)}</span></span>
              </div>
            </div>
            <button
              class="ad-btn ad-btn--primary"
              onclick={() => goto(`/projects/${encodeURIComponent(p.name)}`)}
            >Open project →</button>
          </div>
        {/each}
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
            {#each p.clis as c}
              <CliBadge cli={c} />
            {/each}
          </div>
          <div style="display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 4px 12px; font-size: 12px;">
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
          </div>
          <div style="font-size: 11px; color: var(--ad-faint); margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--ad-border-soft);">
            {relAgo(p.lastMsAgo)} · <span class="ad-mono">{p.model}</span>
          </div>
        </button>
      {/each}
    </div>
  </section>

  <!-- Activity (last 14 days) -->
  <div class="ad-card" style="padding: 16px;">
    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
      <span class="ad-section-h">Activity · last 14 days</span>
      <span class="ad-mono ad-faint" style="font-size: 11px;">output tokens</span>
    </div>
    <div style="display: flex; align-items: baseline; gap: 24px; margin-bottom: 16px; flex-wrap: wrap;">
      <div>
        <div style="font-size: 28px; font-weight: 600; font-family: var(--ad-font-mono);">
          {kfmt(totalTokens || totals.tokensOut)}
        </div>
        <div class="ad-muted" style="font-size: 12px;">↓ tokens generated</div>
      </div>
      <div>
        <div style="font-size: 22px; font-weight: 600; font-family: var(--ad-font-mono);">{totals.msgs}</div>
        <div class="ad-muted" style="font-size: 12px;">messages</div>
      </div>
      <div>
        <div style="font-size: 22px; font-weight: 600; font-family: var(--ad-font-mono);">{totals.sessions}</div>
        <div class="ad-muted" style="font-size: 12px;">sessions</div>
      </div>
    </div>
    {#if tokensByDay.length > 0}
      <BarColumns data={tokensByDay} height={48} />
    {:else}
      <div style="height: 48px; background: var(--ad-bg-2); border-radius: 4px;"></div>
    {/if}
  </div>

  <!-- Keyboard hints -->
  <div style="display: flex; gap: 16px; margin-top: 24px; font-size: 11px; color: var(--ad-faint); flex-wrap: wrap;">
    <span><Kbd>/</Kbd> focus search</span>
    <span><Kbd>g</Kbd><Kbd>p</Kbd> projects</span>
    <span><Kbd>g</Kbd><Kbd>s</Kbd> settings</span>
    <span><Kbd>g</Kbd><Kbd>/</Kbd> search</span>
  </div>
</div>
