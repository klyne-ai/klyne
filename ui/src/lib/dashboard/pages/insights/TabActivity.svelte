<script lang="ts">
  import type { UsageStatsResponse, HeatmapCell } from '$lib/types.js';

  interface Props {
    stats: UsageStatsResponse | null;
  }
  const { stats }: Props = $props();

  const heatmap = $derived<HeatmapCell[]>(stats?.heatmap ?? []);

  const heatmapWeeks = $derived<HeatmapCell[][]>(toWeeks(heatmap));

  function toWeeks(cells: HeatmapCell[]): HeatmapCell[][] {
    if (cells.length === 0) return [];
    const weeks: HeatmapCell[][] = [];
    let cur: HeatmapCell[] = [];
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

  const DAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
</script>

<div style="display: flex; flex-direction: column; gap: 16px;">
  <!-- Heatmap -->
  <div class="ad-card" style="padding: 16px;">
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px;">
      <div class="ad-section-h">Activity heatmap <span style="font-weight: 400; color: var(--ad-faint);">last 14 weeks</span></div>
      <div style="display: flex; align-items: center; gap: 8px; font-size: 11px; color: var(--ad-faint);">
        <span>Less</span>
        {#each [0, 1, 2, 3, 4] as lvl (lvl)}
          <div style="width: 12px; height: 12px; border-radius: 2px; background: {intensityColor(lvl)};"></div>
        {/each}
        <span>More</span>
      </div>
    </div>
    {#if heatmap.length === 0}
      <div style="color: var(--ad-faint); font-size: 13px; padding: 24px 0; text-align: center;">No activity to plot.</div>
    {:else}
      <div style="display: grid; grid-template-columns: auto 1fr; gap: 8px; overflow-x: auto;">
        <!-- Day labels -->
        <div style="display: flex; flex-direction: column; gap: 3px; padding-top: 2px;">
          {#each DAY_LABELS as d, i (d)}
            <div class="mono" style="font-size: 9px; height: 12px; line-height: 12px; color: var(--ad-faint); visibility: {i % 2 === 0 ? 'visible' : 'hidden'};">{d}</div>
          {/each}
        </div>
        <!-- Grid -->
        <div style="display: inline-grid; grid-auto-flow: column; gap: 3px;">
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
      </div>
    {/if}
  </div>

  <!-- Stat tiles -->
  {#if stats}
    <div style="display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px;">
      <div class="ad-card" style="padding: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px;">Current streak</div>
        <div class="mono" style="font-size: 22px; font-weight: 600; color: var(--ad-fg); letter-spacing: -0.02em;">
          {stats.current_streak}<span style="font-size: 14px; color: var(--ad-faint); margin-left: 3px;">days</span>
        </div>
      </div>
      <div class="ad-card" style="padding: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px;">Longest streak</div>
        <div class="mono" style="font-size: 22px; font-weight: 600; color: var(--ad-fg); letter-spacing: -0.02em;">
          {stats.longest_streak}<span style="font-size: 14px; color: var(--ad-faint); margin-left: 3px;">days</span>
        </div>
      </div>
      <div class="ad-card" style="padding: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px;">Favorite model</div>
        <div class="mono" style="font-size: 13px; font-weight: 600; color: var(--ad-fg); word-break: break-word;">{stats.favorite_model || '—'}</div>
      </div>
      <div class="ad-card" style="padding: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px;">Peak hour</div>
        <div class="mono" style="font-size: 16px; font-weight: 600; color: var(--ad-fg);">{stats.peak_hour_local || '—'}</div>
        <div class="mono" style="font-size: 10px; color: var(--ad-faint); margin-top: 4px;">{stats.active_days}/{stats.window_days} active days</div>
      </div>
    </div>
  {/if}
</div>
