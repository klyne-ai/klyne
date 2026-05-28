<!--
  AgentMixDonut — Chart.js doughnut showing claude / codex token share
  across the current window. Center text shows the dominant CLI's pct.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Chart, registerables } from 'chart.js';
  import { kfmt } from '$lib/format.js';

  interface Totals {
    tokens: number;
    claude_tokens: number;
    codex_tokens: number;
    claude_msgs: number;
    codex_msgs: number;
  }
  interface Props { totals: Totals; }
  const { totals }: Props = $props();

  Chart.register(...registerables);

  let canvasEl: HTMLCanvasElement | null = $state(null);
  let chart: Chart | null = null;

  // Show whole-number percent for clean splits, but never round a
  // non-zero codex slice away — if claude is ~99.96% we want the user
  // to see 99.96, not 100, otherwise the legend "codex 206K" reads as a
  // contradiction.
  const claudePct = $derived.by<string>(() => {
    if (totals.tokens <= 0) return '0';
    const raw = (totals.claude_tokens / totals.tokens) * 100;
    if (totals.codex_tokens === 0 || raw === 100) return String(Math.round(raw));
    if (totals.claude_tokens === 0)              return '0';
    // Adaptive precision: between 99 and 100, two decimals (99.96);
    // between 1 and 99, one decimal; otherwise integer.
    if (raw > 99)      return raw.toFixed(2);
    if (raw < 1)       return raw.toFixed(2);
    if (raw % 1 < 0.05 || raw % 1 > 0.95) return String(Math.round(raw));
    return raw.toFixed(1);
  });

  function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function build(): void {
    if (!canvasEl) return;
    if (chart) { chart.destroy(); chart = null; }
    const ctx = canvasEl.getContext('2d');
    if (!ctx) return;
    const claude = cssVar('--ad-claude') || '#e3a06a';
    const codex = cssVar('--ad-codex') || '#b78bff';
    chart = new Chart(ctx, {
      type: 'doughnut',
      data: {
        labels: ['claude', 'codex'],
        datasets: [{
          data: [totals.claude_tokens, totals.codex_tokens],
          backgroundColor: [claude, codex],
          borderColor: cssVar('--ad-panel') || '#222',
          borderWidth: 3,
          hoverOffset: 6
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        cutout: '72%',
        animation: { duration: 900, easing: 'easeOutQuart' },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: cssVar('--ad-panel') || '#222',
            borderColor: cssVar('--ad-border') || '#333',
            borderWidth: 1,
            titleColor: cssVar('--ad-fg') || '#fff',
            bodyColor: cssVar('--ad-fg-2') || '#ccc',
            titleFont: { family: 'JetBrains Mono', size: 11, weight: 600 },
            bodyFont: { family: 'JetBrains Mono', size: 11 },
            padding: 10,
            cornerRadius: 8,
            callbacks: {
              label: (item) => ` ${item.label}  ${kfmt(item.parsed as number)}`
            }
          }
        }
      }
    });
  }

  onMount(() => { build(); });
  onDestroy(() => { chart?.destroy(); chart = null; });

  $effect(() => {
    void totals;
    build();
  });
</script>

<div class="chart-card enter enter-3" style="min-height: 280px; display: flex; flex-direction: column;">
  <div style="margin-bottom: 8px;">
    <h3>Agent mix</h3>
    <div class="sub">share of {kfmt(totals.tokens)} tokens across all projects</div>
  </div>
  <div style="display: flex; align-items: center; gap: 18px; flex: 1; min-height: 0;">
    <div style="position: relative; width: 156px; height: 156px; flex: none;">
      <canvas bind:this={canvasEl}></canvas>
      <div style="position: absolute; inset: 0; display: grid; place-items: center; pointer-events: none; text-align: center;">
        <div>
          <div class="mono" style="font-size: 22px; font-weight: 600; color: var(--ad-fg); letter-spacing: -0.02em;">
            {claudePct}<span style="font-size: 13px; color: var(--ad-faint);">%</span>
          </div>
          <div class="mono faint" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.10em; margin-top: 2px;">claude</div>
        </div>
      </div>
    </div>
    <div style="flex: 1; display: flex; flex-direction: column; gap: 10px;">
      <div class="row" style="gap: 10px; padding: 8px 10px; background: var(--ad-bg-2); border-radius: 8px; border: 1px solid var(--ad-border-soft);">
        <i style="width: 9px; height: 9px; border-radius: 2px; background: var(--ad-claude); flex: none;"></i>
        <div style="flex: 1;">
          <div style="font-size: 12px; font-weight: 600; color: var(--ad-fg);">claude</div>
          <div class="mono faint" style="font-size: 10.5px;">{kfmt(totals.claude_msgs)} msgs</div>
        </div>
        <div class="mono" style="font-size: 13px; font-weight: 600; color: var(--ad-claude);">{kfmt(totals.claude_tokens)}</div>
      </div>
      <div class="row" style="gap: 10px; padding: 8px 10px; background: var(--ad-bg-2); border-radius: 8px; border: 1px solid var(--ad-border-soft);">
        <i style="width: 9px; height: 9px; border-radius: 2px; background: var(--ad-codex); flex: none;"></i>
        <div style="flex: 1;">
          <div style="font-size: 12px; font-weight: 600; color: var(--ad-fg);">codex</div>
          <div class="mono faint" style="font-size: 10.5px;">{kfmt(totals.codex_msgs)} msgs</div>
        </div>
        <div class="mono" style="font-size: 13px; font-weight: 600; color: var(--ad-codex);">{kfmt(totals.codex_tokens)}</div>
      </div>
    </div>
  </div>
</div>
