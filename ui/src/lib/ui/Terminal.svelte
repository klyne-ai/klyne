<!--
  Terminal — one tile in the Work view's session grid. Bound to a single
  session_id; shows that session's tail and re-fetches when SSE signals
  new activity on it.

  This tile does NOT own its grid position — the parent (+page.svelte)
  keeps every tile in a stable insertion slot, so streaming messages on
  one tile never reshuffle the others. The live border / streaming dot
  here is the only visual cue that the tile is currently active.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchMessages } from '$lib/api.js';
  import { subscribeShared } from '$lib/sse.js';
  import { renderMarkdown } from '$lib/markdown.js';
  import type { Message, CockpitThread } from '$lib/types.js';
  import { relAgo, relTime } from '$lib/format.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';

  interface Props {
    thread: CockpitThread;
    /** Parent's clock tick (epoch-ms). Drives the live/streaming
     *  indicator without each tile owning its own setInterval. */
    tickMs: number;
    onFocus: (sessionId: string) => void;
    onInfo: (sessionId: string) => void;
  }
  const { thread, tickMs, onFocus, onInfo }: Props = $props();

  let messages = $state<Message[]>([]);
  let loading = $state(true);
  let scrollEl: HTMLDivElement | null = $state(null);
  let copied = $state(false);

  const ageMs = $derived(tickMs - thread.last_msg_at);
  const isLive = $derived(ageMs < 60_000);
  // "Streaming right now" — message arrived in the last 5s. Drives the
  // amplified border so the user can tell at a glance which tile is
  // actively producing output without us moving the tile.
  const isStreaming = $derived(ageMs < 5_000);

  function projectName(p: string): string {
    return p.split('/').filter(Boolean).pop() ?? p;
  }
  const shortId = $derived(thread.session_id.slice(0, 8));

  async function load(): Promise<void> {
    try {
      const resp = await fetchMessages(thread.session_id, { limit: 20, order: 'desc' });
      messages = resp.messages.slice().reverse();
    } catch {
      messages = [];
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (scrollEl && messages.length > 0) {
      requestAnimationFrame(() => { if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight; });
    }
  });

  let unsub: (() => void) | null = null;
  let debounce: ReturnType<typeof setTimeout> | null = null;
  function scheduleReload(): void {
    if (debounce) clearTimeout(debounce);
    debounce = setTimeout(() => { void load(); }, 500);
  }

  onMount(() => {
    void load();
    unsub = subscribeShared({
      onMsgNew: (p) => {
        if (p.session_id === thread.session_id) scheduleReload();
      },
      onSessionUpdate: (p) => {
        if (p.session_id === thread.session_id) scheduleReload();
      }
    });
  });
  onDestroy(() => {
    unsub?.();
    if (debounce) clearTimeout(debounce);
  });

  // Global rule (see $lib/messageFilters.ts): only conversational
  // user + assistant prose ever reaches the terminal tile.
  const visibleMessages = $derived(messages.filter(isConversationalMessage));

  function bodyHTML(m: Message): string {
    return renderMarkdown(m.content);
  }
  function whoFor(m: Message): string {
    if (m.role === 'user') return 'user';
    return m.cli;
  }

  function copyResume(): void {
    const cli = thread.cli === 'codex' ? 'codex' : 'claude';
    const safe = thread.project_path.replace(/'/g, `'\\''`);
    void navigator.clipboard.writeText(`cd '${safe}' && ${cli} --resume ${thread.session_id}`);
    copied = true;
    setTimeout(() => { copied = false; }, 1200);
  }
</script>

<div
  class="term"
  class:live={isLive}
  class:streaming={isStreaming}
  data-session-id={thread.session_id}
>
  <div class="term-hd">
    <span class="dot {isLive ? 'dot--live' : 'dot--idle'}"></span>
    <span class="name" title={thread.project_path}>
      {projectName(thread.project_path)}<span class="sess" title={`session ${thread.session_id}`}> · {shortId}</span>
    </span>
    {#if thread.model}
      <span class="branch">{thread.model}</span>
    {/if}
    <span class="badge badge--{thread.cli}">{thread.cli}</span>
    <span class="when">{relAgo(ageMs)} ago</span>
    <div class="actions">
      <button class="btn btn--ghost btn--sm btn--icon" title="Advisor detail" onclick={() => onInfo(thread.session_id)}>ⓘ</button>
      <button class="btn btn--ghost btn--sm btn--icon" title="Focus this terminal" onclick={() => onFocus(thread.session_id)}>⤢</button>
      <button class="btn btn--ghost btn--sm btn--icon" title="Copy resume command" onclick={copyResume}>{copied ? '✓' : '⎘'}</button>
    </div>
  </div>
  <div class="term-thread" bind:this={scrollEl}>
    {#if loading}
      <div class="muted" style="font-size: 12px; padding: 12px 0;">Loading thread…</div>
    {:else if visibleMessages.length === 0}
      <div class="muted" style="font-size: 12px; padding: 12px 0; text-align: center;">
        No messages yet for this session.
      </div>
    {:else}
      {#each visibleMessages as m, i (m.id || i)}
        <div class="msg {whoFor(m)}">
          <div class="who">
            <span>{whoFor(m)}</span>
            <span class="when">{relTime(m.ts)}</span>
          </div>
          <div class="body">{@html bodyHTML(m)}</div>
        </div>
      {/each}
    {/if}
  </div>
  <div class="term-foot">
    {#if isLive}
      <span class="streaming-dot">●</span>
      <span>live · {visibleMessages.length} msgs in tail · <button class="btn btn--ghost btn--sm" style="display: inline; padding: 0; font-size: 11px;" onclick={() => goto(`/sessions/${encodeURIComponent(thread.session_id)}`)}>open →</button></span>
    {:else}
      <span>idle · last msg {relAgo(ageMs)} ago · {visibleMessages.length} msgs in tail</span>
    {/if}
  </div>
</div>

<style>
  /* Streaming amplification: ring around the tile when a message landed
     in the last 5s. Stays put — does not move the tile. */
  .term.streaming {
    border-color: var(--ad-active, #10b981);
    box-shadow: 0 0 0 1px var(--ad-active, #10b981);
  }
  .sess {
    font-family: var(--ad-font-mono);
    font-weight: 400;
    color: var(--ad-faint);
    font-size: 0.9em;
  }
</style>
