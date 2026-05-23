<script lang="ts">
  interface Props {
    crumbs: readonly string[];
    daemonStatus: string;
    rightSlot?: import('svelte').Snippet;
  }
  const { crumbs, daemonStatus, rightSlot }: Props = $props();
</script>

<div class="topbar">
  <div class="crumbs">
    {#each crumbs as c, i}
      <span class:here={i === crumbs.length - 1}>{c}</span>
      {#if i < crumbs.length - 1}<span class="sep">/</span>{/if}
    {/each}
  </div>
  <div class="grow"></div>
  {#if rightSlot}{@render rightSlot()}{/if}
  <div class="meta">
    <span class="status-pill"><span class="dot"></span>daemon · {daemonStatus}</span>
  </div>
</div>

<style>
  .topbar {
    display: flex; align-items: center; gap: 14px;
    padding: 0 22px;
    height: 52px;
    border-bottom: 1px solid var(--border-hair);
    background: var(--bg);
    flex-shrink: 0;
  }
  .topbar .crumbs {
    display: flex; align-items: baseline; gap: 8px;
    font-family: var(--font-mono); font-size: 11.5px;
    color: var(--fg-muted);
    min-width: 0;
  }
  .topbar .crumbs :global(.here) { color: var(--fg); }
  .here { color: var(--fg); }
  .topbar .crumbs .sep { color: var(--border); }

  .grow { flex: 1; }
  .meta { display: flex; align-items: center; gap: 14px; font-family: var(--font-mono); font-size: 11px; color: var(--fg-muted); white-space: nowrap; flex-shrink: 0; }
  .meta .status-pill { white-space: nowrap; }
  .meta .status-pill {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 4px 10px; border-radius: 999px;
    background: color-mix(in oklch, var(--ok) 12%, var(--bg-card-2));
    border: 1px solid color-mix(in oklch, var(--ok) 30%, transparent);
    color: var(--ok);
    font-size: 11px;
  }
  .meta .status-pill .dot {
    width: 6px; height: 6px; border-radius: 50%;
    background: var(--ok);
    box-shadow: 0 0 0 3px color-mix(in oklch, var(--ok) 25%, transparent);
  }
</style>
