<script lang="ts">
  /**
   * /stats — tokscale-inspired cross-session usage dashboard.
   *
   * Four tabs:
   *   - Overview : tokens-per-day bar chart + Models-by-cost breakdown
   *   - Models   : per-model table (Source, Model, Input, Output, C.Read, C.Write, Total, Cost)
   *   - Daily    : per-day rows with all token + cost columns
   *   - Stats    : GitHub-style activity heatmap + streaks + favorite model + peak hour
   *
   * Data lives at GET /usage/stats. Filters: cli (claude|codex|both),
   * days lookback, heatmap window. Read-only. No AI calls.
   */
  import { onMount } from 'svelte';
  import { fetchUsageStats } from '$lib/api.js';
  import { kfmt, costFmt } from '$lib/format.js';
  import type {
    CLI,
    UsageStatsResponse,
    DailyRow,
    ModelRow,
    HeatmapCell,
  } from '$lib/types.js';

  type TabId = 'overview' | 'models' | 'daily' | 'stats';

  let activeTab = $state<TabId>('overview');
  let cliFilter = $state<CLI | ''>('');
  let days = $state(30);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let data = $state<UsageStatsResponse | null>(null);

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      data = await fetchUsageStats({
        cli: cliFilter || undefined,
        days,
        heatmap_weeks: 12,
      });
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : 'failed to load stats';
      data = null;
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  // Re-fetch whenever a filter changes. Using $effect.root would be
  // overkill — a plain $effect on the filters does the right thing.
  $effect(() => {
    void cliFilter;
    void days;
    void load();
  });

  // ----------- Derived views ---------------------------------------------

  /** Top-of-page totals strip. */
  const totals = $derived(
    data
      ? {
          cost: data.total_cost_usd,
          input: data.total_input,
          output: data.total_output,
          cacheRead: data.total_cache_read,
          cacheWrite: data.total_cache_write,
          messages: data.total_messages,
          sessions: data.total_sessions,
        }
      : null
  );

  /** Cache-reuse ratio (percent). 0 when total_input is 0. */
  const cacheReusePct = $derived(
    data && data.total_input > 0
      ? (data.total_cache_read / data.total_input) * 100
      : 0
  );

  /** Daily rows are newest-first from the server; reverse for chart so
   *  the leftmost bar is the oldest. */
  const dailyChrono = $derived<DailyRow[]>(
    data ? [...data.daily].reverse() : []
  );

  /** Highest single-day total tokens — used to scale chart bar heights. */
  const maxDailyTokens = $derived(
    dailyChrono.reduce((m, r) => (r.total > m ? r.total : m), 1)
  );

  /** Models sorted by cost DESC (server-provided order). */
  const models = $derived<ModelRow[]>(data?.models ?? []);

  /** Heatmap cells, oldest-first chronological. Server-sorted. */
  const heatmap = $derived<HeatmapCell[]>(data?.heatmap ?? []);

  /** Group heatmap into weeks (columns). Each column has 7 rows
   *  (Sun..Sat). Missing days are rendered as empty. */
  const heatmapWeeks = $derived<HeatmapCell[][]>(toWeeks(heatmap));

  function toWeeks(cells: HeatmapCell[]): HeatmapCell[][] {
    if (cells.length === 0) return [];
    const weeks: HeatmapCell[][] = [];
    let cur: HeatmapCell[] = [];
    // Pad the first week with empty cells before the first weekday.
    const firstWeekday = cells[0].weekday;
    for (let i = 0; i < firstWeekday; i++) {
      cur.push({ date: '', weekday: i, messages: 0, intensity: 0 });
    }
    for (const c of cells) {
      cur.push(c);
      if (cur.length === 7) {
        weeks.push(cur);
        cur = [];
      }
    }
    if (cur.length > 0) {
      while (cur.length < 7) {
        cur.push({ date: '', weekday: cur.length, messages: 0, intensity: 0 });
      }
      weeks.push(cur);
    }
    return weeks;
  }

  function intensityColor(level: number): string {
    switch (level) {
      case 0: return 'var(--ad-bg-2)';
      case 1: return '#1e3a1e';
      case 2: return '#2e6b2e';
      case 3: return '#3fa83f';
      default: return '#5cff5c';
    }
  }

  function fmtPct(p: number): string {
    if (!Number.isFinite(p)) return '0%';
    return `${p.toFixed(p < 10 ? 1 : 0)}%`;
  }
</script>

<svelte:head>
  <title>klyne · stats</title>
</svelte:head>

<div style="padding: 20px 24px; max-width: 1200px; margin: 0 auto;">
  <!-- Header + filter strip -->
  <div style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 16px; gap: 16px; flex-wrap: wrap;">
    <div>
      <h1 style="font-size: 18px; font-weight: 600; margin: 0;">Usage stats</h1>
      <p style="font-size: 12px; color: var(--ad-muted); margin: 4px 0 0;">
        Cross-session token + cost aggregates. Read-only, derived from your local JSONL via the cost engine.
      </p>
    </div>
    <div style="display: flex; gap: 8px; align-items: center; font-size: 12px;">
      <label style="display: flex; align-items: center; gap: 6px;">
        <span style="color: var(--ad-muted);">CLI</span>
        <select bind:value={cliFilter} class="ad-input" style="height: 28px; font-size: 12px;">
          <option value="">both</option>
          <option value="claude">claude</option>
          <option value="codex">codex</option>
        </select>
      </label>
      <label style="display: flex; align-items: center; gap: 6px;">
        <span style="color: var(--ad-muted);">window</span>
        <select bind:value={days} class="ad-input" style="height: 28px; font-size: 12px;">
          <option value={7}>7d</option>
          <option value={30}>30d</option>
          <option value={90}>90d</option>
          <option value={365}>1y</option>
        </select>
      </label>
    </div>
  </div>

  <!-- Top-line totals strip -->
  {#if totals}
    <div class="ad-card" style="display: grid; grid-template-columns: repeat(5, 1fr); padding: 0; margin-bottom: 16px;">
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div class="ad-section-h" style="margin-bottom: 4px;">Total spend</div>
        <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{costFmt(totals.cost, totals.cost > 0)}</div>
      </div>
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div class="ad-section-h" style="margin-bottom: 4px;">↑ Input</div>
        <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{kfmt(totals.input)}</div>
        <div class="ad-mono" style="font-size: 10px; color: var(--ad-faint); margin-top: 4px;">{fmtPct(cacheReusePct)} cached</div>
      </div>
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div class="ad-section-h" style="margin-bottom: 4px;">↓ Output</div>
        <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{kfmt(totals.output)}</div>
      </div>
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div class="ad-section-h" style="margin-bottom: 4px;">Sessions</div>
        <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{totals.sessions}</div>
      </div>
      <div style="padding: 12px 16px;">
        <div class="ad-section-h" style="margin-bottom: 4px;">Messages</div>
        <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{kfmt(totals.messages)}</div>
      </div>
    </div>
  {/if}

  <!-- Tabs -->
  <div style="display: flex; gap: 4px; border-bottom: 1px solid var(--ad-border); margin-bottom: 16px;">
    {#each ['overview', 'models', 'daily', 'stats'] as id (id)}
      {@const isActive = activeTab === id}
      <button
        class="ad-btn"
        onclick={() => (activeTab = id as TabId)}
        style="border-radius: 0; border: none; border-bottom: 2px solid {isActive ? 'var(--ad-claude)' : 'transparent'}; padding: 8px 14px; font-size: 13px; color: {isActive ? 'var(--ad-fg)' : 'var(--ad-fg-2)'}; background: transparent;"
      >
        {id[0].toUpperCase() + id.slice(1)}
      </button>
    {/each}
  </div>

  {#if loading}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">
      Loading…
    </div>
  {:else if error}
    <div class="ad-card" style="padding: 24px; color: var(--ad-error, #f87171);">
      {error}
    </div>
  {:else if !data}
    <div class="ad-card" style="padding: 24px; color: var(--ad-muted);">
      No data yet. Start the daemon and use Claude Code or Codex to populate sessions.
    </div>
  {:else}

    <!-- Overview tab -->
    {#if activeTab === 'overview'}
      <div class="ad-card" style="padding: 16px; margin-bottom: 16px;">
        <div class="ad-section-h" style="margin-bottom: 12px;">Tokens per day</div>
        {#if dailyChrono.length === 0}
          <div style="color: var(--ad-muted); font-size: 13px;">No activity in this window.</div>
        {:else}
          <div style="display: flex; gap: 2px; align-items: flex-end; height: 140px; overflow-x: auto;">
            {#each dailyChrono as d (d.date)}
              {@const h = Math.max(2, Math.round((d.total / maxDailyTokens) * 130))}
              <div
                title="{d.date} · {kfmt(d.total)} tokens · {costFmt(d.cost_usd, d.cost_usd > 0)}"
                style="width: 14px; flex-shrink: 0; background: var(--ad-claude); height: {h}px; opacity: 0.85; border-radius: 2px 2px 0 0;"
              ></div>
            {/each}
          </div>
          <div style="display: flex; justify-content: space-between; font-size: 10px; color: var(--ad-faint); margin-top: 6px; font-family: var(--ad-font-mono);">
            <span>{dailyChrono[0]?.date ?? ''}</span>
            <span>{dailyChrono[dailyChrono.length - 1]?.date ?? ''}</span>
          </div>
        {/if}
      </div>

      <div class="ad-card" style="padding: 16px;">
        <div class="ad-section-h" style="margin-bottom: 12px;">Models by cost</div>
        {#if models.length === 0}
          <div style="color: var(--ad-muted); font-size: 13px;">No model attribution yet.</div>
        {:else}
          {#each models as m (m.model)}
            {@const pct = m.share_pct}
            <div style="margin-bottom: 8px;">
              <div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px;">
                <span class="ad-mono">{m.model}</span>
                <span class="ad-mono" style="color: var(--ad-faint);">
                  {costFmt(m.cost_usd, m.cost_usd > 0)} · {fmtPct(pct)}
                </span>
              </div>
              <div style="height: 6px; background: var(--ad-bg-2); border-radius: 3px; overflow: hidden;">
                <div style="height: 100%; background: var(--ad-claude); width: {Math.min(100, pct).toFixed(1)}%;"></div>
              </div>
            </div>
          {/each}
        {/if}
      </div>
    {/if}

    <!-- Models tab -->
    {#if activeTab === 'models'}
      <div class="ad-card" style="padding: 0;">
        <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
          <thead style="background: var(--ad-bg-2);">
            <tr>
              <th style="text-align: left; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Model</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Input</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Output</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">C.Read</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">C.Write</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Sessions</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Cost</th>
            </tr>
          </thead>
          <tbody>
            {#each models as m (m.model)}
              <tr style="border-top: 1px solid var(--ad-border-soft);">
                <td class="ad-mono" style="padding: 10px 12px;">{m.model}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.input)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.output)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.cache_read)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.cache_write)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{m.sessions}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {m.cost_usd > 0 ? 'var(--ad-fg)' : 'var(--ad-faint)'};">{costFmt(m.cost_usd, m.cost_usd > 0)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <!-- Daily tab -->
    {#if activeTab === 'daily'}
      <div class="ad-card" style="padding: 0;">
        <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
          <thead style="background: var(--ad-bg-2);">
            <tr>
              <th style="text-align: left; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Date</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Messages</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Input</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Output</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">C.Read</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">C.Write</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Total</th>
              <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Cost</th>
            </tr>
          </thead>
          <tbody>
            {#each data.daily as d (d.date)}
              <tr style="border-top: 1px solid var(--ad-border-soft);">
                <td class="ad-mono" style="padding: 10px 12px;">{d.date}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{d.messages}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.input)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.output)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.cache_read)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.cache_write)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(d.total)}</td>
                <td class="ad-mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {d.cost_usd > 0 ? 'var(--ad-fg)' : 'var(--ad-faint)'};">{costFmt(d.cost_usd, d.cost_usd > 0)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <!-- Stats tab -->
    {#if activeTab === 'stats'}
      <div class="ad-card" style="padding: 16px; margin-bottom: 16px;">
        <div class="ad-section-h" style="margin-bottom: 12px;">Activity heatmap (last 12 weeks)</div>
        {#if heatmap.length === 0}
          <div style="color: var(--ad-muted); font-size: 13px;">No activity to plot.</div>
        {:else}
          <div style="display: inline-grid; grid-auto-flow: column; gap: 3px; padding-top: 16px;">
            {#each heatmapWeeks as week, wi (wi)}
              <div style="display: grid; grid-template-rows: repeat(7, 12px); gap: 3px;">
                {#each week as c, di (di)}
                  <div
                    title="{c.date ? `${c.date} · ${c.messages} msgs` : ''}"
                    style="width: 12px; height: 12px; border-radius: 2px; background: {c.date ? intensityColor(c.intensity) : 'transparent'};"
                  ></div>
                {/each}
              </div>
            {/each}
          </div>
          <div style="display: flex; align-items: center; gap: 8px; margin-top: 14px; font-size: 11px; color: var(--ad-faint);">
            <span>Less</span>
            {#each [0, 1, 2, 3, 4] as lvl (lvl)}
              <div style="width: 12px; height: 12px; border-radius: 2px; background: {intensityColor(lvl)};"></div>
            {/each}
            <span>More</span>
          </div>
        {/if}
      </div>

      <div class="ad-card" style="display: grid; grid-template-columns: repeat(3, 1fr); padding: 0;">
        <div style="padding: 14px 16px; border-right: 1px solid var(--ad-border-soft);">
          <div class="ad-section-h" style="margin-bottom: 4px;">Favorite model</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.favorite_model || '—'}</div>
        </div>
        <div style="padding: 14px 16px; border-right: 1px solid var(--ad-border-soft);">
          <div class="ad-section-h" style="margin-bottom: 4px;">Current streak</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.current_streak} days</div>
        </div>
        <div style="padding: 14px 16px;">
          <div class="ad-section-h" style="margin-bottom: 4px;">Longest streak</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.longest_streak} days</div>
        </div>
        <div style="padding: 14px 16px; border-top: 1px solid var(--ad-border-soft); border-right: 1px solid var(--ad-border-soft);">
          <div class="ad-section-h" style="margin-bottom: 4px;">Active days</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.active_days}/{data.window_days}</div>
        </div>
        <div style="padding: 14px 16px; border-top: 1px solid var(--ad-border-soft); border-right: 1px solid var(--ad-border-soft);">
          <div class="ad-section-h" style="margin-bottom: 4px;">Peak hour</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.peak_hour_local || '—'}</div>
        </div>
        <div style="padding: 14px 16px; border-top: 1px solid var(--ad-border-soft);">
          <div class="ad-section-h" style="margin-bottom: 4px;">Window</div>
          <div class="ad-mono" style="font-size: 14px; font-weight: 600;">{data.from} → {data.to}</div>
        </div>
      </div>
    {/if}
  {/if}
</div>
