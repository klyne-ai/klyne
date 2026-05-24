<!--
  KebabMenu — small reusable trigger/popover for per-row actions.

  Renders a `⋯` trigger button. On click, opens a popover anchored beneath
  the trigger. Children are rendered inside the popover; the caller is
  expected to use <button class="km-item"> elements (or any element) for
  individual entries.

  Closes on: outside click, Escape, or a menu item being clicked. Items
  call `close()` on selection — the helper is exposed to children via
  the `close` prop passed back through the children snippet.

  ARIA: the trigger has `aria-haspopup="menu"` + `aria-expanded`. The
  popover is `role="menu"`. Items should be `role="menuitem"`.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { Snippet } from 'svelte';

  interface Props {
    /** Items snippet. Receives `close` so menu items can dismiss the menu. */
    children: Snippet<[{ close: () => void }]>;
    /** ARIA label for the trigger button. Defaults to "More actions". */
    label?: string;
  }
  const { children, label = 'More actions' }: Props = $props();

  let open = $state(false);
  let menuRoot: HTMLDivElement | null = $state(null);

  function toggle(): void {
    open = !open;
  }
  function close(): void {
    open = false;
  }

  function onDocClick(e: MouseEvent): void {
    if (!menuRoot) return;
    if (e.target instanceof Node && menuRoot.contains(e.target)) return;
    close();
  }
  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  $effect(() => {
    if (open) {
      document.addEventListener('mousedown', onDocClick);
      document.addEventListener('keydown', onKey);
      return () => {
        document.removeEventListener('mousedown', onDocClick);
        document.removeEventListener('keydown', onKey);
      };
    }
    return undefined;
  });

  onDestroy(() => {
    document.removeEventListener('mousedown', onDocClick);
    document.removeEventListener('keydown', onKey);
  });
</script>

<div class="km-root" bind:this={menuRoot}>
  <button
    type="button"
    class="km-trigger"
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={label}
    onclick={toggle}
  >
    <span class="km-dots" aria-hidden="true">⋯</span>
  </button>

  {#if open}
    <div class="km-pop" role="menu">
      {@render children({ close })}
    </div>
  {/if}
</div>

<style>
  .km-root {
    position: relative;
    display: inline-flex;
  }
  .km-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border-radius: 6px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--fg-muted);
    cursor: pointer;
    transition: background 120ms ease, color 120ms ease, border-color 120ms ease;
  }
  .km-trigger:hover,
  .km-trigger[aria-expanded='true'] {
    background: var(--bg-card-2);
    color: var(--fg);
    border-color: var(--border-hair);
  }
  .km-dots {
    font-size: 18px;
    line-height: 1;
    letter-spacing: 0.05em;
  }

  .km-pop {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    min-width: 200px;
    background: var(--bg-card);
    border: 1px solid var(--border-soft);
    border-radius: 8px;
    box-shadow: 0 14px 36px -12px rgba(0, 0, 0, 0.55);
    padding: 4px;
    z-index: 50;
    animation: km-pop-in 120ms ease-out both;
  }
  @keyframes km-pop-in {
    from {
      opacity: 0;
      transform: translateY(-4px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  /* Default styling for menu items — children can opt into this via .km-item */
  :global(.km-item) {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 10px;
    border: 0;
    background: transparent;
    color: var(--fg-soft);
    font-family: var(--font-mono);
    font-size: 12px;
    text-align: left;
    border-radius: 6px;
    cursor: pointer;
  }
  :global(.km-item:hover) {
    background: var(--bg-card-2);
    color: var(--fg);
  }
  :global(.km-item--danger) {
    color: var(--alert);
  }
  :global(.km-item--danger:hover) {
    background: color-mix(in oklch, var(--alert) 14%, var(--bg-card-2));
    color: var(--alert);
  }
</style>
