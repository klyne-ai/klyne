<script lang="ts">
  interface Props {
    data: number[];
    height?: number;
  }

  const { data, height = 40 }: Props = $props();

  const max = $derived(Math.max(...data, 1));
</script>

<div style="display: flex; align-items: flex-end; gap: 3px; height: {height}px;">
  {#each data as v, i}
    <div
      style="flex: 1; height: {(v / max) * 100}%; background: {i === data.length - 1 ? 'var(--ad-claude)' : 'var(--ad-border)'}; border-radius: 1px; min-height: 2px;"
    ></div>
  {/each}
</div>
