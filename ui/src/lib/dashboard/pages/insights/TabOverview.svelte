<script lang="ts">
  import { goto } from '$app/navigation';
  import AgentMixDonut from '$lib/ui/AgentMixDonut.svelte';
  import { kfmt, costFmt } from '$lib/format.js';
  import type { UsageStatsResponse, ProjectInsightsResponse, DailyRow } from '$lib/types.js';

  interface Props {
    stats: UsageStatsResponse | null;
    projects: ProjectInsightsResponse | null;
  }
  const { stats, projects }: Props = $props();

  // Chronological daily rows for chart (server gives newest-first, reverse for chart)
  const dailyChrono = $derived<DailyRow[]>(stats ? [...stats.daily].reverse() : []);
  const maxDailyTokens = $derived(dailyChrono.reduce((m, r) => (r.total > m ? r.total : m), 1));

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
        <div style="display: flex; gap: 12px; font-size: 11px; color: var(--ad-faint);">
          <span style="display: flex; align-items: center; gap: 4px;">
            <span style="width: 10px; height: 6px; background: var(--ad-claude); border-radius: 1px; display: inline-block;"></span>
            <span class="mono">claude</span>
          </span>
          <span style="display: flex; align-items: center; gap: 4px;">
            <span style="width: 10px; height: 6px; background: var(--ad-codex); border-radius: 1px; display: inline-block;"></span>
            <span class="mono">codex</span>
          </span>
        </div>
      </div>
      {#if dailyChrono.length === 0}
        <div style="color: var(--ad-faint); font-size: 13px; padding: 24px 0; text-align: center;">No activity in this window.</div>
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

    <!-- Agent mix donut -->
    <AgentMixDonut totals={mixTotals} />
  </div>

  <!-- Top projects by spend -->
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
</style>
