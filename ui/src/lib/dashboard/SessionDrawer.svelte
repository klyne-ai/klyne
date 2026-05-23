<script lang="ts">
  import Icon from './Icon.svelte';
  interface Props { sessionId: string; onClose: () => void; }
  const { sessionId, onClose }: Props = $props();
</script>

<div class="drawer-scrim" onclick={onClose} role="presentation"></div>
<aside class="drawer" role="dialog" aria-modal="true" aria-label="Session detail">
  <header class="drawer-head">
    <span class="kicker">Session</span>
    <span class="mono">{sessionId}</span>
    <button class="x" onclick={onClose} aria-label="Close"><Icon name="x" /></button>
  </header>
  <div class="drawer-body">
    <p>Session detail wiring lands in Task 8.</p>
  </div>
</aside>

<style>
  .drawer-scrim {
    position: fixed; inset: 0;
    background: color-mix(in oklch, var(--bg-inset) 70%, transparent);
    backdrop-filter: blur(2px);
    z-index: 50;
    animation: fadein .12s ease both;
  }
  .drawer {
    position: fixed; top: 0; right: 0; bottom: 0;
    width: min(820px, 92vw);
    background: var(--bg);
    border-left: 1px solid var(--border-hair);
    box-shadow: -16px 0 40px color-mix(in oklch, var(--bg-inset) 60%, transparent);
    z-index: 51;
    display: flex; flex-direction: column;
    animation: slidein .18s cubic-bezier(.3,.7,.2,1) both;
  }
  @keyframes fadein { from { opacity: 0; } to { opacity: 1; } }
  @keyframes slidein { from { transform: translateX(20px); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
  .drawer-head {
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-hair);
    display: flex; align-items: center; gap: 12px;
  }
  .drawer-body { flex: 1; overflow: auto; padding: 18px 22px 32px; }
  .drawer .x {
    margin-left: auto;
    background: transparent; border: 1px solid var(--border-hair);
    width: 26px; height: 26px;
    border-radius: 6px;
    display: grid; place-items: center;
    color: var(--fg-muted);
    cursor: pointer;
  }
  .drawer .x:hover { color: var(--fg); border-color: var(--border-soft); }
  .kicker {
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: 0.12em; text-transform: uppercase;
    color: var(--fg-muted);
  }
  .mono { font-family: var(--font-mono); font-variant-numeric: tabular-nums; }
</style>
