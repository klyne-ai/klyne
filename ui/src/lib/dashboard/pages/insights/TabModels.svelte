<script lang="ts">
  import { kfmt } from '$lib/format.js';
  import type { UsageStatsResponse, ModelRow } from '$lib/types.js';

  interface Props {
    stats: UsageStatsResponse | null;
  }
  const { stats }: Props = $props();

  const models = $derived<ModelRow[]>(stats?.models ?? []);

  function fmtPct(p: number): string {
    if (!Number.isFinite(p)) return '0%';
    return `${p.toFixed(p < 10 ? 1 : 0)}%`;
  }

  // Cache hit pct from model row: cache_read / (input + cache_read) roughly
  // The ModelRow doesn't have direct cache_hit_pct, approximate from cache_read / total
  function cacheHitPct(m: ModelRow): number {
    if (m.total <= 0) return 0;
    return (m.cache_read / m.total) * 100;
  }
</script>

<div class="ad-card" style="padding: 0;">
  {#if models.length === 0}
    <div style="padding: 32px 16px; color: var(--ad-faint); font-size: 13px; text-align: center;">No model attribution in this window.</div>
  {:else}
    <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
      <thead style="background: var(--ad-bg-2);">
        <tr>
          <th style="text-align: left; padding: 10px 16px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Model</th>
          <th style="padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; min-width: 160px;">Share</th>
          <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Sessions</th>
          <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Input</th>
          <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Output</th>
          <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">Cache hit</th>
          <th style="text-align: right; padding: 10px 12px; font-weight: 500; color: var(--ad-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;">%</th>
        </tr>
      </thead>
      <tbody>
        {#each models as m, i (m.model)}
          <tr style="border-top: 1px solid var(--ad-border-soft);">
            <td class="mono" style="padding: 10px 16px; font-size: 12px;">{m.model}</td>
            <td style="padding: 10px 12px; min-width: 160px;">
              <div style="height: 6px; background: var(--ad-bg-2); border-radius: 3px; overflow: hidden; border: 1px solid var(--ad-border-soft);">
                <div style="height: 100%; background: {i === 0 ? 'var(--ad-claude)' : 'color-mix(in srgb, var(--ad-claude) 50%, var(--ad-bg-2))'}; width: {Math.min(100, m.share_pct).toFixed(1)}%;"></div>
              </div>
            </td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{m.sessions}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.input)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right;">{kfmt(m.output)}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: {cacheHitPct(m) >= 80 ? 'var(--ad-live)' : cacheHitPct(m) >= 50 ? 'var(--ad-warn)' : 'var(--ad-faint)'};">{fmtPct(cacheHitPct(m))}</td>
            <td class="mono ad-tnum" style="padding: 10px 12px; text-align: right; color: var(--ad-faint);">{fmtPct(m.share_pct)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
