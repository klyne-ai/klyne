<!--
  DailyActivityChart — Chart.js area chart of token activity over the
  last N days, with separate claude / codex series. Reads theme tokens
  from CSS variables so it respects the active theme.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Chart, registerables } from 'chart.js';
  import type { ProjectInsight } from '$lib/types.js';

  interface Props {
    projects: ProjectInsight[];
  }
  const { projects }: Props = $props();

  Chart.register(...registerables);

  let canvasEl: HTMLCanvasElement | null = $state(null);
  let chart: Chart | null = null;

  interface DailyBucket { claude: number; codex: number; day: string; }

  /**
   * Aggregate per-project daily token series into a unified
   * claude+codex split for the chart. Project totals are split
   * proportionally based on the project's CLI ratios.
   */
  function aggregate(items: ProjectInsight[]): DailyBucket[] {
    const map = new Map<string, DailyBucket>();
    for (const p of items) {
      const totalProject = p.claude.tokens + p.codex.tokens;
      const claudeShare = totalProject > 0 ? p.claude.tokens / totalProject : 1;
      for (const d of p.daily) {
        const bucket = map.get(d.day) ?? { claude: 0, codex: 0, day: d.day };
        bucket.claude += d.tokens * claudeShare;
        bucket.codex  += d.tokens * (1 - claudeShare);
        map.set(d.day, bucket);
      }
    }
    return Array.from(map.values()).sort((a, b) => (a.day < b.day ? -1 : 1));
  }

  function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function build(): void {
    if (!canvasEl) return;
    if (chart) { chart.destroy(); chart = null; }
    const ctx = canvasEl.getContext('2d');
    if (!ctx) return;
    const daily = aggregate(projects);
    if (daily.length === 0) return;

    const claudeColor = cssVar('--ad-claude') || '#e3a06a';
    const codexColor  = cssVar('--ad-codex')  || '#b78bff';
    const fg          = cssVar('--ad-faint')  || '#666';
    const grid        = cssVar('--ad-border-soft') || '#2a2e3a';
    const panel       = cssVar('--ad-panel') || '#222';

    chart = new Chart(ctx, {
      type: 'line',
      data: {
        labels: daily.map((d) => d.day),
        datasets: [
          {
            label: 'claude',
            data: daily.map((d) => Math.round(d.claude)),
            borderColor: claudeColor,
            backgroundColor: `color-mix(in oklch, ${claudeColor} 35%, transparent)`,
            borderWidth: 1.75,
            tension: 0.4,
            pointRadius: 0,
            pointHoverRadius: 4,
            fill: true
          },
          {
            label: 'codex',
            data: daily.map((d) => Math.round(d.codex)),
            borderColor: codexColor,
            backgroundColor: `color-mix(in oklch, ${codexColor} 30%, transparent)`,
            borderWidth: 1.75,
            tension: 0.4,
            pointRadius: 0,
            pointHoverRadius: 4,
            fill: true
          }
        ]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        animation: { duration: 850, easing: 'easeOutQuart' },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: panel,
            borderColor: cssVar('--ad-border') || '#333',
            borderWidth: 1,
            titleColor: cssVar('--ad-fg') || '#fff',
            bodyColor: cssVar('--ad-fg-2') || '#ccc',
            titleFont: { family: 'JetBrains Mono', size: 11, weight: 600 },
            bodyFont: { family: 'JetBrains Mono', size: 11 },
            padding: 10,
            cornerRadius: 8,
            displayColors: true,
            boxPadding: 4,
            callbacks: {
              label: (item) => {
                const v = item.parsed.y as number;
                return ` ${item.dataset.label}  ${kfmt(v)}`;
              }
            }
          }
        },
        scales: {
          x: {
            grid: { display: false },
            ticks: {
              color: fg,
              font: { family: 'JetBrains Mono', size: 10 },
              maxTicksLimit: 6,
              autoSkip: true,
              callback: (_v, i, ticks) => {
                if (i === 0) return `${ticks.length}d ago`;
                if (i === ticks.length - 1) return 'today';
                return '';
              }
            },
            border: { color: grid }
          },
          y: {
            grid: { color: grid, drawTicks: false },
            border: { display: false },
            ticks: {
              color: fg,
              font: { family: 'JetBrains Mono', size: 10 },
              maxTicksLimit: 5,
              callback: (v) => kfmt(v as number),
              padding: 8
            }
          }
        }
      }
    });
  }

  function kfmt(n: number): string {
    if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(1) + 'B';
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return String(Math.round(n));
  }

  onMount(() => { build(); });
  onDestroy(() => { chart?.destroy(); chart = null; });

  $effect(() => {
    void projects;
    build();
  });
</script>

<div class="chart-card enter enter-2" style="min-height: 280px;">
  <div class="row" style="justify-content: space-between; align-items: baseline; margin-bottom: 14px;">
    <div>
      <h3>Daily activity</h3>
      <div class="sub">tokens generated · stacked claude over codex</div>
    </div>
    <div class="row" style="gap: 14px;">
      <span class="row" style="gap: 6px;">
        <i style="width: 9px; height: 9px; border-radius: 2px; background: var(--ad-claude); display: inline-block;"></i>
        <span class="mono" style="font-size: 11px; color: var(--ad-fg-2);">claude</span>
      </span>
      <span class="row" style="gap: 6px;">
        <i style="width: 9px; height: 9px; border-radius: 2px; background: var(--ad-codex); display: inline-block;"></i>
        <span class="mono" style="font-size: 11px; color: var(--ad-fg-2);">codex</span>
      </span>
    </div>
  </div>
  <div style="position: relative; height: 220px;">
    <canvas bind:this={canvasEl}></canvas>
  </div>
</div>
