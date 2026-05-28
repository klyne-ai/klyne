<script lang="ts">
  import { goto } from '$app/navigation';
  import AgentMixDonut from '$lib/ui/AgentMixDonut.svelte';
  import { kfmt } from '$lib/format.js';
  import type { UsageStatsResponse, ProjectInsightsResponse, DailyRow } from '$lib/types.js';

  interface Props {
    stats: UsageStatsResponse | null;
    projects: ProjectInsightsResponse | null;
    /** Per-date claude/codex token breakdown — drives the bar hover
     *  tooltip. Best-effort: empty object means "no per-CLI data, fall
     *  back to the aggregate total only". */
    dailyByCli?: Record<string, { claude: number; codex: number }>;
  }
  const { stats, projects, dailyByCli = {} }: Props = $props();

  // ── tokens-per-day hover tooltip state ─────────────────────────
  // We render our own tooltip instead of the native `title` attribute
  // so we can show the per-CLI breakdown + reformat the date nicely.
  // hoverDate gates visibility; hoverX/Y are absolute coords relative
  // to the chart container so the tooltip positions over the bar.
  let hoverDate = $state<string | null>(null);
  let hoverLeft = $state(0);
  let hoverBarTop = $state(0);

  function onBarEnter(date: string, ev: MouseEvent): void {
    hoverDate = date;
    const el = ev.currentTarget as HTMLElement;
    const wrapEl = el.closest('.tpd-bars') as HTMLElement | null;
    if (!wrapEl) return;
    const wrapRect = wrapEl.getBoundingClientRect();
    const barRect = el.getBoundingClientRect();
    hoverLeft = barRect.left - wrapRect.left + barRect.width / 2;
    hoverBarTop = barRect.top - wrapRect.top;
  }
  function onBarLeave(): void { hoverDate = null; }

  // Chronological daily rows for chart (server gives newest-first, reverse for chart)
  const dailyChrono = $derived<DailyRow[]>(stats ? [...stats.daily].reverse() : []);
  const maxDailyTokens = $derived(dailyChrono.reduce((m, r) => (r.total > m ? r.total : m), 1));

  const hoverBreakdown = $derived(hoverDate ? dailyByCli[hoverDate] : undefined);
  const hoverTotal = $derived(
    hoverDate ? dailyChrono.find(d => d.date === hoverDate)?.total ?? 0 : 0
  );

  // Top 5 projects by tokens
  const topProjects = $derived(
    projects ? [...projects.projects].sort((a, b) => b.tokens - a.tokens).slice(0, 5) : []
  );

  // Totals for AgentMixDonut
  const mixTotals = $derived(
    projects
      ? {
          tokens: projects.totals.tokens,
          claude_tokens: projects.totals.claude.tokens,
          codex_tokens: projects.totals.codex.tokens,
          claude_msgs: projects.totals.claude.messages,
          codex_msgs: projects.totals.codex.messages,
        }
      : { tokens: 0, claude_tokens: 0, codex_tokens: 0, claude_msgs: 0, codex_msgs: 0 }
  );
</script>

<div style="display: flex; flex-direction: column; gap: 16px;">
  <!-- Tokens per day + Agent mix row -->
  <div style="display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: 16px;">
    <!-- Tokens per day chart -->
    <div class="ad-card" style="padding: 16px;">
      <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px;">
        <div class="ad-section-h">Tokens per day</div>
        <!-- Legend simplified to one item: DailyRow.total is a per-day
             sum across whatever CLI filter is active, NOT a per-CLI
             breakdown. Showing claude+codex chips here implied the bars
             were stacked, which they aren't. -->
        <div style="display: flex; gap: 6px; font-size: 11px; color: var(--ad-faint); align-items: center;">
          <span style="width: 10px; height: 6px; background: var(--ad-claude); border-radius: 1px; display: inline-block;"></span>
          <span class="mono">tokens / day</span>
        </div>
      </div>
      {#if dailyChrono.length === 0}
        <div style="color: var(--ad-faint); font-size: 13px; padding: 24px 0; text-align: center;">No activity in this window.</div>
      {:else}
        <!-- Bars distribute across the full container width with flex:1.
             min-width keeps single-day windows from collapsing to a
             hairline; max-width keeps a 90d window from looking like a
             pile of skyscrapers. -->
        <div class="tpd-bars" style="position: relative; display: flex; gap: 4px; align-items: flex-end; height: 140px;">
          {#each dailyChrono as d (d.date)}
            {@const h = Math.max(2, Math.round((d.total / maxDailyTokens) * 130))}
            <div
              role="img"
              aria-label="{d.date}: {kfmt(d.total)} tokens"
              onmouseenter={(e) => onBarEnter(d.date, e)}
              onmouseleave={onBarLeave}
              class="tpd-bar"
              class:tpd-bar-active={hoverDate === d.date}
              style="flex: 1 1 0; min-width: 6px; max-width: 48px; background: var(--ad-claude); height: {h}px; border-radius: 2px 2px 0 0;"
            ></div>
          {/each}

          {#if hoverDate}
            <!-- Floating tooltip — absolutely positioned over the chart
                 area, translated to sit centered above the hovered bar.
                 pointer-events:none so it doesn't steal hover from the
                 next bar over. -->
            <div
              role="tooltip"
              class="tpd-tt mono"
              style="left: {hoverLeft}px; top: {hoverBarTop}px;"
            >
              <div class="tpd-tt-date">{hoverDate}</div>
              <div class="tpd-tt-row">
                <span class="tpd-tt-swatch" style="background: var(--ad-claude);"></span>
                <span class="tpd-tt-label">claude</span>
                <span class="tpd-tt-val">{hoverBreakdown ? kfmt(hoverBreakdown.claude) : '—'}</span>
              </div>
              <div class="tpd-tt-row">
                <span class="tpd-tt-swatch" style="background: var(--ad-codex);"></span>
                <span class="tpd-tt-label">codex</span>
                <span class="tpd-tt-val">{hoverBreakdown ? kfmt(hoverBreakdown.codex) : '—'}</span>
              </div>
              <div class="tpd-tt-row tpd-tt-total">
                <span class="tpd-tt-label">total</span>
                <span class="tpd-tt-val">{kfmt(hoverTotal)}</span>
              </div>
            </div>
          {/if}
        </div>
        <div style="display: flex; justify-content: space-between; font-size: 10px; color: var(--ad-faint); margin-top: 6px; font-family: var(--ad-font-mono);">
          <span>{dailyChrono[0]?.date ?? ''}</span>
          <span>{dailyChrono[dailyChrono.length - 1]?.date ?? ''}</span>
        </div>
      {/if}
    </div>

    <!-- Agent mix donut -->
    <AgentMixDonut totals={mixTotals} />
  </div>

  <!-- Top projects by tokens -->
  <div class="ad-card" style="padding: 0;">
    <div style="display: flex; align-items: center; justify-content: space-between; padding: 14px 16px; border-bottom: 1px solid var(--ad-border-soft);">
      <div class="ad-section-h">Top projects by tokens</div>
      <button
        class="ad-btn"
        style="font-size: 11px; color: var(--ad-faint); background: none; border: none; cursor: pointer; padding: 0;"
        onclick={() => void goto('/insights?tab=projects')}
      >see all →</button>
    </div>
    {#if topProjects.length === 0}
      <div style="padding: 24px 16px; color: var(--ad-faint); font-size: 13px; text-align: center;">No project activity in this window.</div>
    {:else}
      <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
        <thead style="background: var(--ad-bg-2);">
          <tr>
            <th style="text-align: left; padding: 8px 16px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Project</th>
            <th style="text-align: right; padding: 8px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Tokens</th>
            <th style="text-align: right; padding: 8px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Sessions</th>
            <th style="text-align: right; padding: 8px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Cache hit</th>
            <th style="text-align: right; padding: 8px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Trend</th>
            <th style="padding: 8px 12px;"></th>
          </tr>
        </thead>
        <tbody>
          {#each topProjects as p (p.project_path)}
            <tr style="border-top: 1px solid var(--ad-border-soft);" class="hover-row">
              <td style="padding: 10px 16px; font-size: 13px; font-weight: 500; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{p.name}</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-size: 12.5px;">{kfmt(p.tokens)}</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-size: 12.5px; color: var(--ad-faint);">{p.sessions}</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-size: 12.5px; color: {p.cache_hit_pct >= 80 ? 'var(--ad-live)' : p.cache_hit_pct >= 50 ? 'var(--ad-warn)' : 'var(--ad-danger)'};">{Math.round(p.cache_hit_pct)}%</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-size: 12.5px; color: {p.trend_pct > 0 ? 'var(--ad-warn)' : p.trend_pct < 0 ? 'var(--ad-live)' : 'var(--ad-faint)'};">
                {p.trend_pct > 0 ? '↑' : p.trend_pct < 0 ? '↓' : '·'}{Math.abs(Math.round(p.trend_pct))}%
              </td>
              <td style="padding: 10px 12px; text-align: right;">
                <button
                  class="ad-btn"
                  style="font-size: 11px; padding: 2px 8px;"
                  onclick={() => void goto(`/projects/${encodeURIComponent(p.name)}`)}
                >open →</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>

<style>
  .hover-row:hover {
    background: var(--ad-bg-2);
  }

  /* Tokens-per-day bars: subtle opacity by default, full on hover so
     the active bar visibly highlights along with the tooltip. */
  .tpd-bar {
    opacity: 0.78;
    cursor: pointer;
    transition: opacity 120ms ease-out, filter 120ms ease-out;
  }
  .tpd-bar:hover,
  .tpd-bar-active {
    opacity: 1;
    filter: brightness(1.1);
  }
  .tpd-bar:focus-visible {
    outline: 1px solid var(--ad-fg);
    outline-offset: 2px;
  }

  /* Floating tooltip — sits above the hovered bar, centered horizontally
     on the bar's center. pointer-events: none so adjacent bars stay
     hoverable while the tooltip is rendered over them. */
  .tpd-tt {
    position: absolute;
    transform: translate(-50%, calc(-100% - 10px));
    pointer-events: none;
    background: var(--ad-panel);
    border: 1px solid var(--ad-border);
    border-radius: 8px;
    padding: 8px 10px;
    font-size: 11px;
    box-shadow: 0 12px 32px color-mix(in oklch, black 50%, transparent);
    z-index: 10;
    min-width: 168px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .tpd-tt-date {
    font-size: 11px;
    color: var(--ad-fg);
    font-weight: 600;
    letter-spacing: -0.01em;
    padding-bottom: 4px;
    margin-bottom: 2px;
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .tpd-tt-row {
    display: grid;
    grid-template-columns: 9px auto 1fr;
    gap: 8px;
    align-items: center;
    font-variant-numeric: tabular-nums;
  }
  .tpd-tt-swatch {
    width: 9px;
    height: 9px;
    border-radius: 2px;
  }
  .tpd-tt-label {
    color: var(--ad-faint);
    font-size: 11px;
    letter-spacing: 0.01em;
  }
  .tpd-tt-val {
    color: var(--ad-fg);
    text-align: right;
    font-size: 12px;
    font-weight: 600;
  }
  .tpd-tt-total {
    grid-template-columns: auto 1fr;
    padding-top: 4px;
    margin-top: 2px;
    border-top: 1px solid var(--ad-border-soft);
  }
  .tpd-tt-total .tpd-tt-label {
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 10px;
  }
</style>
