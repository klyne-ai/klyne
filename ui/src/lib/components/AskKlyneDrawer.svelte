<!--
  AskKlyneDrawer — slide-in chat drawer for the Productivity page.

  Ephemeral: each open of the drawer starts an empty conversation.
  Each turn POSTs to /api/ask with the page's current (projects,
  fromMs, toMs) plus the in-memory message history.
-->
<script lang="ts">
  import { askKlyne, type AskMessage } from '$lib/api.js';

  let { open = $bindable(false), projects = [], fromMs, toMs }: {
    open?: boolean;
    projects?: string[];
    fromMs: number;
    toMs: number;
  } = $props();

  let messages = $state<AskMessage[]>([]);
  let input = $state('');
  let pending = $state(false);
  let error = $state<string | null>(null);
  let textareaEl = $state<HTMLTextAreaElement | undefined>(undefined);

  // Reset every time the drawer transitions closed → open so chat is
  // ephemeral by spec.
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) {
      messages = [];
      input = '';
      error = null;
      queueMicrotask(() => textareaEl?.focus());
    }
    wasOpen = open;
  });

  async function send() {
    const q = input.trim();
    if (!q || pending) return;
    error = null;

    const history = [...messages];
    messages = [...messages, { role: 'user', content: q }];
    input = '';
    pending = true;

    try {
      const resp = await askKlyne({
        projects, from_ms: fromMs, to_ms: toMs, question: q, history,
      });
      messages = [...messages, { role: 'assistant', content: resp.answer }];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      pending = false;
      queueMicrotask(() => textareaEl?.focus());
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  }

  function close() { open = false; }
</script>

{#if open}
  <div class="overlay" onclick={close} role="presentation"></div>
  <aside class="drawer" aria-label="Ask Klyne chat">
    <header class="head">
      <span class="title">Ask Klyne</span>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    <div class="msgs" aria-live="polite">
      {#if messages.length === 0 && !pending}
        <p class="empty">
          Ask about what you did in the range shown on this page.
          E.g. <em>"how many bugs fixed?"</em>, <em>"any pending?"</em>.
        </p>
      {/if}
      {#each messages as m}
        <div class="msg {m.role}">
          <span class="role">{m.role}</span>
          <p>{m.content}</p>
        </div>
      {/each}
      {#if pending}
        <div class="msg assistant pending"><p>Thinking…</p></div>
      {/if}
      {#if error}
        <div class="err"><strong>Error:</strong> {error}</div>
      {/if}
    </div>

    <footer class="foot">
      <textarea
        bind:this={textareaEl}
        bind:value={input}
        onkeydown={onKey}
        placeholder="Ask Klyne about your recent work…"
        rows="3"
        disabled={pending}
      ></textarea>
      <button class="send" onclick={send} disabled={pending || input.trim().length === 0}>
        {pending ? '…' : 'Send'}
      </button>
    </footer>
  </aside>
{/if}

<style>
  .overlay {
    position: fixed; inset: 0; background: rgb(0 0 0 / 0.4);
    z-index: 40;
  }
  .drawer {
    position: fixed; top: 0; right: 0; bottom: 0;
    width: min(480px, 100vw);
    background: var(--bg-card, #1a1a1a);
    color: var(--fg, #eee);
    border-left: 1px solid var(--border, #333);
    box-shadow: -8px 0 24px rgb(0 0 0 / 0.4);
    display: flex; flex-direction: column;
    z-index: 41;
    font-family: var(--font-sans, system-ui);
    animation: slide-in 180ms ease-out;
  }
  @keyframes slide-in {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }
  .head {
    display: flex; justify-content: space-between; align-items: center;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border, #333);
  }
  .title { font-weight: 600; }
  .close {
    background: none; border: none; color: inherit; cursor: pointer;
    font-size: 18px;
  }
  .msgs {
    flex: 1; overflow-y: auto; padding: 16px;
    display: flex; flex-direction: column; gap: 12px;
  }
  .empty {
    color: var(--fg-muted, #888); font-size: 13px; margin: 0;
  }
  .msg { display: flex; flex-direction: column; gap: 4px; }
  .msg .role {
    text-transform: uppercase; font-size: 11px;
    color: var(--fg-muted, #888); letter-spacing: 0.05em;
    font-family: var(--font-mono, ui-monospace, monospace);
  }
  .msg p { margin: 0; padding: 10px 12px; border-radius: 8px; white-space: pre-wrap; }
  .msg.user p { background: var(--bg-card-2, #222); }
  .msg.assistant p { background: var(--bg-inset, #111); border: 1px solid var(--border-hair, #2a2a2a); }
  .msg.pending p { opacity: 0.7; font-style: italic; }
  .err {
    background: color-mix(in oklch, var(--alert, #ff5555) 12%, transparent);
    color: var(--alert, #ff8a8a);
    border: 1px solid color-mix(in oklch, var(--alert, #ff5555) 30%, transparent);
    padding: 10px 12px; border-radius: 8px; font-size: 13px;
  }
  .foot {
    display: flex; gap: 8px; padding: 12px 16px;
    border-top: 1px solid var(--border, #333);
  }
  .foot textarea {
    flex: 1; background: var(--bg-inset, #111); color: inherit;
    border: 1px solid var(--border, #333); border-radius: 8px;
    padding: 8px 10px; resize: vertical;
    font: inherit;
  }
  .send {
    background: var(--accent, #c9a449); color: #000; border: none;
    padding: 0 16px; border-radius: 8px; cursor: pointer; font-weight: 600;
  }
  .send:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
