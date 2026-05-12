<!--
  Terminal — one tile in the Work view's pinned grid. Shows the most
  recent session's tail for the bound project. Re-fetches when SSE
  signals a new message in that project.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchSessions, fetchMessages } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import { renderMarkdown } from '$lib/markdown.js';
  import type { Message, Session } from '$lib/types.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { relAgo, relTime } from '$lib/format.js';

  interface Props {
    project: ProjectAggregate;
    onUnpin: (path: string) => void;
    onFocus: (path: string) => void;
    onInfo: (sessionId: string) => void;
  }
  const { project, onUnpin, onFocus, onInfo }: Props = $props();

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
    unsub = subscribe({
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

  function bodyHTML(m: Message): string {
    return renderMarkdown(m.content);
  }
  function whoFor(m: Message): string {
    if (m.role === 'user') return 'user';
    if (m.role === 'tool') return 'tool';
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
      <button class="btn btn--ghost btn--sm btn--icon" title="Unpin" onclick={() => onUnpin(project.project_path)}>✕</button>
    </div>
  </div>
  <div class="term-thread" bind:this={scrollEl}>
    {#if loading}
      <div class="muted" style="font-size: 12px; padding: 12px 0;">Loading thread…</div>
    {:else if messages.length === 0}
      <div class="muted" style="font-size: 12px; padding: 12px 0; text-align: center;">
        No messages yet for this project.
      </div>
    {:else}
      {#each messages as m, i (m.id || i)}
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
      <span class="streaming">●</span>
      <span>live · {messages.length} msgs in tail · session <button class="btn btn--ghost btn--sm" style="display: inline; padding: 0; font-size: 11px;" onclick={() => session && goto(`/sessions/${encodeURIComponent(session.id)}`)}>open →</button></span>
    {:else}
      <span>idle · last msg {relAgo(project.lastMsAgo)} ago · {messages.length} msgs in tail</span>
    {/if}
  </div>
</div>
