<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { fetchSession, fetchMessages, fetchRestore } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import { kfmt, costFmt, relAgo } from '$lib/format.js';
  import type { Session, Message, RestoreResponse } from '$lib/types.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import StatusBadge from '$lib/ui/StatusBadge.svelte';

  const sessionId = $derived($page.params.id ?? '');

  let session = $state<Session | null>(null);
  let messages = $state<Message[]>([]);
  let restoreData = $state<RestoreResponse | null>(null);
  let showRestore = $state(false);
  let loadError = $state<string | null>(null);
  let copyFeedback = $state(false);

  const isCompacted = $derived(session?.status === 'compacted');
  const projectName = $derived(
    session ? (session.project_path.split('/').filter(Boolean).pop() ?? session.project_path) : ''
  );

  async function loadData(id: string): Promise<void> {
    if (!id) return;
    loadError = null;
    try {
      const [sr, mr] = await Promise.all([
        fetchSession(id),
        fetchMessages(id, { limit: 100 }),
      ]);
      session = sr.session;
      messages = mr.messages;
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load session';
    }
  }

  async function fetchNewMessages(id: string): Promise<void> {
    try {
      const res = await fetchMessages(id, { limit: 50 });
      const newMsgs = res.messages.filter(
        (m) => !messages.some((existing) => existing.id === m.id)
      );
      if (newMsgs.length > 0) {
        messages = [...messages, ...newMsgs];
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

  async function copyId(): Promise<void> {
    await navigator.clipboard.writeText(sessionId).catch(() => {});
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
  <title>agentdeck — session {sessionId.slice(0, 8)}</title>
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
          <button class="ad-btn ad-btn--ghost ad-btn--sm" onclick={copyId} title="Copy ID">
            {copyFeedback ? 'copied!' : 'copy'}
          </button>
        </div>
        <div style="display: flex; gap: 12px; font-size: 12px; color: var(--ad-muted); flex-wrap: wrap; align-items: center;">
          <CliBadge cli={session.cli} />
          <span class="ad-badge ad-badge--ghost ad-mono" style="font-size: 11px;">{session.model}</span>
          <StatusBadge status={session.status} />
          <span>started {relAgo(Date.now() - session.started_at)}</span>
          <span>·</span>
          <span>last msg {relAgo(Date.now() - session.last_msg_at)}</span>
        </div>
      </div>
    </div>

    <!-- Stats strip -->
    <div class="ad-card" style="display: grid; grid-template-columns: repeat(4, 1fr); padding: 0; margin-bottom: 16px;">
      {#each [
        ['Messages', String(session.msg_count)],
        ['↑ tokens', kfmt(session.tokens_in)],
        ['↓ tokens', kfmt(session.tokens_out)],
        ['Cost', costFmt(session.cost_usd, session.cost_usd > 0)],
      ] as [label, value], i}
        <div style="padding: 12px 16px; border-right: {i < 3 ? '1px solid var(--ad-border-soft)' : 'none'};">
          <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">{label}</div>
          <div class="ad-mono ad-tnum" style="font-size: 16px; font-weight: 600;">{value}</div>
        </div>
      {/each}
    </div>

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

    <!-- Messages -->
    <div class="ad-stack" style="gap: 8px;">
      {#each messages as m}
        {@const palette = m.role === 'user'
          ? { lab: 'you', color: 'var(--ad-fg)', bg: 'var(--ad-panel)' }
          : m.role === 'assistant'
          ? { lab: 'claude', color: 'var(--ad-claude)', bg: 'var(--ad-panel)' }
          : m.role === 'tool'
          ? { lab: m.tool_calls?.[0]?.name ?? 'tool', color: 'var(--ad-fg-2)', bg: 'var(--ad-bg-2)' }
          : m.role === 'system'
          ? { lab: 'system', color: 'var(--ad-compact)', bg: 'var(--ad-compact-bg)' }
          : { lab: m.role, color: 'var(--ad-muted)', bg: 'var(--ad-panel)' }}
        <div class="ad-card" style="background: {palette.bg}; padding: 0;">
          <div style="display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; border-bottom: 1px solid var(--ad-border-soft);">
            <div style="display: flex; align-items: center; gap: 8px;">
              <span style="font-size: 11px; font-weight: 600; color: {palette.color}; font-family: var(--ad-font-mono); text-transform: lowercase;">{palette.lab}</span>
              {#if m.role === 'tool'}
                <span class="ad-badge ad-badge--ghost" style="font-size: 10px;">tool_use</span>
              {/if}
            </div>
            <span class="ad-mono ad-faint" style="font-size: 11px;">
              {m.tokens_out ? `↓ ${m.tokens_out} · ` : ''}{relAgo(Date.now() - m.ts)}
            </span>
          </div>
          <div style="padding: 10px 12px; font-family: {m.role === 'tool' || m.role === 'system' ? 'var(--ad-font-mono)' : 'var(--ad-font-ui)'}; font-size: {m.role === 'tool' || m.role === 'system' ? '12px' : '13px'}; color: {m.role === 'system' ? 'var(--ad-compact)' : 'var(--ad-fg)'}; white-space: pre-wrap; line-height: 1.55;">{m.content}</div>
        </div>
      {/each}
      {#if messages.length === 0}
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
