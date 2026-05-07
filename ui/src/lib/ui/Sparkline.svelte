<script lang="ts">
  interface Props {
    data: number[];
    height?: number;
    color?: string;
  }

  const { data, height = 40, color = 'var(--ad-claude)' }: Props = $props();

  const max = $derived(Math.max(...data, 1));
  const w = 100;
  const step = $derived(w / (data.length - 1 || 1));
  const points = $derived(
    data.map((v, i) => `${i * step},${height - (v / max) * height * 0.85 - 2}`).join(' ')
  );
</script>

<svg
  viewBox="0 0 {w} {height}"
  preserveAspectRatio="none"
  style="width: 100%; height: {height}px; display: block;"
>
  <polyline
    {points}
    fill="none"
    stroke={color}
    stroke-width="1.4"
    vector-effect="non-scaling-stroke"
  />
  {#each data as v, i}
    <circle
      cx={i * step}
      cy={height - (v / max) * height * 0.85 - 2}
      r="1.4"
      fill={color}
      vector-effect="non-scaling-stroke"
    />
  {/each}
</svg>
