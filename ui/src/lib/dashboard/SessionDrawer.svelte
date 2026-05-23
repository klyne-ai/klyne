<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { subscribe } from '$lib/sse.js';
  import {
    fetchSession,
    fetchMessages,
    fetchSummary,
  } from '$lib/api.js';
  import { kfmt, relAgo, costFmt } from '$lib/format.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';
  import { advisorsStore } from '$lib/advisors.svelte.js';
  import type { Session, Message, SummaryResponse } from '$lib/types.js';
  import Icon from './Icon.svelte';
  import TokenTimelineChart from '$lib/components/TokenTimelineChart.svelte';
  import RestoreContext from '$lib/components/RestoreContext.svelte';

  interface Props {
    sessionId: string;
    onClose: () => void;
  }
  const { sessionId, onClose }: Props = $props();

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------
  let session = $state<Session | null>(null);
  let messages = $state<Message[]>([]);
  let summary = $state<SummaryResponse | null>(null);
  let loadError = $state<string | null>(null);
  let copied = $state(false);
  let showRestore = $state(false);
  let expandedIds = $state<Set<string>>(new Set());
  let closeBtn = $state<HTMLButtonElement | null>(null);

  // ---------------------------------------------------------------------------
  // Derived
  // ---------------------------------------------------------------------------
  const isCompacted = $derived(session?.status === 'compacted');
  const projectName = $derived(
    session
      ? (session.project_path.split('/').filter(Boolean).pop() ?? session.project_path)
      : ''
  );

  const visibleMessages = $derived(messages.filter(isConversationalMessage));
  const hiddenCount = $derived(messages.length - visibleMessages.length);

  /** Advisories that fired in this session */
  const sessionAdvisories = $derived(
    advisorsStore.advisories.filter((a) => a.session_id === sessionId)
  );

  const cachedRead = $derived(session?.cached_read_tokens ?? 0);
  const cachedWrite = $derived(session?.cached_write_tokens ?? 0);
  const freshIn = $derived(Math.max(0, (session?.tokens_in ?? 0) - cachedRead - cachedWrite));
  const cachedPct = $derived(
    (session?.tokens_in ?? 0) > 0
      ? Math.round(((cachedRead + cachedWrite) / session!.tokens_in) * 100)
      : 0
  );

  function resumeCommand(): string {
    if (!session) return `claude --resume ${sessionId}`;
    const cli = session.cli === 'codex' ? 'codex' : 'claude';
    const safePath = session.project_path.replace(/'/g, `'\\''`);
    return `cd '${safePath}' && ${cli} --resume ${session.id}`;
  }

  async function copyResumeCmd(): Promise<void> {
    await navigator.clipboard.writeText(resumeCommand()).catch(() => {});
    copied = true;
    setTimeout(() => { copied = false; }, 1200);
  }

  function isLong(text: string): boolean {
    if (!text) return false;
    const newlineCount = (text.match(/\n/g) || []).length;
    return newlineCount >= 6 || text.length >= 480;
  }

  function toggleExpanded(id: string): void {
    const next = new Set(expandedIds);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expandedIds = next;
  }

  // ---------------------------------------------------------------------------
  // Data loading
  // ---------------------------------------------------------------------------
  function newestPageInDisplayOrder(page: Message[]): Message[] {
    return [...page].reverse();
  }

  function sortMessagesForDisplay(page: Message[]): Message[] {
    return [...page].sort((a, b) => (a.ts - b.ts) || a.id.localeCompare(b.id));
  }

  async function loadData(id: string): Promise<void> {
    if (!id) return;
    loadError = null;
    try {
      const [sr, mr] = await Promise.all([
        fetchSession(id),
        fetchMessages(id, { limit: 100, order: 'desc' }),
      ]);
      session = sr.session;
      messages = newestPageInDisplayOrder(mr.messages);

      // Load summary non-fatally
      try {
        summary = await fetchSummary(id);
      } catch {
        summary = null;
      }
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load session';
    }
  }

  async function fetchNewMessages(id: string): Promise<void> {
    try {
      const res = await fetchMessages(id, { limit: 50, order: 'desc' });
      const latest = newestPageInDisplayOrder(res.messages);
      const newMsgs = latest.filter((m) => !messages.some((existing) => existing.id === m.id));
      if (newMsgs.length > 0) {
        messages = sortMessagesForDisplay([...messages, ...newMsgs]);
      }
    } catch {
      // non-fatal
    }
  }

  let unsubscribeSSE: (() => void) | null = null;

  onMount(() => {
    void loadData(sessionId);

    unsubscribeSSE = subscribe({
      onMsgNew: (payload) => {
        if (payload.session_id === sessionId) {
          void fetchNewMessages(sessionId);
        }
      },
    });

    // Focus close button for accessibility
    closeBtn?.focus();
  });

  onDestroy(() => {
    unsubscribeSSE?.();
    unsubscribeSSE = null;
  });

  $effect(() => {
    const id = sessionId;
    session = null;
    messages = [];
    summary = null;
    loadError = null;
    if (id) void loadData(id);
  });

  // Esc closes the drawer
  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') { e.preventDefault(); onClose(); }
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="drawer-scrim" onclick={onClose} role="presentation"></div>

<!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
<aside class="drawer" role="dialog" aria-modal="true" aria-label="Session detail">
  <!-- ── Header ─────────────────────────────────────────────────────────── -->
  <header class="drawer-head">
    <span class="dot {session?.status === 'active' ? 'dot--active' : session?.status === 'compacted' ? 'dot--compact' : 'dot--idle'}"></span>
    <div class="head-meta">
      <div class="head-top">
        <span class="kicker">Session</span>
        <span class="mono uuid">{sessionId}</span>
        {#if session}
          <span class="pill pill-{session.cli}">{session.cli}</span>
        {/if}
      </div>
      {#if session}
        <div class="head-sub">
          <span class="mono model">{session.model}</span>
          <span class="sep">·</span>
          <span>{projectName}</span>
          <span class="sep">·</span>
          <span>started {relAgo(Date.now() - session.started_at)}</span>
          <span class="sep">·</span>
          <span>last msg {relAgo(Date.now() - session.last_msg_at)}</span>
        </div>
      {/if}
    </div>
    <button
      class="x"
      onclick={onClose}
      aria-label="Close session drawer"
      bind:this={closeBtn}
    ><Icon name="x" /></button>
  </header>

  <!-- ── Body ──────────────────────────────────────────────────────────── -->
  <div class="drawer-body">
    {#if loadError}
      <div class="error-card">{loadError}</div>

    {:else if !session}
      <div class="loading-card">Loading session…</div>

    {:else}

      <!-- 1. Stat tiles -->
      <div class="stats-grid">
        <div class="stat">
          <span class="stat-k">Messages</span>
          <span class="stat-v mono">{session.msg_count}</span>
        </div>
        <div class="stat">
          <span class="stat-k">↑ Tokens in</span>
          <span class="stat-v mono">{kfmt(session.tokens_in)}</span>
          {#if cachedRead > 0 || cachedWrite > 0}
            <span class="stat-s mono">
              fresh {kfmt(freshIn)}{cachedRead > 0 ? ` · ↻ ${kfmt(cachedRead)}` : ''} · {cachedPct}% cached
            </span>
          {/if}
        </div>
        <div class="stat">
          <span class="stat-k">↓ Tokens out</span>
          <span class="stat-v mono">{kfmt(session.tokens_out)}</span>
        </div>
        <div class="stat">
          <span class="stat-k">Cost</span>
          <span class="stat-v mono" class:faint={session.cost_usd === 0}>
            {costFmt(session.cost_usd, session.cost_usd > 0)}
          </span>
        </div>
      </div>

      <!-- 2. Token timeline chart -->
      <TokenTimelineChart sessionId={session.id} />

      <!-- 3. Restore-context banner (compacted sessions) -->
      {#if isCompacted}
        <div class="restore-banner">
          <span class="restore-icon">♻</span>
          <div class="restore-text">
            <div class="restore-title">Session was compacted</div>
            <div class="restore-body">Context window trimmed — restore the rolling summary as a resume prompt.</div>
          </div>
          <button class="restore-btn" onclick={() => { showRestore = true; }}>
            ↩ Restore context
          </button>
        </div>
      {/if}

      <!-- 4. KLYNE_SUMMARY card -->
      {#if summary?.text}
        <section class="card">
          <header class="card-head">
            <div class="title">
              KLYNE_SUMMARY
              <span class="sub">auto-extracted · v{summary.version}</span>
            </div>
          </header>
          <div class="card-body summary-text">{summary.text}</div>
        </section>
      {/if}

      <!-- 5. Advisors fired in this session -->
      {#if sessionAdvisories.length > 0}
        <section class="card">
          <header class="card-head">
            <div class="title">
              Advisors fired
              <span class="sub">{sessionAdvisories.length} event{sessionAdvisories.length === 1 ? '' : 's'}</span>
            </div>
          </header>
          <div class="advisors-list">
            {#each sessionAdvisories as adv (adv.message_id)}
              {@const isAlert = adv.kind === 'hard_ceiling' || adv.kind === 'acceleration'}
              <div class="adv-row" class:adv-alert={isAlert} class:adv-warn={!isAlert}>
                <div class="adv-top">
                  <span class="adv-kind">{adv.kind.replace(/_/g, ' ')}</span>
                  <span class="adv-ago mono">{relAgo(Date.now() - adv.ts)}</span>
                </div>
                <p class="adv-body">{adv.content}</p>
              </div>
            {/each}
          </div>
        </section>
      {/if}

      <!-- 6. Message stream -->
      <section class="card">
        <header class="card-head">
          <div class="title">
            Messages
            <span class="sub">{visibleMessages.length} shown{hiddenCount > 0 ? ` · ${hiddenCount} hidden` : ''}</span>
          </div>
        </header>
        <div class="msg-stream">
          {#each visibleMessages as m (m.id)}
            {@const isUser = m.role === 'user'}
            {@const cliColor = session.cli === 'codex' ? 'var(--fg-accent, #f59e0b)' : 'var(--fg-purple, #c084fc)'}
            {@const lab = isUser ? 'you' : (session.cli === 'codex' ? 'codex' : 'claude')}
            {@const labColor = isUser ? 'var(--fg)' : cliColor}
            {@const isExpanded = expandedIds.has(m.id)}
            {@const long = isLong(m.content)}

            <div class="msg-row">
              <div class="msg-head">
                <span class="msg-role" style="color: {labColor};">{lab}</span>
                <span class="msg-meta mono">
                  {m.tokens_out ? `↓ ${kfmt(m.tokens_out)} · ` : ''}{relAgo(Date.now() - m.ts)}
                </span>
              </div>
              <div
                class="msg-body"
                class:msg-clipped={long && !isExpanded}
              >{m.content}</div>
              {#if long}
                <button
                  class="msg-toggle"
                  onclick={() => toggleExpanded(m.id)}
                >{isExpanded ? 'Show less' : 'Show more'}</button>
              {/if}
            </div>
          {/each}
          {#if visibleMessages.length === 0}
            <div class="empty-msg">No messages loaded.</div>
          {/if}
        </div>
      </section>

      <!-- 7. Resume command -->
      <section class="card">
        <header class="card-head">
          <div class="title">Resume command</div>
          <button class="k-btn" onclick={copyResumeCmd} aria-label="Copy resume command">
            <Icon name="copy" /> {copied ? 'copied' : 'copy'}
          </button>
        </header>
        <pre class="mono resume">{resumeCommand()}</pre>
      </section>

    {/if}
  </div><!-- /drawer-body -->
</aside>

<!-- Restore-context modal -->
{#if showRestore && session}
  <RestoreContext sessionId={session.id} onclose={() => { showRestore = false; }} />
{/if}

<style>
  /* ── Scrim + slide panel ─────────────────────────────────────────────── */
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

  /* ── Header ──────────────────────────────────────────────────────────── */
  .drawer-head {
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-hair);
    display: flex; align-items: flex-start; gap: 10px;
    flex-shrink: 0;
  }
  .dot {
    width: 8px; height: 8px; border-radius: 50%;
    margin-top: 4px; flex-shrink: 0;
    background: var(--fg-muted);
  }
  .dot--active  { background: #22c55e; }
  .dot--compact { background: #a78bfa; }
  .dot--idle    { background: var(--fg-muted); }

  .head-meta { flex: 1; min-width: 0; }
  .head-top {
    display: flex; align-items: center; gap: 8px;
    margin-bottom: 3px; flex-wrap: wrap;
  }
  .kicker {
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: 0.12em; text-transform: uppercase;
    color: var(--fg-muted); flex-shrink: 0;
  }
  .uuid {
    font-family: var(--font-mono); font-size: 11px;
    color: var(--fg-soft); overflow: hidden; text-overflow: ellipsis;
    white-space: nowrap; min-width: 0;
  }
  .pill {
    font-family: var(--font-mono); font-size: 10px;
    padding: 1px 6px; border-radius: 4px;
    font-weight: 600; flex-shrink: 0;
  }
  .pill-claude { background: color-mix(in oklch, #c084fc 15%, transparent); color: #c084fc; }
  .pill-codex  { background: color-mix(in oklch, #f59e0b 15%, transparent); color: #f59e0b; }

  .head-sub {
    display: flex; flex-wrap: wrap; gap: 4px;
    font-size: 11px; color: var(--fg-muted);
    align-items: center;
  }
  .model { font-family: var(--font-mono); font-size: 10px; }
  .sep { opacity: 0.4; }

  .x {
    margin-left: auto; flex-shrink: 0;
    background: transparent; border: 1px solid var(--border-hair);
    width: 26px; height: 26px;
    border-radius: 6px;
    display: grid; place-items: center;
    color: var(--fg-muted); cursor: pointer;
  }
  .x:hover { color: var(--fg); border-color: var(--border-soft); }

  /* ── Body ────────────────────────────────────────────────────────────── */
  .drawer-body {
    flex: 1; overflow: auto;
    padding: 18px 22px 40px;
    display: flex; flex-direction: column; gap: 14px;
  }

  /* ── State cards ─────────────────────────────────────────────────────── */
  .error-card, .loading-card {
    padding: 24px; text-align: center;
    background: var(--bg-card); border: 1px solid var(--border-hair);
    border-radius: 8px; color: var(--fg-muted); font-size: 13px;
  }
  .error-card { color: var(--error, #f87171); }

  /* ── Stat grid ───────────────────────────────────────────────────────── */
  .stats-grid {
    display: grid; grid-template-columns: repeat(4, 1fr);
    background: var(--bg-card); border: 1px solid var(--border-hair);
    border-radius: 8px; overflow: hidden;
  }
  .stat {
    padding: 12px 14px;
    border-right: 1px solid var(--border-hair);
    display: flex; flex-direction: column; gap: 2px;
  }
  .stat:last-child { border-right: none; }
  .stat-k {
    font-size: 10px; color: var(--fg-muted);
    text-transform: uppercase; letter-spacing: 0.06em;
  }
  .stat-v {
    font-size: 16px; font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .stat-v.faint { color: var(--fg-muted); }
  .stat-s { font-size: 10px; color: var(--fg-muted); }
  .mono { font-family: var(--font-mono); }

  /* ── Restore banner ──────────────────────────────────────────────────── */
  .restore-banner {
    display: flex; align-items: center; gap: 12px;
    padding: 12px 14px;
    background: color-mix(in oklch, #a78bfa 8%, var(--bg-card));
    border: 1px solid color-mix(in oklch, #a78bfa 30%, transparent);
    border-radius: 8px;
  }
  .restore-icon { font-size: 18px; flex-shrink: 0; }
  .restore-text { flex: 1; min-width: 0; }
  .restore-title { font-size: 13px; font-weight: 600; color: #a78bfa; }
  .restore-body { font-size: 11px; color: var(--fg-muted); margin-top: 2px; }
  .restore-btn {
    padding: 6px 12px; border-radius: 6px; font-size: 12px; font-weight: 500;
    background: #a78bfa; color: #fff; border: none; cursor: pointer;
    flex-shrink: 0;
  }
  .restore-btn:hover { opacity: 0.85; }

  /* ── Generic card ────────────────────────────────────────────────────── */
  .card {
    background: var(--bg-card); border: 1px solid var(--border-hair);
    border-radius: 8px; overflow: hidden;
  }
  .card-head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border-hair);
  }
  .title {
    font-size: 12px; font-weight: 600; color: var(--fg);
    display: flex; align-items: center; gap: 6px;
  }
  .sub { font-size: 11px; font-weight: 400; color: var(--fg-muted); }
  .card-body { padding: 12px 14px; }

  /* ── KLYNE_SUMMARY ───────────────────────────────────────────────────── */
  .summary-text {
    font-size: 12.5px; color: var(--fg-soft, var(--fg-muted));
    line-height: 1.6; white-space: pre-wrap;
  }

  /* ── Advisors ────────────────────────────────────────────────────────── */
  .advisors-list {
    display: flex; flex-direction: column; gap: 6px;
    padding: 10px 12px;
  }
  .adv-row {
    padding: 8px 10px; border-radius: 6px;
    background: var(--bg-inset); border-left: 2px solid var(--border-soft);
  }
  .adv-alert { border-left-color: var(--error, #f87171); }
  .adv-warn  { border-left-color: var(--warn, #f59e0b); }
  .adv-top {
    display: flex; justify-content: space-between; align-items: center;
    margin-bottom: 3px;
  }
  .adv-kind {
    font-size: 10px; font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.08em; color: var(--fg-muted);
  }
  .adv-alert .adv-kind { color: var(--error, #f87171); }
  .adv-warn .adv-kind  { color: var(--warn, #f59e0b); }
  .adv-ago  { font-size: 10px; color: var(--fg-muted); }
  .adv-body {
    margin: 0; font-size: 11.5px; color: var(--fg-soft, var(--fg-muted));
    line-height: 1.5;
    display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2;
    -webkit-box-orient: vertical; overflow: hidden;
  }

  /* ── Message stream ──────────────────────────────────────────────────── */
  .msg-stream {
    display: flex; flex-direction: column; gap: 1px;
  }
  .msg-row {
    padding: 10px 14px;
    border-bottom: 1px solid var(--border-hair);
  }
  .msg-row:last-child { border-bottom: none; }
  .msg-head {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 4px;
  }
  .msg-role {
    font-size: 11px; font-weight: 600;
    font-family: var(--font-mono); text-transform: lowercase;
  }
  .msg-meta { font-size: 10px; color: var(--fg-muted); }
  .msg-body {
    font-size: 13px; color: var(--fg); white-space: pre-wrap;
    line-height: 1.55; word-break: break-word;
  }
  .msg-clipped {
    max-height: 7.75em; overflow: hidden;
    -webkit-mask-image: linear-gradient(to bottom, black 60%, transparent 100%);
    mask-image: linear-gradient(to bottom, black 60%, transparent 100%);
  }
  .msg-toggle {
    background: none; border: none; cursor: pointer;
    font-size: 11px; color: var(--fg-muted);
    margin-top: 4px; padding: 0;
  }
  .msg-toggle:hover { color: var(--fg); }
  .empty-msg {
    padding: 20px 14px; text-align: center;
    color: var(--fg-muted); font-size: 13px;
  }

  /* ── Resume command ──────────────────────────────────────────────────── */
  .k-btn {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 4px 10px; border-radius: 5px; font-size: 11px;
    background: transparent; border: 1px solid var(--border-soft);
    color: var(--fg-muted); cursor: pointer;
  }
  .k-btn:hover { color: var(--fg); border-color: var(--border); }
  .resume {
    margin: 0; padding: 12px 14px;
    font-family: var(--font-mono); font-size: 11.5px;
    color: var(--fg-soft, var(--fg-muted));
    white-space: pre-wrap; word-break: break-all;
  }

  /* ── Responsive: narrower screens stack stat tiles 2-up ─────────────── */
  @media (max-width: 520px) {
    .stats-grid { grid-template-columns: repeat(2, 1fr); }
    .stat:nth-child(2) { border-right: none; }
  }
</style>
