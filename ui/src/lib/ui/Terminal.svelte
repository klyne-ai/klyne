<!--
  Terminal — one tile in the Work view's pinned grid. Shows the most
  recent session's tail for the bound project. Re-fetches when SSE
  signals a new message in that project.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchSessions, fetchMessages } from '$lib/api.js';
  import { subscribeShared } from '$lib/sse.js';
  import { renderMarkdown } from '$lib/markdown.js';
  import type { Message, Session } from '$lib/types.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { relAgo, relTime } from '$lib/format.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';

  interface Props {
    project: ProjectAggregate;
    pinned: boolean;
    onTogglePin: (path: string) => void;
    onFocus: (path: string) => void;
    onInfo: (sessionId: string) => void;
  }
  const { project, pinned, onTogglePin, onFocus, onInfo }: Props = $props();

  let session = $state<Session | null>(null);
  let messages = $state<Message[]>([]);
  let loading = $state(true);
  let scrollEl: HTMLDivElement | null = $state(null);

  const isLive = $derived(project.lastMsAgo < 60_000);

  async function load(): Promise<void> {
    try {
      const sessResp = await fetchSessions({ project: project.project_path, limit: 1 });
      const s = sessResp.sessions[0];
      if (!s) { session = null; messages = []; return; }
      session = s;
      const msgResp = await fetchMessages(s.id, { limit: 20, order: 'desc' });
      messages = msgResp.messages.slice().reverse();
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
        if (session && p.session_id === session.id) scheduleReload();
      },
      onSessionUpdate: (p) => {
        if (session && p.session_id === session.id) scheduleReload();
      }
    });
  });
  onDestroy(() => {
    unsub?.();
    if (debounce) clearTimeout(debounce);
  });

  // Global rule (see $lib/messageFilters.ts): only conversational
  // user + assistant prose ever reaches the terminal tile. Tool /
  // system rows and pure-tool-call assistant envelopes are dropped
  // everywhere we render messages, so this tile no longer needs the
  // helpers that resolved tool_call → tool name / arg etc.
  const visibleMessages = $derived(messages.filter(isConversationalMessage));

  function bodyHTML(m: Message): string {
    // visibleMessages guarantees role is user|assistant with non-empty
    // content, so we can render the body verbatim. The tool-row and
    // pure-tool-call assistant branches that used to live here are
    // gone — those rows are filtered upstream.
    return renderMarkdown(m.content);
  }

  function headerLabel(m: Message): string {
    return whoFor(m);
  }

  function whoFor(m: Message): string {
    if (m.role === 'user') return 'user';
    return m.cli;
  }
</script>

<div class="term" class:live={isLive}>
  <div class="term-hd">
    <span class="dot {isLive ? 'dot--live' : 'dot--idle'}"></span>
    <span class="name" title={project.project_path}>{project.name}</span>
    {#if session?.model}
      <span class="branch">{session.model}</span>
    {/if}
    {#each project.clis as c}
      <span class="badge badge--{c}">{c}</span>
    {/each}
    <span class="when">{relAgo(project.lastMsAgo)} ago</span>
    <div class="actions">
      <button class="btn btn--ghost btn--sm btn--icon" title="Advisor detail" disabled={!session} onclick={() => session && onInfo(session.id)}>ⓘ</button>
      <button class="btn btn--ghost btn--sm btn--icon" title="Focus this terminal" onclick={() => onFocus(project.project_path)}>⤢</button>
      <button
        class="btn btn--ghost btn--sm btn--icon"
        title={pinned ? 'Unpin' : 'Pin to keep when idle'}
        onclick={() => onTogglePin(project.project_path)}
      >{pinned ? '✕' : '★'}</button>
    </div>
  </div>
  <div class="term-thread" bind:this={scrollEl}>
    {#if loading}
      <div class="muted" style="font-size: 12px; padding: 12px 0;">Loading thread…</div>
    {:else if visibleMessages.length === 0}
      <div class="muted" style="font-size: 12px; padding: 12px 0; text-align: center;">
        No messages yet for this project.
      </div>
    {:else}
      {#each visibleMessages as m, i (m.id || i)}
        <div class="msg {whoFor(m)}">
          <div class="who">
            <span>{headerLabel(m)}</span>
            <span class="when">{relTime(m.ts)}</span>
          </div>
          <div class="body">{@html bodyHTML(m)}</div>
        </div>
      {/each}
    {/if}
  </div>
  <div class="term-foot">
    {#if isLive}
      <span class="streaming">●</span>
      <span>live · {visibleMessages.length} msgs in tail · session <button class="btn btn--ghost btn--sm" style="display: inline; padding: 0; font-size: 11px;" onclick={() => session && goto(`/sessions/${encodeURIComponent(session.id)}`)}>open →</button></span>
    {:else}
      <span>idle · last msg {relAgo(project.lastMsAgo)} ago · {visibleMessages.length} msgs in tail</span>
    {/if}
  </div>
</div>
