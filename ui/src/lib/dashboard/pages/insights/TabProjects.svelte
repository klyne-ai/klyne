<script lang="ts">
  import { goto } from '$app/navigation';
  import { kfmt } from '$lib/format.js';
  import type { ProjectInsightsResponse, ProjectInsight } from '$lib/types.js';

  interface Props {
    projects: ProjectInsightsResponse | null;
  }
  const { projects }: Props = $props();

  type SortKey = 'tokens' | 'trend' | 'cache' | 'compact';
  let sortBy = $state<SortKey>('tokens');

  const sorted = $derived.by<ProjectInsight[]>(() => {
    if (!projects) return [];
    const items = [...projects.projects];
    items.sort((a, b) => {
      if (sortBy === 'cache') return a.cache_hit_pct - b.cache_hit_pct;
      if (sortBy === 'trend') return Math.abs(b.trend_pct) - Math.abs(a.trend_pct);
      if (sortBy === 'compact') return b.compact_count - a.compact_count;
      return b.tokens - a.tokens;
    });
    return items;
  });

  const maxTokens = $derived(Math.max(1, ...sorted.map((p) => p.tokens)));

  function trendColor(pct: number): string {
    if (pct > 0) return 'var(--ad-warn)';
    if (pct < 0) return 'var(--ad-live)';
    return 'var(--ad-faint)';
  }

  function cacheColor(pct: number): string {
    if (pct >= 80) return 'var(--ad-live)';
    if (pct >= 50) return 'var(--ad-warn)';
    return 'var(--ad-danger)';
  }
</script>

<div style="display: flex; flex-direction: column; gap: 16px;">
  <!-- Sort controls -->
  <div style="display: flex; align-items: center; gap: 10px;">
    <span class="mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.10em; color: var(--ad-faint);">sort</span>
    <div class="seg">
      {#each ([['tokens','volume'], ['trend','trend'], ['cache','cache↓'], ['compact','/compact']] as const) as [key, label] (key)}
        <button
          class="ad-btn {sortBy === key ? 'active' : ''}"
          onclick={() => (sortBy = key)}
        >{label}</button>
      {/each}
    </div>
    {#if projects}
      <span class="mono" style="font-size: 11px; color: var(--ad-faint);">· {projects.projects.length} projects</span>
    {/if}
  </div>

  <!-- Table -->
  <div class="ad-card" style="padding: 0;">
    {#if !projects || projects.projects.length === 0}
      <div style="padding: 32px 16px; color: var(--ad-faint); font-size: 13px; text-align: center;">No project activity in this window.</div>
    {:else}
      <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
        <thead style="background: var(--ad-bg-2);">
          <tr>
            <th style="text-align: left; padding: 10px 16px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Project</th>
            <th style="padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; min-width: 160px;">Tokens (claude / codex)</th>
            <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Tokens</th>
            <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Trend</th>
            <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Cache hit</th>
            <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Tok / msg</th>
            <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">/compact</th>
            <th style="padding: 10px 12px;"></th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as p (p.project_path)}
            {@const widthPct = p.tokens > 0 ? (p.tokens / maxTokens) * 100 : 0}
            {@const cPct = p.tokens > 0 ? (p.claude.tokens / p.tokens) * 100 : 0}
            {@const xPct = p.tokens > 0 ? (p.codex.tokens / p.tokens) * 100 : 0}
            <tr style="border-top: 1px solid var(--ad-border-soft);" class="hover-row">
              <td style="padding: 10px 16px;">
                <div style="display: flex; align-items: center; gap: 8px;">
                  <span class="dot {p.last_msg_at > Date.now() - 60_000 ? 'dot--live' : 'dot--idle'}"></span>
                  <span style="font-size: 13px; font-weight: 500; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{p.name}</span>
                </div>
              </td>
              <td style="padding: 10px 12px; min-width: 160px;">
                <div style="height: 14px; position: relative; background: var(--ad-bg-2); border-radius: 4px; overflow: hidden; border: 1px solid var(--ad-border-soft);">
                  <div style="position: absolute; inset: 0; width: {widthPct}%; display: flex;">
                    {#if p.claude.tokens > 0}
                      <div style="width: {cPct}%; background: var(--ad-claude);" title="claude · {kfmt(p.claude.tokens)}"></div>
                    {/if}
                    {#if p.codex.tokens > 0}
                      <div style="width: {xPct}%; background: var(--ad-codex);" title="codex · {kfmt(p.codex.tokens)}"></div>
                    {/if}
                  </div>
                </div>
              </td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; font-weight: 600;">{kfmt(p.tokens)}</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {trendColor(p.trend_pct)}; font-weight: 600;">
                {p.trend_pct > 0 ? '↑' : p.trend_pct < 0 ? '↓' : '·'}{Math.abs(Math.round(p.trend_pct))}%
              </td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {cacheColor(p.cache_hit_pct)}; font-weight: 600;">{Math.round(p.cache_hit_pct)}%</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: var(--ad-faint);">{kfmt(Math.round(p.tokens_per_message))}</td>
              <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {p.compact_count > 0 ? 'var(--ad-danger)' : 'var(--ad-faint)'}; font-weight: {p.compact_count > 0 ? 600 : 400};">
                {p.compact_count > 0 ? `${p.compact_count}/${p.sessions}` : '—'}
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
