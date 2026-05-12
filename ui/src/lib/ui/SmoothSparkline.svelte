<!--
  SmoothSparkline — Catmull-Rom-smoothed area path with an animated
  pulse dot on "today". Used in the Insights expanded-project row to
  replace the bar columns from earlier iterations.
-->
<script lang="ts">
  interface Props {
    data: number[];
    accentLastDays?: number;
  }
  const { data, accentLastDays = 3 }: Props = $props();

  const w = 100;
  const h = 36;
  const pad = 2;

  function buildPaths(values: number[]): { area: string; line: string; lastX: number; lastY: number; pts: Array<[number, number]> } {
    if (values.length === 0) return { area: '', line: '', lastX: 0, lastY: 0, pts: [] };
    const max = Math.max(...values) || 1;
    const step = values.length > 1 ? (w - pad * 2) / (values.length - 1) : 0;
    const pts: Array<[number, number]> = values.map((v, i) => [
      pad + i * step,
      h - pad - (v / max) * (h - pad * 2)
    ]);
    let line = '';
    pts.forEach(([x, y], i) => {
      if (i === 0) { line = `M ${x.toFixed(2)} ${y.toFixed(2)}`; return; }
      const [px, py] = pts[i - 1];
      const cx = (px + x) / 2;
      line += ` Q ${px.toFixed(2)} ${py.toFixed(2)} ${cx.toFixed(2)} ${((py + y) / 2).toFixed(2)} T ${x.toFixed(2)} ${y.toFixed(2)}`;
    });
    const last = pts[pts.length - 1];
    const area = `${line} L ${last[0].toFixed(2)} ${h - pad} L ${pts[0][0].toFixed(2)} ${h - pad} Z`;
    return { area, line, lastX: last[0], lastY: last[1], pts };
  }

  const paths = $derived(buildPaths(data));
  const gradId = $derived('sg-' + Math.random().toString(36).slice(2, 8));
</script>

<svg viewBox="0 0 {w} {h}" preserveAspectRatio="none" class="spark-canvas" style="overflow: visible;">
  <defs>
    <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="var(--ad-accent)" stop-opacity="0.35" />
      <stop offset="100%" stop-color="var(--ad-accent)" stop-opacity="0" />
    </linearGradient>
  </defs>
  {#if paths.area}
    <path d={paths.area} fill="url(#{gradId})" />
    <path d={paths.line} fill="none" stroke="var(--ad-accent)" stroke-width="1.4" stroke-linejoin="round" stroke-linecap="round" />
    {#each paths.pts.slice(-accentLastDays) as p, i}
      <circle cx={p[0]} cy={p[1]} r={i === accentLastDays - 1 ? 2.2 : 1.4} fill="var(--ad-accent)" />
    {/each}
    <circle cx={paths.lastX} cy={paths.lastY} r="3.5" fill="var(--ad-accent)" opacity="0.25">
      <animate attributeName="r" values="3.5;6.5;3.5" dur="2.2s" repeatCount="indefinite" />
      <animate attributeName="opacity" values="0.35;0;0.35" dur="2.2s" repeatCount="indefinite" />
    </circle>
  {/if}
</svg>
