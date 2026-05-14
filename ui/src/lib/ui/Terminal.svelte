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
  import type { Message, Session, ToolCall } from '$lib/types.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { relAgo, relTime } from '$lib/format.js';

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

  // toolCallIndex maps each tool_call id seen in the visible window to its
  // originating call (which carries the tool name + input JSON). A
  // tool-role message's tool_results reference these ids; we use the map
  // to label the row with the real tool name instead of a bare "tool".
  const toolCallIndex = $derived.by(() => {
    const idx = new Map<string, ToolCall>();
    for (const m of messages) {
      if (!m.tool_calls) continue;
      for (const tc of m.tool_calls) {
        if (tc.id) idx.set(tc.id, tc);
      }
    }
    return idx;
  });

  // visibleMessages drops tool-role rows we have no way to label
  // meaningfully — empty content AND no resolvable tool_call match.
  // Those rows render as a bare "tool" header with no body and clutter
  // the tail; the user asked us to either name them or hide them.
  const visibleMessages = $derived(messages.filter(keepMessage));

  function keepMessage(m: Message): boolean {
    if (m.role !== 'tool') return true;
    if ((m.content ?? '').trim().length > 0) return true;
    const results = m.tool_results ?? [];
    return results.some((tr) => toolCallIndex.has(tr.id));
  }

  // truncate keeps long tool outputs from blowing up the terminal tile.
  // 240 chars is generous enough to surface a typical Bash stdout
  // first-line or a file-not-found error.
  function truncate(s: string, n: number = 240): string {
    s = (s ?? '').trim();
    if (s.length <= n) return s;
    return s.slice(0, n).trimEnd() + '…';
  }

  // extractToolArg pulls the most useful single argument out of a tool
  // call's input JSON for header display. Names match Claude Code's
  // canonical tools (Read/Edit/Bash/Grep/Glob/Write) plus their lower-
  // case aliases.
  function extractToolArg(name: string, rawInput: string): string {
    if (!rawInput) return '';
    let obj: Record<string, unknown>;
    try { obj = JSON.parse(rawInput) as Record<string, unknown>; } catch { return ''; }
    const lower = (name || '').toLowerCase();
    const pick = (...keys: string[]): string => {
      for (const k of keys) {
        const v = obj[k];
        if (typeof v === 'string' && v) return v;
      }
      return '';
    };
    switch (lower) {
      case 'read': case 'view': case 'open': case 'cat':
        return pick('file_path', 'path', 'filename');
      case 'edit': case 'multiedit': case 'write':
        return pick('file_path', 'path');
      case 'bash': case 'shell': case 'sh': case 'exec': case 'run':
        return pick('command', 'cmd', 'script');
      case 'grep':
        return pick('pattern', 'query');
      case 'glob':
        return pick('pattern', 'path');
      default:
        return pick('file_path', 'path', 'pattern', 'command', 'query');
    }
  }

  // toolHeader renders the per-row label for tool-role messages. Falls
  // back to "tool" only when the call cannot be resolved — keepMessage
  // already filtered those out, so in practice every rendered tool row
  // gets a real name.
  function toolHeader(m: Message): string {
    const results = m.tool_results ?? [];
    for (const tr of results) {
      const tc = toolCallIndex.get(tr.id);
      if (!tc) continue;
      const arg = extractToolArg(tc.name, tc.input);
      const error = tr.is_error ? ' ✗' : '';
      return arg ? `tool · ${tc.name}${error} — ${truncate(arg, 80)}` : `tool · ${tc.name}${error}`;
    }
    return 'tool';
  }

  // assistantToolCallSummary surfaces tool calls on an assistant turn
  // when the assistant has no text body — without it, a "call only"
  // assistant row would render blank, which is exactly the readability
  // problem the user reported on the tool-result side.
  function assistantToolCallSummary(m: Message): string {
    const calls = m.tool_calls ?? [];
    if (calls.length === 0) return '';
    const parts = calls.slice(0, 3).map((tc) => {
      const arg = extractToolArg(tc.name, tc.input);
      return arg ? `${tc.name}(${truncate(arg, 60)})` : tc.name;
    });
    if (calls.length > 3) parts.push(`+${calls.length - 3} more`);
    return 'called: ' + parts.join(', ');
  }

  function bodyHTML(m: Message): string {
    if (m.role === 'tool') {
      const text = (m.content ?? '').trim();
      if (text) return renderMarkdown(text);
      // No content but we already filtered out unresolvable rows in
      // keepMessage, so we know at least one tool_result resolves.
      // Surface the first output (truncated) as the body.
      const results = m.tool_results ?? [];
      for (const tr of results) {
        if (!toolCallIndex.has(tr.id)) continue;
        return renderMarkdown('```\n' + truncate(tr.output, 600) + '\n```');
      }
      return '';
    }
    if (m.role === 'assistant') {
      const text = (m.content ?? '').trim();
      const summary = text ? '' : assistantToolCallSummary(m);
      return renderMarkdown(text || summary);
    }
    return renderMarkdown(m.content);
  }

  function headerLabel(m: Message): string {
    if (m.role === 'tool') return toolHeader(m);
    return whoFor(m);
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
