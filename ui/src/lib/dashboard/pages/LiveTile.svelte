<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl } from '$lib/dashboard/url-state';
  import { relAgo, kfmt } from '$lib/format';
  import { isConversationalMessage } from '$lib/messageFilters';
  import { hiddenSessionIds, toggleHidden } from '$lib/hidden-sessions.svelte';
  import ThreadPeekModal from './ThreadPeekModal.svelte';
  import type { CockpitThread, Message } from '$lib/types';

  interface Props {
    thread: CockpitThread;
    recent: Message[];
    tickMs: number;
    onFocus?: (id: string) => void;
  }
  const { thread, recent, tickMs, onFocus }: Props = $props();

  let expandBtn = $state<HTMLButtonElement | null>(null);
  let expandOpen = $state(false);
  let expandOrigin = $state<{ x: number; y: number } | null>(null);
  // `recent` arrives newest-first (fetched order:'desc', conversational-only).
  const conversational = $derived(recent.filter(isConversationalMessage));
  const hiddenCount = $derived(Math.max(0, conversational.length - 3));

  function openExpand(): void {
    if (expandBtn) {
      const r = expandBtn.getBoundingClientRect();
      expandOrigin = {
        x: ((r.left + r.width / 2) / window.innerWidth) * 100,
        y: ((r.top + r.height / 2) / window.innerHeight) * 100,
      };
    }
    expandOpen = true;
  }

  // The 3 most-recent turns, newest at top — `conversational` is already
  // newest-first, so take the head, not the tail. (The old `.slice(-3)`
  // took the 3 *oldest* of the window, which only looked right when a
  // session had ≤3 conversational rows in its raw tail.)
  const tail = $derived(conversational.slice(0, 3));
  const lastAgo = $derived(relAgo(tickMs - thread.last_msg_at));
  const isLive = $derived(tickMs - thread.last_msg_at < 60_000);
  const isHidden = $derived(hiddenSessionIds().has(thread.session_id));

  // project_name derived from project_path
  const projectName = $derived(
    thread.project_path.split('/').filter(Boolean).pop() ?? thread.project_path
  );

  // Shorten session_id for display
  const shortId = $derived(thread.session_id.slice(0, 8));

  function openDrawer(): void {
    void goto(sessionUrl($page.url.pathname + $page.url.search, thread.session_id));
  }

  function openFocus(e: MouseEvent): void {
    e.stopPropagation();
    onFocus?.(thread.session_id);
  }
</script>

<div
  class="card live-tile"
  style:border-color={isLive
    ? 'color-mix(in oklch, var(--ok) 30%, var(--border-hair))'
    : 'var(--border-hair)'}
  style:box-shadow={isLive
    ? '0 0 0 1px color-mix(in oklch, var(--ok) 18%, transparent), 0 10px 30px -16px color-mix(in oklch, var(--ok) 30%, transparent)'
    : 'none'}
>
  <!-- Header -->
  <div class="tile-head">
    <div class="row tile-head-left">
      <span class="dot" class:dot--live={isLive} class:dot--idle={!isLive}></span>
      <span class="tile-project">{projectName}</span>
      <span class="mono dim tile-id">{shortId}</span>
    </div>
    <div class="row tile-head-right">
      <span class="pill" class:pill-claude={thread.cli === 'claude'} class:pill-codex={thread.cli === 'codex'}>{thread.cli}</span>
      <span class="mono dim tile-ago">{lastAgo}</span>
      <button
        class="eye-btn"
        onclick={() => toggleHidden(thread.session_id)}
        title={isHidden ? 'Show this session' : 'Hide this session'}
        aria-label={isHidden ? 'Show this session' : 'Hide this session'}
      >
        {#if isHidden}
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
            <path d="M2 2l12 12M6.5 6.7A3 3 0 0 0 9.3 9.5M4 4.3C2.8 5.2 2 6.5 2 8c0 0 2 5 6 5a6.7 6.7 0 0 0 3.7-1.2M7 3.1C7.3 3 7.7 3 8 3c4 0 6 5 6 5a10.5 10.5 0 0 1-1.5 2.4"/>
          </svg>
        {:else}
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
            <path d="M2 8s2-5 6-5 6 5 6 5-2 5-6 5-6-5-6-5Z"/>
            <circle cx="8" cy="8" r="2"/>
          </svg>
        {/if}
      </button>
    </div>
  </div>

  <!-- Meta row: branch · msgs · tokens · (no ctx_pct: not in CockpitThread) -->
  <div class="tile-meta mono">
    <span class="row tile-branch">
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="4" cy="4" r="1.4"/><circle cx="4" cy="12" r="1.4"/><circle cx="12" cy="6" r="1.4"/>
        <path d="M4 5.5v5M4.7 11.6c4-1 7.3-2.4 7.3-5"/>
      </svg>
      <span class="tile-branch-name">{thread.git_branch || '—'}</span>
    </span>
    <span class="muted">{thread.msg_count} · ↓ {kfmt(thread.tokens_out)}</span>
  </div>

  <!-- Tail messages (last 3 conversational) -->
  <div class="tile-tail">
    {#each tail as m (m.id)}
      <div class="tail-msg">
        <div class="row tail-msg-head">
          <span class="kicker" style:color="var(--accent)">{m.role}</span>
          <span class="mono dim tail-ago">{relAgo(tickMs - m.ts)}</span>
        </div>
        <p class="tail-content">{m.content}</p>
      </div>
    {/each}
    {#if tail.length === 0}
      <div class="tail-empty mono">no recent turns</div>
    {/if}
  </div>

  <!-- Footer -->
  <footer class="tile-foot">
    <button
      bind:this={expandBtn}
      class="k-btn k-btn--ghost tile-expand-btn"
      onclick={openExpand}
      title="Expand thread"
      aria-label="Expand thread"
    >
      <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
        <path d="M3 4h10M3 8h10M3 12h7"/>
      </svg>
      <span>expand{hiddenCount > 0 ? ` · +${hiddenCount}` : ''}</span>
    </button>
    <span class="mono dim tile-state">{isLive ? 'streaming' : 'idle'} · {lastAgo} ago</span>
    <div class="row" style:gap="6px">
      {#if onFocus}
        <button class="k-btn tile-focus-btn" onclick={openFocus}>focus</button>
      {/if}
      <button class="k-btn tile-open-btn" onclick={openDrawer}>open ›</button>
    </div>
  </footer>
</div>

{#if expandOpen}
  <ThreadPeekModal
    thread={thread}
    messages={recent}
    origin={expandOrigin}
    tickMs={tickMs}
    onClose={() => (expandOpen = false)}
  />
{/if}

<style>
  .live-tile {
    overflow: hidden;
    display: flex;
    flex-direction: column;
    transition: border-color .15s, box-shadow .15s;
  }

  /* Header */
  .tile-head {
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-hair);
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 0;
  }
  .tile-head-left {
    gap: 8px;
    min-width: 0;
    flex: 1;
    overflow: hidden;
  }
  .tile-head-right {
    gap: 6px;
    flex-shrink: 0;
  }
  .tile-project {
    font-size: 13.5px;
    color: var(--fg);
    font-weight: 500;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .tile-id {
    font-size: 11px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }
  .tile-ago {
    font-size: 11px;
    white-space: nowrap;
  }

  /* Meta */
  .tile-meta {
    padding: 6px 14px;
    display: flex;
    gap: 12px;
    font-size: 11px;
    border-bottom: 1px solid var(--border-hair);
  }
  .tile-branch {
    gap: 6px;
    min-width: 0;
    flex: 1;
    color: var(--fg-muted, var(--fg-soft));
  }
  .tile-branch-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* Tail */
  .tile-tail {
    padding: 10px 14px;
    display: flex;
    flex-direction: column;
    gap: 10px;
    flex: 1;
    overflow: hidden;
    max-height: 280px;
  }
  .tail-msg {
    border-left: 2px solid color-mix(in oklch, var(--accent) 30%, transparent);
    padding-left: 10px;
  }
  .tail-msg-head {
    gap: 8px;
    margin-bottom: 2px;
  }
  .tail-ago {
    font-size: 10.5px;
  }
  .tail-content {
    margin: 0;
    font-size: 12.5px;
    color: var(--fg-soft);
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .tail-empty {
    font-size: 11px;
    color: var(--fg-muted, var(--fg-soft));
    font-style: italic;
  }

  /* Footer */
  .tile-foot {
    padding: 8px 14px;
    border-top: 1px solid var(--border-hair);
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--bg-card-2, var(--bg-inset));
    gap: 8px;
  }
  .tile-state {
    font-size: 10.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    text-align: center;
  }
  .tile-expand-btn {
    padding: 4px 8px;
    color: var(--fg-muted);
    border-color: transparent;
    background: transparent;
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
  }
  .tile-expand-btn:hover { color: var(--fg); }
  .tile-open-btn {
    padding: 4px 10px;
    flex-shrink: 0;
  }
  .tile-focus-btn {
    padding: 4px 10px;
    flex-shrink: 0;
  }

  /* Eye toggle */
  .eye-btn {
    background: transparent;
    border: none;
    cursor: pointer;
    color: var(--fg-muted, var(--fg-soft));
    padding: 2px 4px;
    display: flex;
    align-items: center;
    border-radius: 4px;
    line-height: 1;
  }
  .eye-btn:hover {
    color: var(--fg);
    background: var(--bg-card-2, var(--bg-inset));
  }

  /* Pill variants — scoped to this component; no :global() leak */
  .pill-claude {
    background: color-mix(in oklch, var(--accent) 15%, transparent);
    color: var(--accent);
    border-color: color-mix(in oklch, var(--accent) 25%, transparent);
  }
  .pill-codex {
    background: color-mix(in oklch, var(--ok) 15%, transparent);
    color: var(--ok);
    border-color: color-mix(in oklch, var(--ok) 25%, transparent);
  }
</style>
