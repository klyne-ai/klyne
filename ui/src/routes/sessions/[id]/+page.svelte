<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { fetchSession, fetchMessages, fetchRestore, deleteSession } from '$lib/api.js';
  import { removeSession } from '$lib/stores.svelte.js';
  import { subscribe } from '$lib/sse.js';
  import { kfmt, relAgo, costFmt } from '$lib/format.js';
  import type { Session, Message, RestoreResponse } from '$lib/types.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import StatusBadge from '$lib/ui/StatusBadge.svelte';
  import TokenSavings from '$lib/components/TokenSavings.svelte';
  import TokenTimelineChart from '$lib/components/TokenTimelineChart.svelte';

  const sessionId = $derived($page.params.id ?? '');

  let session = $state<Session | null>(null);
  let messages = $state<Message[]>([]);
  let restoreData = $state<RestoreResponse | null>(null);
  let showRestore = $state(false);
  let loadError = $state<string | null>(null);
  let copyFeedback = $state(false);
  let deleting = $state(false);

  async function handleDelete(): Promise<void> {
    if (!session) return;
    const ok = window.confirm(
      `Delete this session permanently?\n\nProject: ${session.project_path}\nMessages: ${session.msg_count}\n↓ Tokens: ${kfmt(session.tokens_out)}\n\nThis only removes it from klyne — the source JSONL on disk is untouched. Cannot be undone.`
    );
    if (!ok) return;
    deleting = true;
    try {
      await deleteSession(session.id);
      removeSession(session.id);
      const dest = projectName ? `/projects/${encodeURIComponent(projectName)}` : '/projects';
      void goto(dest);
    } catch (err) {
      loadError = err instanceof Error ? err.message : 'delete failed';
      deleting = false;
    }
  }

  // Per-message expanded state keyed by message id (long bodies are
  // collapsed by default; click to expand).
  let expandedIds = $state<Set<string>>(new Set());

  function toggleExpanded(id: string): void {
    const next = new Set(expandedIds);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expandedIds = next;
  }

  // Heuristic: messages with >=6 newlines OR >=480 chars are considered "long"
  function isLong(text: string): boolean {
    if (!text) return false;
    const newlineCount = (text.match(/\n/g) || []).length;
    return newlineCount >= 6 || text.length >= 480;
  }

  const isCompacted = $derived(session?.status === 'compacted');
  const projectName = $derived(
    session ? (session.project_path.split('/').filter(Boolean).pop() ?? session.project_path) : ''
  );

  // Global rule (see $lib/messageFilters.ts): never show tool /
  // system / pure-tool-call rows. The session detail view is purely
  // user + assistant prose; the action-trace is implicit in the
  // assistant's narration and the Files / Tokens tabs.
  const visibleMessages = $derived(messages.filter(isConversationalMessage));
  const hiddenCount = $derived(messages.length - visibleMessages.length);

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
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load session';
    }
  }

  async function fetchNewMessages(id: string): Promise<void> {
    try {
      const res = await fetchMessages(id, { limit: 50, order: 'desc' });
      const latest = newestPageInDisplayOrder(res.messages);
      const newMsgs = latest.filter(
        (m) => !messages.some((existing) => existing.id === m.id)
      );
      if (newMsgs.length > 0) {
        messages = sortMessagesForDisplay([...messages, ...newMsgs]);
      }
    } catch {
      // non-fatal
    }
  }

  async function openRestore(): Promise<void> {
    if (!restoreData) {
      try {
        restoreData = await fetchRestore(sessionId);
      } catch {
        // fallback with no data
      }
    }
    showRestore = true;
  }

  /** Build a paste-and-go resume command. Just copying the session id
   *  is a footgun — Claude Code resolves --resume against $PWD, so
   *  pasting the bare id from a fresh shell yields "No conversation
   *  found". The full command cd's to the originating project_path
   *  first so the resume succeeds wherever the user pastes it. */
  function resumeCommand(): string {
    if (!session) return `claude --resume ${sessionId}`;
    const cli = session.cli === 'codex' ? 'codex' : 'claude';
    const safePath = session.project_path.replace(/'/g, `'\\''`);
    return `cd '${safePath}' && ${cli} --resume ${session.id}`;
  }

  async function copyResumeCmd(): Promise<void> {
    await navigator.clipboard.writeText(resumeCommand()).catch(() => {});
    copyFeedback = true;
    setTimeout(() => { copyFeedback = false; }, 1200);
  }

  let unsubscribe: (() => void) | null = null;

  onMount(() => {
    void loadData(sessionId);

    unsubscribe = subscribe({
      onMsgNew: (payload) => {
        if (payload.session_id === sessionId) {
          void fetchNewMessages(sessionId);
        }
      },
    });
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
  });

  $effect(() => {
    const id = sessionId;
    if (id) void loadData(id);
  });
</script>

<svelte:head>
  <title>klyne — session {sessionId.slice(0, 8)}</title>
</svelte:head>

<div style="padding: 20px 24px 60px; max-width: 980px;">
  <button
    onclick={() => projectName ? goto(`/projects/${encodeURIComponent(projectName)}`) : goto('/projects')}
    class="ad-btn ad-btn--ghost"
    style="margin-bottom: 12px; padding-left: 4px;"
  >
    ‹ {projectName || 'projects'}
  </button>

  {#if loadError}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error);">{loadError}</div>
  {:else if session}
    <!-- Header -->
    <div style="display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; gap: 16px;">
      <div style="min-width: 0; flex: 1;">
        <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
          <h1 style="font-size: 20px; font-weight: 600; margin: 0;">session</h1>
          <span class="ad-mono ad-muted" style="font-size: 14px; overflow: hidden; text-overflow: ellipsis;">{session.id}</span>
          <button
            class="ad-btn ad-btn--ghost ad-btn--sm"
            onclick={copyResumeCmd}
            title={session ? `Copy: ${resumeCommand()}` : 'Copy resume command'}
          >
            {copyFeedback ? 'copied!' : '⎘ resume cmd'}
          </button>
          <button
            class="ad-btn ad-btn--ghost ad-btn--sm"
            onclick={handleDelete}
            disabled={deleting}
            title="Delete this session permanently"
            style="color: var(--ad-error, #f87171); margin-left: auto;"
          >{deleting ? 'deleting…' : 'delete'}</button>
        </div>
        <div style="display: flex; gap: 12px; font-size: 12px; color: var(--ad-muted); flex-wrap: wrap; align-items: center;">
          <CliBadge cli={session.cli} />
          <span class="ad-badge ad-badge--ghost ad-mono" style="font-size: 11px;">{session.model}</span>
          <StatusBadge status={session.status === 'compacted' ? 'compacted' : (Date.now() - session.last_msg_at < 60_000 ? 'active' : 'idle')} />
          <span>started {relAgo(Date.now() - session.started_at)}</span>
          <span>·</span>
          <span>last msg {relAgo(Date.now() - session.last_msg_at)}</span>
        </div>
      </div>
    </div>

    <!-- Stats strip — input is broken down: fresh / cache_read / cache_write. -->
    {@const cachedRead = session.cached_read_tokens ?? 0}
    {@const cachedWrite = session.cached_write_tokens ?? 0}
    {@const freshIn = Math.max(0, session.tokens_in - cachedRead - cachedWrite)}
    {@const cachedPct = session.tokens_in > 0 ? Math.round(((cachedRead + cachedWrite) / session.tokens_in) * 100) : 0}
    <div class="ad-card" style="display: grid; grid-template-columns: repeat(4, 1fr); padding: 0; margin-bottom: 16px;">
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">Messages</div>
        <div class="ad-mono ad-tnum" style="font-size: 16px; font-weight: 600;">{session.msg_count}</div>
      </div>
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">↑ tokens in</div>
        <div class="ad-mono ad-tnum" style="font-size: 16px; font-weight: 600;">{kfmt(session.tokens_in)}</div>
        {#if cachedRead > 0 || cachedWrite > 0}
          <div class="ad-mono" style="font-size: 10px; color: var(--ad-faint); margin-top: 4px; line-height: 1.4;">
            fresh {kfmt(freshIn)}
            {#if cachedRead > 0} · cache↻ {kfmt(cachedRead)}{/if}
            {#if cachedWrite > 0} · cache+ {kfmt(cachedWrite)}{/if}
            <br />{cachedPct}% cached
          </div>
        {/if}
      </div>
      <div style="padding: 12px 16px; border-right: 1px solid var(--ad-border-soft);">
        <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">↓ tokens out</div>
        <div class="ad-mono ad-tnum" style="font-size: 16px; font-weight: 600;">{kfmt(session.tokens_out)}</div>
      </div>
      <div style="padding: 12px 16px;">
        <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">Cost</div>
        <div class="ad-mono ad-tnum" style="font-size: 16px; font-weight: 600; color: {session.cost_usd > 0 ? 'var(--ad-fg)' : 'var(--ad-faint)'};">{costFmt(session.cost_usd, session.cost_usd > 0)}</div>
      </div>
    </div>

    <!-- Token-savings indicator: passive context-fill bar + active
         compact-now CTA + AI break advisor. Hidden via internal logic
         when uncalibrated or under thresholds. -->
    <TokenSavings sessionId={session.id} cli={session.cli} projectPath={session.project_path} />

    <!-- Token-usage line chart. Shares the same per-turn computation
         as `klyne tokens` and the get_token_timeline MCP tool, so the
         curve here matches what the CLI prints. -->
    <TokenTimelineChart sessionId={session.id} />

    <!-- Compact banner -->
    {#if isCompacted}
      <div style="background: var(--ad-compact-bg); border: 1px solid color-mix(in oklch, var(--ad-compact) 40%, transparent); border-radius: 6px; padding: 12px 14px; margin-bottom: 16px; display: flex; align-items: center; gap: 12px;">
        <span style="font-size: 16px;">♻</span>
        <div style="flex: 1;">
          <div style="font-weight: 600; color: var(--ad-compact); font-size: 13px;">This session was compacted</div>
          <div style="font-size: 12px; color: var(--ad-fg-2);">Context window trimmed. Restore the rolling summary as a resume prompt.</div>
        </div>
        <button class="ad-btn ad-btn--primary" onclick={openRestore}>↩ Restore context</button>
      </div>
    {/if}

    <!-- Hidden-count footer: tool / system / pure-tool-call rows are
         filtered globally; the count is shown so the user knows the
         transcript on disk is larger than what's rendered. -->
    {#if hiddenCount > 0}
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px; padding: 6px 12px; background: var(--ad-bg-2); border: 1px solid var(--ad-border-soft); border-radius: var(--ad-r-sm);">
        <span class="ad-mono ad-faint" style="font-size: 11px;">
          {hiddenCount} tool / system row{hiddenCount === 1 ? '' : 's'} hidden — only user + assistant prose is shown
        </span>
      </div>
    {/if}

    <!-- Messages -->
    <div class="ad-stack" style="gap: 8px;">
      {#each visibleMessages as m (m.id)}
        {@const cliLabel = session?.cli === 'codex' ? 'codex' : 'claude'}
        {@const cliColor = session?.cli === 'codex' ? 'var(--ad-codex)' : 'var(--ad-claude)'}
        {@const palette = m.role === 'user'
          ? { lab: 'you',    color: 'var(--ad-fg)', bg: 'var(--ad-panel)' }
          : { lab: cliLabel, color: cliColor,       bg: 'var(--ad-panel)' }}
        {@const isExpanded = expandedIds.has(m.id)}
        {@const long = isLong(m.content)}

        <div class="ad-card" style="background: {palette.bg}; padding: 0;">
          <!-- Row header -->
          <div style="display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; border-bottom: 1px solid var(--ad-border-soft);">
            <div style="display: flex; align-items: center; gap: 8px;">
              <span style="font-size: 11px; font-weight: 600; color: {palette.color}; font-family: var(--ad-font-mono); text-transform: lowercase;">{palette.lab}</span>
            </div>
            <span class="ad-mono ad-faint" style="font-size: 11px;">
              {m.tokens_out ? `↓ ${m.tokens_out} · ` : ''}{relAgo(Date.now() - m.ts)}
            </span>
          </div>

          <!-- Row body — visibleMessages guarantees role is user|assistant
               with non-empty content, so the body is always plain prose. -->
          <div style="padding: 10px 12px;">
            <div
              style="font-family: var(--ad-font-ui); font-size: 13px; color: var(--ad-fg); white-space: pre-wrap; line-height: 1.55; {long && !isExpanded ? 'max-height: 7.75em; overflow: hidden; -webkit-mask-image: linear-gradient(to bottom, black 60%, transparent 100%); mask-image: linear-gradient(to bottom, black 60%, transparent 100%);' : ''}"
            >{m.content}</div>
            {#if long}
              <button
                class="ad-btn ad-btn--ghost ad-btn--sm"
                onclick={() => toggleExpanded(m.id)}
                style="margin-top: 6px; font-size: 11px; color: var(--ad-muted);"
              >{isExpanded ? 'Show less' : 'Show more'}</button>
            {/if}

          </div>
        </div>
      {/each}
      {#if visibleMessages.length === 0}
        <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">No messages loaded.</div>
      {/if}
    </div>
  {:else}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">Loading session…</div>
  {/if}
</div>

<!-- Restore modal -->
{#if showRestore && session}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    onclick={() => { showRestore = false; }}
    style="position: fixed; inset: 0; background: rgba(0,0,0,0.55); display: grid; place-items: center; z-index: 100; padding: 24px;"
  >
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      onclick={(e) => e.stopPropagation()}
      class="ad-card"
      style="background: var(--ad-bg-2); padding: 0; width: min(680px, 100%); max-height: 80vh; display: flex; flex-direction: column;"
    >
      <div style="padding: 14px 18px; border-bottom: 1px solid var(--ad-border); display: flex; justify-content: space-between; align-items: center;">
        <div>
          <div style="font-weight: 600;">Restore context</div>
          <div class="ad-muted" style="font-size: 12px;">Rolling summary + last messages</div>
        </div>
        <button onclick={() => { showRestore = false; }} class="ad-btn ad-btn--ghost">✕</button>
      </div>
      <div style="padding: 16px; overflow-y: auto; flex: 1;">
        <div class="ad-section-h" style="margin-bottom: 6px;">Resume command</div>
        <div class="ad-mono" style="background: var(--ad-bg); padding: 10px 12px; border-radius: 4px; border: 1px solid var(--ad-border); font-size: 12px; display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;">
          <span>{restoreData?.resume_cmd ?? `claude --resume ${sessionId}`}</span>
          <button
            class="ad-btn ad-btn--sm"
            onclick={() => navigator.clipboard.writeText(restoreData?.resume_cmd ?? `claude --resume ${sessionId}`).catch(() => {})}
          >copy</button>
        </div>
        {#if restoreData?.markdown}
          <div class="ad-section-h" style="margin-bottom: 6px;">Markdown bundle</div>
          <pre class="ad-mono" style="background: var(--ad-bg); padding: 12px; border-radius: 4px; border: 1px solid var(--ad-border); font-size: 11px; margin: 0; white-space: pre-wrap; line-height: 1.55; color: var(--ad-fg-2);">{restoreData.markdown}</pre>
        {/if}
      </div>
      <div style="padding: 12px; border-top: 1px solid var(--ad-border); display: flex; justify-content: flex-end; gap: 8px;">
        <button class="ad-btn" onclick={() => { showRestore = false; }}>Close</button>
        <button
          class="ad-btn ad-btn--primary"
          onclick={() => {
            if (restoreData?.markdown) {
              navigator.clipboard.writeText(restoreData.markdown).catch(() => {});
            }
          }}
        >Copy as resume prompt</button>
      </div>
    </div>
  </div>
{/if}
