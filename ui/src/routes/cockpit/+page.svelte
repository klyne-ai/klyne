<script lang="ts">
  /**
   * /cockpit — situational-awareness view across all currently-running
   * sessions.
   *
   * A "tile" maps to a single session_id. The most-recent git_branch is
   * surfaced as informational metadata on the tile (so users can see
   * "where did I last touch this") but is NOT used as a separator —
   * users want one entry-point per session regardless of how many
   * worktrees they cycled through.
   *
   * Read-only by design — see the brief for why a reply box would cross
   * the §17/§18 line.
   */
  import { onMount, onDestroy } from 'svelte';
  import { fade, scale } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  import { goto } from '$app/navigation';
  import { fetchCockpitThreads, fetchMessages } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import { relAgo, kfmt } from '$lib/format.js';
  import { renderMarkdown } from '$lib/markdown.js';
  import type { CockpitThread, Message, MsgNew, CLI } from '$lib/types.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';

  type Tile = {
    /** Stable key: session_id (one tile per session). */
    key: string;
    thread: CockpitThread;
    /** Most recent N previewable messages, oldest → newest. The tile body
     *  scrolls; default scroll position is the bottom so the latest reply
     *  is visible without interaction. */
    recent: Message[];
  };

  const TAIL_LIMIT = 10;
  /** Sliding window for the threads endpoint. Anything older than 7d
   *  rarely matches a "what was I just doing" mental query. */
  const SINCE_WINDOW_MS = 7 * 86_400_000;

  /** Tile key: session_id only. One running session = one tile,
   *  regardless of which branch / worktree it was last touched from.
   *  The tile still surfaces the most-recent branch as informational
   *  metadata, but branch is no longer a separator — users wanted a
   *  single point of entry per session. */
  function tileKey(t: Pick<CockpitThread, 'session_id'>): string {
    return t.session_id;
  }

  // Map keyed by tile composite key so SSE events can patch one tile in
  // place without rebuilding the whole list.
  let tiles = $state<Record<string, Tile>>({});
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let showIdle = $state(false);
  let tick = $state(Date.now());

  /** Threads with at least one message in the last ACTIVE_MS qualify as
   *  "live"; older ones are folded behind the toggle. 30 minutes is wide
   *  enough that a session you stepped away from for coffee still shows
   *  up on return. */
  const ACTIVE_MS = 30 * 60_000;

  /** Stable display key for sorting: project name, then session id.
   *  Alphabetical order keeps tiles locked in place while messages stream
   *  — sorting by last_msg_at made every reply yank tiles to the top
   *  every few seconds, which is unreadable at 6+ live tiles. */
  function sortKey(t: Tile): string {
    const proj = (t.thread.project_path.split('/').filter(Boolean).pop() ?? '').toLowerCase();
    return `${proj} ${t.thread.session_id}`;
  }

  const sortedTiles = $derived.by(() => {
    const all = Object.values(tiles);
    const filtered = showIdle
      ? all
      : all.filter((t) => tick - t.thread.last_msg_at < ACTIVE_MS);
    return filtered.sort((a, b) => sortKey(a).localeCompare(sortKey(b)));
  });

  const activeCount = $derived(
    Object.values(tiles).filter((t) => tick - t.thread.last_msg_at < ACTIVE_MS).length
  );
  const idleCount = $derived(Object.keys(tiles).length - activeCount);

  /** Filter: skip tool/system messages and assistant turns that are pure
   *  tool_use with no prose — they make terrible previews. */
  function isPreviewable(m: Message): boolean {
    if (m.role === 'tool' || m.role === 'system') return false;
    const hasText = (m.content?.trim().length ?? 0) > 0;
    if (m.role === 'assistant' && !hasText) return false;
    return hasText;
  }

  async function loadTile(thread: CockpitThread): Promise<void> {
    const key = tileKey(thread);
    try {
      // Fetch the tail across the whole session — no branch filter, since
      // tiles are now session-scoped. Tool-subshell cwds are still
      // ignored (we never filtered on those).
      const resp = await fetchMessages(thread.session_id, {
        limit: 50,
        order: 'desc',
      });
      const desc = resp.messages.filter(isPreviewable);
      const tail = desc.slice(0, TAIL_LIMIT).reverse();
      tiles = { ...tiles, [key]: { key, thread, recent: tail } };
    } catch {
      tiles = { ...tiles, [key]: { key, thread, recent: [] } };
    }
  }

  async function loadAll(): Promise<void> {
    // Only show the loading skeleton on the very first fetch — subsequent
    // periodic refreshes keep the existing grid visible to avoid an
    // empty-grid flash every 60s.
    const isFirstLoad = Object.keys(tiles).length === 0;
    if (isFirstLoad) loading = true;
    loadError = null;
    try {
      const since = Date.now() - SINCE_WINDOW_MS;
      const resp = await fetchCockpitThreads({ since, limit: 100 });
      const seen = new Set<string>();
      // Merge: load each thread that came back. We DON'T blow away the
      // tiles map first — that would cause a flash on every periodic
      // refresh. Instead we record what came back, drop anything stale
      // at the end, and let loadTile patch in place.
      for (const t of resp.threads) seen.add(tileKey(t));
      await Promise.all(resp.threads.map(loadTile));
      // Drop tiles that have fallen completely outside the response.
      // They've either been deleted, fallen past the 7-day window, or
      // pushed past the 100-thread cap. Either way, the user won't
      // miss them — and keeping them around forever leaks memory.
      const next = { ...tiles };
      let pruned = false;
      for (const k of Object.keys(next)) {
        if (!seen.has(k)) {
          delete next[k];
          pruned = true;
        }
      }
      if (pruned) tiles = next;
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load cockpit threads';
    } finally {
      if (isFirstLoad) loading = false;
    }
  }

  /** SSE arrival: schedule a refetch for the affected session.
   *
   *  We deliberately do NOT optimistically bump last_msg_at on existing
   *  tiles here. With alphabetical sort the bump bought us nothing
   *  visually, and the server-authoritative thread row overwrites it
   *  800ms later anyway.
   *
   *  Tiles also are NEVER deleted by this handler — the display filter
   *  (live vs idle vs hidden) works off last_msg_at + tick alone, and
   *  the periodic loadAll() prunes anything stale. Aggressive deletes
   *  here used to race with concurrent loadTile() calls and yank
   *  newly-discovered tiles out from under the user. */
  const refetchTimers: Record<string, ReturnType<typeof setTimeout>> = {};

  function bumpAndRefresh(sessionId: string): void {
    if (refetchTimers[sessionId]) return;
    refetchTimers[sessionId] = setTimeout(async () => {
      delete refetchTimers[sessionId];
      try {
        const since = Date.now() - SINCE_WINDOW_MS;
        const resp = await fetchCockpitThreads({ since, limit: 100 });
        await Promise.all(
          resp.threads
            .filter((t) => t.session_id === sessionId)
            .map(loadTile)
        );
      } catch {
        // Next periodic loadAll will recover.
      }
    }, 800);
  }

  function onMsgNew(ev: MsgNew): void {
    bumpAndRefresh(ev.session_id);
  }

  let unsubscribe: (() => void) | null = null;
  let tickHandle: ReturnType<typeof setInterval> | null = null;
  let periodicHandle: ReturnType<typeof setInterval> | null = null;

  onMount(() => {
    void loadAll();
    unsubscribe = subscribe({ onMsgNew });
    tickHandle = setInterval(() => { tick = Date.now(); }, 5_000);
    // Periodic full refresh: catches new buckets for sessions that
    // never fired SSE during this page's lifetime (e.g. a brand-new
    // session started in another terminal). 60s is rare enough not
    // to matter performance-wise but tight enough that a "missed"
    // thread surfaces within a minute.
    periodicHandle = setInterval(() => { void loadAll(); }, 60_000);
    window.addEventListener('keydown', onWindowKey);
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
    if (tickHandle !== null) clearInterval(tickHandle);
    if (periodicHandle !== null) clearInterval(periodicHandle);
    for (const k of Object.keys(refetchTimers)) {
      clearTimeout(refetchTimers[k]);
      delete refetchTimers[k];
    }
    window.removeEventListener('keydown', onWindowKey);
  });

  function projectName(path: string): string {
    return path.split('/').filter(Boolean).pop() ?? path;
  }

  /** Tile labels show the project name only — sub-folder cwds were a
   *  red herring (they came from tool subshells, not real terminals).
   *  Kept as a no-op stub so existing call sites compile; will be
   *  inlined out in a follow-up sweep. */
  function cwdSuffix(_thread: CockpitThread): string {
    return '';
  }

  /** Truncate to roughly N lines worth of monospace text. The fixed-height
   *  tile body uses CSS line-clamp for the visual cut; this just bounds
   *  the DOM payload so a giant tool-result paste doesn't bloat memory. */
  function preview(content: string, maxChars = 600): string {
    const trimmed = content.trim();
    if (trimmed.length <= maxChars) return trimmed;
    return trimmed.slice(0, maxChars) + '…';
  }

  function isLive(t: Tile): boolean {
    return tick - t.thread.last_msg_at < 60_000;
  }

  /** Build a paste-and-go resume command. Just copying the session id
   *  doesn't work because Claude Code resolves --resume against the
   *  current working directory; if the user runs it from $HOME they get
   *  "No conversation found". Always cd's to the project root — the
   *  directory the user originally ran `claude` from. */
  function resumeCommand(t: CockpitThread): string {
    const cli = t.cli === 'codex' ? 'codex' : 'claude';
    const safePath = t.project_path.replace(/'/g, `'\\''`);
    return `cd '${safePath}' && ${cli} --resume ${t.session_id}`;
  }

  let copiedKey = $state<string | null>(null);

  function copyResume(e: MouseEvent, t: Tile): void {
    e.stopPropagation();
    void navigator.clipboard.writeText(resumeCommand(t.thread));
    copiedKey = t.key;
    setTimeout(() => { if (copiedKey === t.key) copiedKey = null; }, 1200);
  }

  // ── Expanded-tile modal ─────────────────────────────────────────────
  // Click ⤢ on any tile to open it in a roomier viewport. The modal
  // shares the tile's `recent[]` slice (no extra fetch on open — fast)
  // but renders all messages in full + with markdown. The selected
  // tile object is read live from `tiles[expandedKey]` so SSE patches
  // flow into the modal automatically.

  let expandedKey = $state<string | null>(null);

  const expandedTile = $derived(expandedKey ? (tiles[expandedKey] ?? null) : null);

  function openExpand(e: Event, key: string): void {
    e.stopPropagation();
    expandedKey = key;
  }

  function closeExpand(): void {
    expandedKey = null;
  }

  // Global ESC handler so the modal stays keyboard-friendly without
  // putting focus traps in front of the user.
  function onWindowKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && expandedKey !== null) {
      e.preventDefault();
      closeExpand();
    }
  }

  /** Svelte action that pins the scroll viewport to the bottom whenever
   *  the message list changes, so the most recent reply is always
   *  visible on first paint and after each SSE refresh. The user can
   *  still scroll up to read older context — manual scrolls aren't
   *  fought, only programmatic re-renders re-pin. */
  function autoScrollBottom(node: HTMLElement, msgs: Message[]) {
    const apply = () => {
      requestAnimationFrame(() => {
        node.scrollTop = node.scrollHeight;
      });
    };
    apply();
    let last = msgs;
    return {
      update(next: Message[]) {
        const lastId = last[last.length - 1]?.id;
        const nextId = next[next.length - 1]?.id;
        if (lastId !== nextId) apply();
        last = next;
      },
    };
  }
</script>

<svelte:head><title>Cockpit — agentdeck</title></svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1600px;">
  <!-- Header -->
  <div style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 4px;">
    <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0;">
      Cockpit
      {#if !loading}
        <span class="ad-faint" style="font-weight: 400; font-size: 14px; margin-left: 10px;">
          {activeCount} live
          {#if idleCount > 0}<span style="color: var(--ad-muted);">· {idleCount} idle</span>{/if}
        </span>
      {/if}
    </h1>
    <div style="display: flex; align-items: center; gap: 8px;">
      <label style="display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--ad-muted); cursor: pointer; user-select: none;">
        <input type="checkbox" bind:checked={showIdle} />
        show idle
      </label>
      <span class="ad-mono ad-faint" style="font-size: 11px;">via SSE · sorted A→Z · idle &gt; 30m hidden</span>
    </div>
  </div>
  <p class="ad-muted" style="margin-top: 4px; margin-bottom: 20px; font-size: 13px;">
    Every parallel sub-thread active in the last 30 minutes — sorted alphabetically so tiles stay put while messages stream in.
  </p>

  {#if loadError}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error);">{loadError}</div>
  {:else if loading}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">Loading threads…</div>
  {:else if sortedTiles.length === 0}
    <div class="ad-card" style="padding: 32px; text-align: center; color: var(--ad-muted); font-size: 13px;">
      No active sub-threads in the last 30 minutes.
      {#if idleCount > 0}
        <div style="margin-top: 8px;">
          <button class="ad-btn ad-btn--ghost ad-btn--sm" onclick={() => (showIdle = true)}>
            Show {idleCount} idle thread{idleCount > 1 ? 's' : ''}
          </button>
        </div>
      {/if}
    </div>
  {:else}
    <!-- Tile grid: auto-fit means 1 column at narrow widths, up to 5 at 1920px. -->
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 12px;">
      {#each sortedTiles as t (t.key)}
        {@const live = isLive(t)}
        {@const ageMs = tick - t.thread.last_msg_at}
        {@const stale = ageMs > ACTIVE_MS}
        {@const subdir = cwdSuffix(t.thread)}
        <div
          class="ad-card"
          role="button"
          tabindex="0"
          onclick={() => goto(`/sessions/${encodeURIComponent(t.thread.session_id)}`)}
          onkeydown={(e) => { if (e.key === 'Enter') goto(`/sessions/${encodeURIComponent(t.thread.session_id)}`); }}
          style="
            padding: 0;
            cursor: pointer;
            display: flex;
            flex-direction: column;
            transition: background 80ms, opacity 200ms, border-color 200ms;
            opacity: {stale ? 0.55 : 1};
            border-color: {live ? 'color-mix(in oklch, var(--ad-active) 50%, var(--ad-border))' : 'var(--ad-border)'};
            min-height: 240px;
          "
          onmouseenter={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel-hi)')}
          onmouseleave={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel)')}
        >
          <!-- Tile header: project name with optional branch/subdir suffix -->
          <div style="padding: 10px 12px 8px; border-bottom: 1px solid var(--ad-border-soft); display: flex; align-items: center; gap: 8px; min-width: 0;">
            <span class="ad-dot ad-dot--{live ? 'active' : 'idle'}"></span>
            <div style="flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px;">
              <div style="display: flex; align-items: center; gap: 6px; min-width: 0;">
                <span style="font-weight: 600; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title={t.thread.cwd || t.thread.project_path}>
                  {projectName(t.thread.project_path)}{subdir}
                </span>
              </div>
              {#if t.thread.git_branch}
                <span class="ad-mono" style="font-size: 10px; color: var(--ad-faint); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="git branch at the time of writing">
                   {t.thread.git_branch}
                </span>
              {/if}
            </div>
            <CliBadge cli={t.thread.cli as CLI} />
            <span class="ad-mono ad-faint" style="font-size: 10px; white-space: nowrap;">
              {relAgo(ageMs)}
            </span>
          </div>

          <!-- Tile body: scrollable thread of last 10 previewable messages.
               Click events on the inner scroll panel stop propagation so
               the user can scroll without accidentally navigating to the
               full session view. -->
          <div
            class="cockpit-thread"
            use:autoScrollBottom={t.recent}
            onclick={(e) => e.stopPropagation()}
            onkeydown={(e) => e.stopPropagation()}
            role="presentation"
            style="
              padding: 8px 12px;
              flex: 1;
              overflow-y: auto;
              display: flex;
              flex-direction: column;
              gap: 8px;
              font-size: 12px;
              line-height: 1.45;
              min-width: 0;
              max-height: 280px;
              scroll-behavior: smooth;
            "
          >
            {#if t.recent.length === 0}
              <div style="color: var(--ad-faint); font-style: italic; font-size: 11px;">No previewable messages yet.</div>
            {:else}
              {#each t.recent as m, mi (m.id)}
                {@const isUser = m.role === 'user'}
                {@const isLast = mi === t.recent.length - 1}
                <div style="min-width: 0;">
                  <div
                    style="
                      font-size: 10px;
                      color: {isUser ? 'var(--ad-faint)' : 'var(--ad-claude, var(--ad-active))'};
                      text-transform: uppercase;
                      letter-spacing: 0.06em;
                      margin-bottom: 2px;
                      display: flex; gap: 6px; align-items: baseline;
                    "
                  >
                    <span>{isUser ? 'you' : t.thread.cli}</span>
                    <span class="ad-mono" style="font-size: 9px; color: var(--ad-faint); text-transform: none; letter-spacing: 0;">{relAgo(tick - m.ts)}</span>
                    {#if isLast}<span class="ad-mono" style="font-size: 9px; color: var(--ad-active); text-transform: none; letter-spacing: 0;">latest</span>{/if}
                  </div>
                  <!-- Older messages get hard-clipped to keep the tile
                       light; the most recent message renders in full so
                       the user can read the whole reply without clicking
                       through. The wrapping container scrolls anyway.
                       Both flavours run through the markdown renderer so
                       backticks / lists / headings render properly
                       instead of leaking source like `**bold**`. -->
                  <div
                    class="cockpit-md {isUser ? 'cockpit-md--user' : 'cockpit-md--ai'}"
                    style="color: {isUser ? 'var(--ad-fg-2)' : 'var(--ad-fg)'}; word-break: break-word;"
                  >{@html renderMarkdown(isLast ? m.content.trim() : preview(m.content, 400))}</div>
                </div>
              {/each}
            {/if}
          </div>

          <!-- Tile footer: model, msg count, expand, copy-resume -->
          <div style="padding: 6px 12px 8px; border-top: 1px solid var(--ad-border-soft); display: flex; align-items: center; gap: 8px; font-size: 10px; color: var(--ad-faint); min-width: 0;">
            <span class="ad-mono" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0;">{t.thread.model || '—'}</span>
            <span class="ad-mono ad-tnum">{t.thread.msg_count} msgs</span>
            <span class="ad-mono ad-tnum">↓ {kfmt(t.thread.tokens_out)}</span>
            <button
              type="button"
              onclick={(e) => openExpand(e, t.key)}
              title="Expand thread (full markdown view)"
              aria-label="Expand thread"
              style="background: transparent; border: 0; color: var(--ad-faint); cursor: pointer; padding: 2px 6px; font-size: 10px; font-family: var(--ad-font-mono);"
              onmouseenter={(e) => ((e.currentTarget as HTMLElement).style.color = 'var(--ad-fg)')}
              onmouseleave={(e) => ((e.currentTarget as HTMLElement).style.color = 'var(--ad-faint)')}
            >expand</button>
            <button
              type="button"
              onclick={(e) => copyResume(e, t)}
              title={`Copy: ${resumeCommand(t.thread)}`}
              aria-label="Copy resume command"
              style="background: transparent; border: 0; color: {copiedKey === t.key ? 'var(--ad-active)' : 'var(--ad-faint)'}; cursor: pointer; padding: 2px 6px; font-size: 10px; font-family: var(--ad-font-mono);"
              onmouseenter={(e) => { if (copiedKey !== t.key) (e.currentTarget as HTMLElement).style.color = 'var(--ad-fg)'; }}
              onmouseleave={(e) => { if (copiedKey !== t.key) (e.currentTarget as HTMLElement).style.color = 'var(--ad-faint)'; }}
            >{copiedKey === t.key ? '✓ copied' : '⎘ resume'}</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<!-- ── Expanded-tile modal ─────────────────────────────────────────────
     Two transitions stack: backdrop fades, card scales-and-fades. Both
     use cubicOut so the open feels confident rather than springy.
     Uses the live `expandedTile` derived value so SSE updates flow
     into the modal without re-opening it. -->
{#if expandedTile}
  {@const t = expandedTile}
  {@const subdir = cwdSuffix(t.thread)}
  <div
    class="cockpit-modal-backdrop"
    transition:fade={{ duration: 160, easing: cubicOut }}
    onclick={closeExpand}
    onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') closeExpand(); }}
    role="presentation"
  >
    <div
      class="cockpit-modal-card ad-card"
      transition:scale={{ duration: 220, start: 0.96, opacity: 0, easing: cubicOut }}
      role="dialog"
      aria-modal="true"
      aria-label="Expanded thread for {projectName(t.thread.project_path)}"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
      tabindex="-1"
    >
      <!-- Header -->
      <div class="cockpit-modal-header">
        <span class="ad-dot ad-dot--{isLive(t) ? 'active' : 'idle'}"></span>
        <div style="flex: 1; min-width: 0;">
          <div style="font-size: 14px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title={t.thread.cwd || t.thread.project_path}>
            {projectName(t.thread.project_path)}{subdir}
          </div>
          {#if t.thread.git_branch}
            <div class="ad-mono" style="font-size: 11px; color: var(--ad-faint); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
               {t.thread.git_branch}
            </div>
          {/if}
        </div>
        <CliBadge cli={t.thread.cli as CLI} />
        <span class="ad-mono ad-faint" style="font-size: 11px; white-space: nowrap;">
          {relAgo(tick - t.thread.last_msg_at)}
        </span>
        <button
          type="button"
          class="cockpit-modal-close"
          onclick={closeExpand}
          aria-label="Close"
          title="Close (Esc)"
        >✕</button>
      </div>

      <!-- Body: full markdown for every message in t.recent. The
           autoScrollBottom action pins to the latest, same as the tile,
           so opening lands on the most recent reply. -->
      <div
        class="cockpit-md cockpit-modal-body"
        use:autoScrollBottom={t.recent}
      >
        {#if t.recent.length === 0}
          <div style="color: var(--ad-faint); font-style: italic; font-size: 12px;">No previewable messages yet.</div>
        {:else}
          {#each t.recent as m, mi (m.id)}
            {@const isUser = m.role === 'user'}
            {@const isLast = mi === t.recent.length - 1}
            <div class="cockpit-modal-msg">
              <div class="cockpit-modal-msg-head">
                <span style="color: {isUser ? 'var(--ad-faint)' : 'var(--ad-claude, var(--ad-active))'}; font-weight: 600;">
                  {isUser ? 'you' : t.thread.cli}
                </span>
                <span class="ad-mono ad-faint">{relAgo(tick - m.ts)}</span>
                {#if isLast}<span class="ad-mono" style="color: var(--ad-active); font-size: 10px;">latest</span>{/if}
              </div>
              <div
                class="cockpit-modal-msg-body"
                style="color: {isUser ? 'var(--ad-fg-2)' : 'var(--ad-fg)'};"
              >{@html renderMarkdown(m.content.trim())}</div>
            </div>
          {/each}
        {/if}
      </div>

      <!-- Footer: model + counters + actions -->
      <div class="cockpit-modal-footer">
        <span class="ad-mono" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0;">{t.thread.model || '—'}</span>
        <span class="ad-mono ad-tnum">{t.thread.msg_count} msgs</span>
        <span class="ad-mono ad-tnum">↓ {kfmt(t.thread.tokens_out)}</span>
        <button
          type="button"
          class="ad-btn ad-btn--ghost ad-btn--sm"
          onclick={(e) => copyResume(e, t)}
          title={`Copy: ${resumeCommand(t.thread)}`}
        >{copiedKey === t.key ? '✓ copied' : '⎘ resume cmd'}</button>
        <button
          type="button"
          class="ad-btn ad-btn--primary ad-btn--sm"
          onclick={() => goto(`/sessions/${encodeURIComponent(t.thread.session_id)}`)}
        >Open full thread →</button>
      </div>
    </div>
  </div>
{/if}

<!--
  Scoped styles for the rendered-markdown blocks inside each tile. We use
  :global() because the HTML is injected via {@html ...} from
  renderMarkdown(), so Svelte's scoping pass never sees those tags and
  would otherwise strip class hooks.

  Goals:
    - tight vertical rhythm (default <p> margins waste tile real estate)
    - readable code blocks without overflow
    - links visible but not loud
-->
<style>
  .cockpit-md :global(p)   { margin: 0 0 0.4em; }
  .cockpit-md :global(p:last-child) { margin-bottom: 0; }
  .cockpit-md :global(ul),
  .cockpit-md :global(ol)  { margin: 0.2em 0 0.4em; padding-left: 1.2em; }
  .cockpit-md :global(li)  { margin: 0.1em 0; }
  .cockpit-md :global(h1),
  .cockpit-md :global(h2),
  .cockpit-md :global(h3),
  .cockpit-md :global(h4)  { font-size: 13px; font-weight: 600; margin: 0.5em 0 0.3em; line-height: 1.3; }
  .cockpit-md :global(code) {
    font-family: var(--ad-font-mono);
    font-size: 0.92em;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 3px;
    padding: 0 4px;
  }
  .cockpit-md :global(pre) {
    margin: 0.4em 0;
    padding: 8px 10px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 4px;
    overflow-x: auto;
    font-size: 11px;
    line-height: 1.4;
  }
  .cockpit-md :global(pre code) {
    background: transparent;
    border: 0;
    padding: 0;
    font-size: inherit;
  }
  .cockpit-md :global(blockquote) {
    margin: 0.4em 0;
    padding: 0 0 0 10px;
    border-left: 2px solid var(--ad-border);
    color: var(--ad-fg-2);
  }
  .cockpit-md :global(a) {
    color: var(--ad-claude, var(--ad-active));
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .cockpit-md :global(strong) { font-weight: 600; }
  .cockpit-md :global(em)     { font-style: italic; }
  .cockpit-md :global(hr) {
    border: 0;
    border-top: 1px solid var(--ad-border-soft);
    margin: 0.6em 0;
  }
  .cockpit-md :global(table)  {
    border-collapse: collapse;
    margin: 0.4em 0;
    font-size: 11px;
  }
  .cockpit-md :global(th),
  .cockpit-md :global(td) {
    border: 1px solid var(--ad-border-soft);
    padding: 3px 6px;
    text-align: left;
  }

  /* ── Expanded modal ──────────────────────────────────────────────
     Backdrop covers the viewport; card centers and caps at sensible
     dimensions. Body scrolls; header/footer stay sticky to the card.
     The card uses display:flex column so the body's flex:1 collapses
     into available height regardless of message count. */
  .cockpit-modal-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklch, var(--ad-bg, #000) 75%, transparent);
    backdrop-filter: blur(2px);
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }
  .cockpit-modal-card {
    width: min(880px, 100%);
    max-height: min(80vh, 900px);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.45);
  }
  .cockpit-modal-header {
    padding: 14px 16px 12px;
    border-bottom: 1px solid var(--ad-border-soft);
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }
  .cockpit-modal-close {
    background: transparent;
    border: 0;
    color: var(--ad-faint);
    cursor: pointer;
    font-size: 14px;
    padding: 4px 8px;
    border-radius: 4px;
  }
  .cockpit-modal-close:hover { color: var(--ad-fg); background: var(--ad-bg-2); }
  .cockpit-modal-body {
    flex: 1 1 auto;
    overflow-y: auto;
    padding: 16px 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
    font-size: 13px;
    line-height: 1.55;
    scroll-behavior: smooth;
  }
  .cockpit-modal-msg {
    border-bottom: 1px solid var(--ad-border-soft);
    padding-bottom: 12px;
  }
  .cockpit-modal-msg:last-child { border-bottom: 0; padding-bottom: 0; }
  .cockpit-modal-msg-head {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin-bottom: 6px;
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .cockpit-modal-msg-body {
    word-break: break-word;
  }
  .cockpit-modal-footer {
    padding: 10px 16px;
    border-top: 1px solid var(--ad-border-soft);
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 11px;
    color: var(--ad-faint);
    flex-shrink: 0;
  }
</style>
