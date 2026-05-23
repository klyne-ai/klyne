<script lang="ts">
  interface Props { onClose: () => void; }
  const { onClose }: Props = $props();
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onClose()} />

<div class="palette-scrim" onclick={onClose} role="presentation">
  <div class="palette" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <input autofocus placeholder="Search messages, sessions, projects…" />
    <div class="group">
      <div class="group-label">Type to search</div>
    </div>
  </div>
</div>

<style>
  .palette-scrim {
    position: fixed; inset: 0;
    background: color-mix(in oklch, var(--bg-inset) 70%, transparent);
    backdrop-filter: blur(2px);
    z-index: 60;
    display: grid; place-items: start center;
    padding-top: 12vh;
    animation: fadein .1s ease both;
  }
  .palette {
    width: min(640px, 92vw);
    background: var(--bg);
    border: 1px solid var(--border-soft);
    border-radius: 12px;
    box-shadow: 0 20px 60px color-mix(in oklch, var(--bg-inset) 80%, transparent);
    overflow: hidden;
  }
  .palette input {
    width: 100%;
    background: transparent;
    color: var(--fg);
    font-family: var(--font-sans); font-size: 15px;
    border: 0; outline: none;
    padding: 16px 18px;
    border-bottom: 1px solid var(--border-hair);
  }
  .palette .group {
    padding: 8px 0;
  }
  .palette .group-label {
    padding: 6px 18px;
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: 0.14em; text-transform: uppercase;
    color: var(--fg-dim);
  }
  .palette .row {
    padding: 8px 18px;
    display: grid; grid-template-columns: 16px 1fr auto;
    gap: 12px;
    align-items: center;
    cursor: pointer;
  }
  .palette .row:hover { background: var(--bg-card); }
  .palette .row .icon { color: var(--fg-muted); }
  .palette .row .label { font-size: 13px; color: var(--fg); }
  .palette .row .desc { font-family: var(--font-mono); font-size: 11px; color: var(--fg-dim); }

  @keyframes fadein { from { opacity: 0; } to { opacity: 1; } }
</style>
