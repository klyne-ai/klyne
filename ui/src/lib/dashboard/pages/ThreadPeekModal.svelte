<!--
  Thread peek modal — opens from a Live tile's "expand" button. Shows
  the full message tail for one session in a centered modal, pop-animated
  from the button's origin. Three tabs:

    - Messages — the conversational stream (staggered reveal, default).
    - Context  — context-window fill, per-reply cost trend, 5h window,
                 and any active advisor messages (collapsed by default).
    - Files    — every file Claude has loaded into context with relevance
                 score + stale flag. Filter chips: all / stale / useful.

  Header carries always-visible stats pills (tokens in/out, ctx fill %,
  file count, 5h %) so the high-signal numbers stay one glance away no
  matter which tab is open.

  Click scrim, press Esc, or hit ✕ to dismiss; closing plays the
  reverse fade/scale.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl } from '$lib/dashboard/url-state';
  import { relAgo, relTime, kfmt } from '$lib/format';
  import { isConversationalMessage } from '$lib/messageFilters';
  import { portal } from '$lib/dashboard/portal';
  import { fetchSession, fetchAdvisorDetail, fetchMessages } from '$lib/api';
  import type {
    CockpitThread,
    Message,
    Session,
    AdvisorDetailResponse,
    AdvisoryKind,
    AdvisoryRow,
    StaleProof,
    AccelerationProof,
    ContextWindowProof,
  } from '$lib/types';

  interface Props {
    thread: CockpitThread;
    /**
     * Initial messages from the cockpit feed (latest few). On open we
     * fetch the full page via fetchMessages so the user sees the whole
     * recent conversation, not just the cockpit's truncated tail.
     */
    messages: Message[];
    origin: { x: number; y: number } | null;
    tickMs: number;
    onClose: () => void;
  }
  const { thread, messages: initialMessages, origin, tickMs, onClose }: Props = $props();

  type Tab = 'messages' | 'context' | 'files';
  type FileFilter = 'all' | 'stale' | 'useful';

  let activeTab = $state<Tab>('messages');
  let fileFilter = $state<FileFilter>('all');
  let closing = $state(false);

  // ── Async data (best-effort, modal stays usable on partial load) ──
  let session = $state<Session | null>(null);
  let advisor = $state<AdvisorDetailResponse | null>(null);
  let loadingDetail = $state(true);
  let detailError = $state<string | null>(null);

  // ── Messages — start with the cockpit feed snapshot, then replace
  //    with a 100-newest fetch on mount; paginate older via cursor.
  //    Seeded on the first $effect tick to avoid Svelte's
  //    state_referenced_locally warning on prop capture. ──
  let messages = $state<Message[]>([]);
  let seeded = false;
  let olderCursor = $state<number | null>(null);
  let loadingOlder = $state(false);
  let messagesError = $state<string | null>(null);

  // ── Expanded state for collapsible advisory groups ──
  let advisoryOpenKinds = $state<Set<AdvisoryKind>>(new Set());

  const visible = $derived(messages.filter(isConversationalMessage));
  const isLive = $derived(tickMs - thread.last_msg_at < 60_000);

  function sortMessages(list: Message[]): Message[] {
    return [...list].sort((a, b) => (a.ts - b.ts) || a.id.localeCompare(b.id));
  }

  async function fetchInitialMessages(): Promise<void> {
    try {
      const r = await fetchMessages(thread.session_id, { limit: 100, order: 'desc' });
      // API returns newest-first; we display chronological so flip.
      messages = sortMessages(r.messages);
      olderCursor = r.next_before > 0 ? r.next_before : null;
      messagesError = null;
    } catch (e) {
      messagesError = e instanceof Error ? e.message : 'failed to load messages';
    }
  }

  async function loadOlderMessages(): Promise<void> {
    if (olderCursor === null || loadingOlder) return;
    loadingOlder = true;
    try {
      const r = await fetchMessages(thread.session_id, {
        limit: 100,
        before: olderCursor,
        order: 'desc',
      });
      messages = sortMessages([...r.messages, ...messages]);
      olderCursor = r.next_before > 0 ? r.next_before : null;
    } catch {
      // non-fatal — user can click again
    } finally {
      loadingOlder = false;
    }
  }

  function close(): void {
    closing = true;
    setTimeout(onClose, 140);
  }

  function openFullSession(): void {
    onClose();
    void goto(sessionUrl($page.url.pathname + $page.url.search, thread.session_id));
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  async function loadDetail(): Promise<void> {
    loadingDetail = true;
    detailError = null;
    try {
      const [s, a] = await Promise.all([
        fetchSession(thread.session_id).then((r) => r.session).catch(() => null),
        fetchAdvisorDetail(thread.session_id).catch((e) => {
          throw e;
        }),
      ]);
      session = s;
      advisor = a;
    } catch (e) {
      detailError = e instanceof Error ? e.message : 'failed to load context';
    } finally {
      loadingDetail = false;
    }
  }

  let prevOverflow = '';
  onMount(() => {
    if (!seeded) {
      messages = initialMessages;
      seeded = true;
    }
    window.addEventListener('keydown', onKey);
    prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    void loadDetail();
    void fetchInitialMessages();
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    document.body.style.overflow = prevOverflow;
  });

  const cssOrigin = $derived(
    origin
      ? `--tm-origin-x:${origin.x}vw;--tm-origin-y:${origin.y}vh;`
      : ''
  );

  const projectName = $derived(
    thread.project_path.split('/').filter(Boolean).pop() ?? thread.project_path
  );

  // ── Header pill derivations ──
  const tokensIn = $derived(session?.tokens_in ?? 0);
  const tokensOut = $derived(session?.tokens_out ?? 0);

  // Context fill — sourced from advisor proof when available so the bar in
  // the Context tab and the header pill always agree.
  const ctxFillPct = $derived(advisor?.context_window.fill_pct ?? 0);
  const ctxFillTone = $derived<'' | 'warn' | 'alert'>(
    ctxFillPct >= 75 ? 'alert' : ctxFillPct >= 50 ? 'warn' : ''
  );

  const fiveHourPct = $derived(advisor?.five_hour?.pct_used ?? 0);
  const fiveHourTone = $derived<'' | 'warn' | 'alert'>(
    fiveHourPct >= 75 ? 'alert' : fiveHourPct >= 50 ? 'warn' : ''
  );

  // ── Files data ──
  const files = $derived(advisor?.stale.files ?? []);
  const staleCount = $derived(files.filter((f) => f.stale).length);
  const filesTone = $derived<'' | 'warn' | 'alert'>(
    advisor && advisor.stale.stale_share > advisor.stale.threshold ? 'alert' : staleCount > 0 ? 'warn' : ''
  );
  const filteredFiles = $derived(
    fileFilter === 'all'
      ? [...files].sort((a, b) => b.score - a.score)
      : fileFilter === 'stale'
        ? files.filter((f) => f.stale).sort((a, b) => b.score - a.score)
        : files.filter((f) => !f.stale).sort((a, b) => b.score - a.score)
  );

  // ── Advisory data ──
  const advisoriesByKind = $derived.by(() => {
    const groups: Partial<Record<AdvisoryKind, AdvisoryRow[]>> = {};
    for (const a of advisor?.advisories ?? []) {
      (groups[a.kind] ??= []).push(a);
    }
    return groups;
  });
  const advisoryKinds = $derived(Object.keys(advisoriesByKind) as AdvisoryKind[]);
  const advisoryTotal = $derived(advisor?.advisories.length ?? 0);

  const kindMeta: Record<AdvisoryKind, { label: string; tone: 'warn' | 'alert' }> = {
    stale: { label: 'stale context', tone: 'warn' },
    acceleration: { label: 'acceleration', tone: 'warn' },
    hard_ceiling: { label: 'hard ceiling', tone: 'alert' },
    window_50: { label: '5h window 50%', tone: 'warn' },
    window_75: { label: '5h window 75%', tone: 'alert' },
    topic_shift: { label: 'topic shift', tone: 'warn' },
    unknown: { label: 'other', tone: 'warn' },
  };

  function wouldFireNow(kind: AdvisoryKind, d: AdvisorDetailResponse): boolean {
    switch (kind) {
      case 'stale':
        return d.stale.stale_share > d.stale.threshold;
      case 'acceleration':
        return d.acceleration.would_fire;
      case 'hard_ceiling':
        return d.context_window.would_fire;
      case 'window_50':
        return (d.five_hour?.pct_used ?? 0) >= 50;
      case 'window_75':
        return (d.five_hour?.pct_used ?? 0) >= 75;
      case 'topic_shift':
        return d.topic_shift?.would_fire ?? false;
      default:
        return false;
    }
  }

  function toggleAdvisory(k: AdvisoryKind): void {
    const next = new Set(advisoryOpenKinds);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    advisoryOpenKinds = next;
  }

  // ── Plain-English summary helpers (carried over from the deleted
  // AdvisorModal so the trend / fill panels read naturally). ──

  function staleSummary(d: StaleProof): string {
    if (d.files.length === 0) return 'No files loaded yet.';
    const pct = Math.round(d.stale_share * 100);
    const total = kfmt(d.total_bytes);
    const stale = kfmt(d.stale_bytes);
    if (pct === 0) return `All ${total}B of loaded files are still in your rotation.`;
    if (d.stale_share > d.threshold)
      return `${pct}% of loaded files (${stale}B of ${total}B) aren't in your recent rotation — above the alert threshold.`;
    return `${pct}% of loaded files (${stale}B of ${total}B) aren't in rotation; below threshold.`;
  }

  function accelSummary(d: AccelerationProof): string {
    if (d.sampled_turns < 8) return `Not enough data yet (${d.sampled_turns} replies).`;
    const recent = kfmt(d.recent_mean);
    const prior = kfmt(d.prior_mean);
    const r = d.ratio;
    if (!isFinite(r) || r === 0) return `Recent replies very small — not enough signal.`;
    if (r < 0.5) return `Recent ${recent} vs earlier ${prior} (${(1 / r).toFixed(1)}× cheaper). Healthy.`;
    if (r < 1.5) return `Per-reply cost roughly stable (recent ${recent}, earlier ${prior}).`;
    if (r < 2.0)
      return `Per-reply cost trending up — ${recent} vs ${prior} (${r.toFixed(2)}×). Watch this.`;
    return `Recent replies ${r.toFixed(2)}× more expensive than earlier (${recent} vs ${prior}). Consider /klyne:handoff.`;
  }

  function ctxSummary(d: ContextWindowProof): string {
    const pct = Math.round(d.fill_pct);
    const used = kfmt(d.latest_input);
    const max = kfmt(d.context_window);
    if (pct >= 75) return `${pct}% full (${used} of ${max}). Above ${d.threshold}% the advisor recommends a fresh session.`;
    if (pct >= 50) return `${pct}% full (${used} of ${max}). Healthy but watch the trend.`;
    return `${pct}% full (${used} of ${max}). Plenty of headroom.`;
  }
</script>

<div use:portal class="modal-portal-host">
<div
  class="thread-scrim"
  class:is-closing={closing}
  onclick={close}
  role="presentation"
></div>

<div
  class="thread-modal tpm"
  class:is-closing={closing}
  style={cssOrigin}
  role="dialog"
  aria-modal="true"
  aria-label={`Thread for ${projectName} · ${thread.session_id.slice(0, 8)}`}
>
  <header class="thread-modal__head">
    <span class="dot" class:dot--live={isLive} class:dot--idle={!isLive}></span>
    <div class="meta">
      <h3>
        <span>{projectName}</span>
        <span
          class="pill"
          class:pill-claude={thread.cli === 'claude'}
          class:pill-codex={thread.cli === 'codex'}
          style:font-size="10px"
        >{thread.cli}</span>
      </h3>
      <div class="sub">
        <span>{thread.session_id.slice(0, 8)}</span>
        <span>·</span>
        <span>{thread.git_branch ?? '—'}</span>
        <span>·</span>
        <span>{visible.length} msg{visible.length === 1 ? '' : 's'}</span>
        <span>·</span>
        <span>last {relAgo(tickMs - thread.last_msg_at)} ago</span>
      </div>
    </div>
    <button class="k-btn k-btn--ghost" onclick={close} aria-label="Close">✕</button>
  </header>

  <!-- Always-visible stats pills row. Dim while data loads; tinted by tone. -->
  <div class="tpm-pills" aria-label="Session stats">
    <span class="tpm-pill" class:loading={loadingDetail && !session}>
      <span class="tpm-pill-k">in</span>
      <span class="mono">{session ? kfmt(tokensIn) : '—'}</span>
    </span>
    <span class="tpm-pill" class:loading={loadingDetail && !session}>
      <span class="tpm-pill-k">out</span>
      <span class="mono">{session ? kfmt(tokensOut) : '—'}</span>
    </span>
    <span class="tpm-pill" class:loading={loadingDetail && !advisor} data-tone={ctxFillTone}>
      <span class="tpm-pill-k">ctx</span>
      <span class="mono">{advisor ? `${Math.round(ctxFillPct)}%` : '—'}</span>
    </span>
    <span class="tpm-pill" class:loading={loadingDetail && !advisor} data-tone={filesTone}>
      <span class="tpm-pill-k">files</span>
      <span class="mono">
        {#if advisor}
          {files.length}{staleCount > 0 ? ` · ${staleCount} stale` : ''}
        {:else}—{/if}
      </span>
    </span>
    {#if advisor?.five_hour}
      <span class="tpm-pill" data-tone={fiveHourTone}>
        <span class="tpm-pill-k">5h</span>
        <span class="mono">{Math.round(fiveHourPct)}%</span>
      </span>
    {/if}
  </div>

  <!-- Tabs. -->
  <div class="tpm-tabs" role="tablist" aria-label="Modal sections">
    {#each ['messages', 'context', 'files'] as tab (tab)}
      <button
        type="button"
        role="tab"
        class="tpm-tab"
        class:active={activeTab === tab}
        aria-selected={activeTab === tab}
        onclick={() => (activeTab = tab as Tab)}
      >
        {tab}
        {#if tab === 'files' && files.length > 0}
          <span class="tpm-tab-n mono">{files.length}</span>
        {/if}
      </button>
    {/each}
  </div>

  <div class="thread-modal__body tpm-body">
    {#if activeTab === 'messages'}
      {#if olderCursor !== null}
        <button
          type="button"
          class="tpm-load-older"
          onclick={loadOlderMessages}
          disabled={loadingOlder}
          aria-busy={loadingOlder}
        >
          {loadingOlder ? 'Loading older messages…' : '↑ Load older messages'}
        </button>
      {/if}
      {#each visible as m, i (m.id)}
        <div class="msg" style:animation-delay="{i * 55}ms">
          <div class="role">{m.role}</div>
          <p>{m.content}</p>
        </div>
      {/each}
      {#if visible.length === 0}
        {#if messagesError}
          <p class="mono dim">Couldn't load messages — {messagesError}</p>
        {:else}
          <p class="mono dim">no conversational turns captured yet</p>
        {/if}
      {/if}
    {:else if activeTab === 'context'}
      {#if loadingDetail && !advisor}
        <p class="mono dim">Loading context…</p>
      {:else if detailError && !advisor}
        <div class="tpm-error">
          <p>Couldn't load context.</p>
          <p class="mono dim">{detailError}</p>
          <button type="button" class="k-btn" onclick={loadDetail}>Retry</button>
        </div>
      {:else if advisor}
        <!-- Context window fill -->
        <section class="tpm-card" data-tone={ctxFillTone}>
          <div class="tpm-card-head">
            <strong>Context window</strong>
            <span class="mono dim">{kfmt(advisor.context_window.latest_input)} / {kfmt(advisor.context_window.context_window)}</span>
          </div>
          <div class="tpm-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(ctxFillPct)}>
            <div class="tpm-bar-fill" data-tone={ctxFillTone} style:width="{Math.min(100, Math.round(ctxFillPct))}%"></div>
          </div>
          <p class="tpm-card-note">{ctxSummary(advisor.context_window)}</p>
        </section>

        <!-- Per-reply cost trend -->
        <section class="tpm-card" data-tone={advisor.acceleration.would_fire ? 'warn' : ''}>
          <div class="tpm-card-head">
            <strong>Per-reply cost trend</strong>
            <span class="mono dim">{advisor.acceleration.sampled_turns} replies sampled</span>
          </div>
          <div class="tpm-stat-row">
            <div class="tpm-stat">
              <span class="tpm-stat-k">earlier avg</span>
              <span class="mono tpm-stat-v">{kfmt(advisor.acceleration.prior_mean)}</span>
            </div>
            <div class="tpm-stat">
              <span class="tpm-stat-k">recent avg</span>
              <span class="mono tpm-stat-v">{kfmt(advisor.acceleration.recent_mean)}</span>
            </div>
            <div class="tpm-stat">
              <span class="tpm-stat-k">ratio</span>
              <span class="mono tpm-stat-v">{advisor.acceleration.ratio.toFixed(2)}×</span>
            </div>
          </div>
          <p class="tpm-card-note">{accelSummary(advisor.acceleration)}</p>
        </section>

        <!-- 5-hour window -->
        {#if advisor.five_hour}
          <section class="tpm-card" data-tone={fiveHourTone}>
            <div class="tpm-card-head">
              <strong>5-hour window</strong>
              <span class="mono dim">{kfmt(advisor.five_hour.total_effective)} / {kfmt(advisor.five_hour.cap)}{advisor.five_hour.plan_tier ? ` · ${advisor.five_hour.plan_tier}` : ''}</span>
            </div>
            <div class="tpm-bar">
              <div class="tpm-bar-fill" data-tone={fiveHourTone} style:width="{Math.min(100, Math.round(fiveHourPct))}%"></div>
            </div>
          </section>
        {/if}

        <!-- Active advisories (collapsed by default) -->
        <section class="tpm-card">
          <div class="tpm-card-head">
            <strong>Advisories</strong>
            <span class="mono dim">
              {#if advisoryTotal === 0}
                none yet
              {:else}
                {advisoryTotal} message{advisoryTotal === 1 ? '' : 's'} across {advisoryKinds.length} kind{advisoryKinds.length === 1 ? '' : 's'}
              {/if}
            </span>
          </div>
          {#if advisoryTotal === 0}
            <p class="tpm-card-note dim">No advisories have fired yet for this session — signals above are healthy.</p>
          {:else}
            <ul class="tpm-adv-list">
              {#each advisoryKinds as k (k)}
                {@const rows = advisoriesByKind[k] ?? []}
                {@const meta = kindMeta[k]}
                {@const open = advisoryOpenKinds.has(k)}
                {@const stillFiring = wouldFireNow(k, advisor)}
                <li class="tpm-adv">
                  <button
                    type="button"
                    class="tpm-adv-head"
                    aria-expanded={open}
                    onclick={() => toggleAdvisory(k)}
                  >
                    <span class="tpm-adv-chev" class:open>▸</span>
                    <span class="tpm-adv-kind" data-tone={stillFiring ? meta.tone : ''}>{meta.label}</span>
                    <span class="tpm-adv-count mono dim">fired {rows.length}× · {stillFiring ? 'currently active' : 'resolved'}</span>
                  </button>
                  {#if open}
                    <ul class="tpm-adv-rows">
                      {#each rows as adv (adv.message_id)}
                        <li class="tpm-adv-row">
                          <div class="mono dim tpm-adv-when">{relTime(adv.ts)}</div>
                          <p>{adv.content}</p>
                        </li>
                      {/each}
                    </ul>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/if}
    {:else if activeTab === 'files'}
      {#if loadingDetail && !advisor}
        <p class="mono dim">Loading files…</p>
      {:else if detailError && !advisor}
        <div class="tpm-error">
          <p>Couldn't load files.</p>
          <button type="button" class="k-btn" onclick={loadDetail}>Retry</button>
        </div>
      {:else if advisor}
        {#if files.length === 0}
          <p class="mono dim">No files have been loaded into this session yet.</p>
        {:else}
          <div class="tpm-files-head">
            <div class="tpm-chip-row" role="group" aria-label="Filter files">
              {#each ['all', 'stale', 'useful'] as f (f)}
                <button
                  type="button"
                  class="k-btn tpm-chip"
                  class:active={fileFilter === f}
                  aria-pressed={fileFilter === f}
                  onclick={() => (fileFilter = f as FileFilter)}
                >
                  {f}
                  <span class="mono dim tpm-chip-n">
                    {f === 'all'
                      ? files.length
                      : f === 'stale'
                        ? staleCount
                        : files.length - staleCount}
                  </span>
                </button>
              {/each}
            </div>
            <p class="mono dim tpm-files-note">{staleSummary(advisor.stale)}</p>
          </div>

          {#if filteredFiles.length === 0}
            <p class="mono dim">No files match this filter.</p>
          {:else}
            <table class="tpm-files">
              <thead>
                <tr>
                  <th>file</th>
                  <th class="num">bytes</th>
                  <th class="num">relevance</th>
                  <th class="num">status</th>
                </tr>
              </thead>
              <tbody>
                {#each filteredFiles as f (f.path)}
                  <tr>
                    <td class="mono tpm-file-path" title={f.path}>{f.basename}</td>
                    <td class="num mono">{kfmt(f.bytes)}</td>
                    <td class="num mono">{f.score.toFixed(2)}</td>
                    <td class="num"><span class="tpm-file-status" data-tone={f.stale ? 'warn' : 'ok'}>{f.stale ? 'stale' : 'relevant'}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        {/if}
      {/if}
    {/if}
  </div>

  <footer class="thread-modal__foot">
    <span class="mono dim" style:font-size="11px">
      {isLive ? 'streaming' : 'idle'} · {relAgo(tickMs - thread.last_msg_at)} ago
    </span>
    <button class="k-btn" onclick={openFullSession}>open full session →</button>
  </footer>
</div>
</div>

<style>
  .modal-portal-host {
    /* `display: contents` keeps this wrapper transparent to layout so the
       portaled scrim + modal render at <body> level (see portal action). */
    display: contents;
  }

  /* Slightly taller modal so the Context tab's three stacked panels don't
     squeeze the Messages tab when the user switches back. */
  .tpm {
    width: min(720px, 94vw);
    max-height: min(82vh, 820px);
  }

  /* ── Stats pills row ── */
  .tpm-pills {
    display: flex;
    gap: 6px;
    padding: 6px 18px 8px;
    border-bottom: 1px solid var(--border-hair);
    flex-wrap: wrap;
    background: var(--bg-card);
  }
  .tpm-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--bg-card-2);
    border: 1px solid var(--border-hair);
    font-size: 11px;
    color: var(--fg-soft);
    transition: color 120ms ease, border-color 120ms ease, background 120ms ease;
  }
  .tpm-pill.loading {
    opacity: 0.45;
  }
  .tpm-pill[data-tone='warn'] {
    color: var(--warn);
    border-color: color-mix(in oklch, var(--warn) 35%, transparent);
    background: color-mix(in oklch, var(--warn) 10%, var(--bg-card-2));
  }
  .tpm-pill[data-tone='alert'] {
    color: var(--alert);
    border-color: color-mix(in oklch, var(--alert) 35%, transparent);
    background: color-mix(in oklch, var(--alert) 10%, var(--bg-card-2));
  }
  .tpm-pill-k {
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 9.5px;
  }

  /* ── Tabs ── */
  .tpm-tabs {
    display: flex;
    gap: 4px;
    padding: 4px 12px 0;
    border-bottom: 1px solid var(--border-hair);
    background: var(--bg-card);
  }
  .tpm-tab {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px;
    border: 0;
    background: transparent;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 11px;
    text-transform: lowercase;
    border-bottom: 2px solid transparent;
    cursor: pointer;
    transition: color 120ms ease, border-color 120ms ease;
  }
  .tpm-tab:hover {
    color: var(--fg-soft);
  }
  .tpm-tab.active {
    color: var(--fg);
    border-bottom-color: var(--accent);
  }
  .tpm-tab-n {
    font-size: 10px;
    color: var(--fg-muted);
  }

  /* ── Body adjustments ── */
  .tpm-body {
    gap: 14px;
  }

  /* "Load older" button at the top of the messages tab. */
  .tpm-load-older {
    align-self: center;
    padding: 5px 12px;
    border-radius: 999px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg-soft);
    font-family: var(--font-mono);
    font-size: 11px;
    cursor: pointer;
    transition: background 120ms ease, color 120ms ease, border-color 120ms ease;
  }
  .tpm-load-older:not(:disabled):hover {
    background: var(--bg-card);
    color: var(--fg);
    border-color: var(--border);
  }
  .tpm-load-older:disabled {
    opacity: 0.6;
    cursor: progress;
  }

  /* ── Context-tab cards ── */
  .tpm-card {
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    padding: 10px 12px;
    background: var(--bg-card-2);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .tpm-card[data-tone='warn'] {
    border-left: 3px solid var(--warn);
  }
  .tpm-card[data-tone='alert'] {
    border-left: 3px solid var(--alert);
  }
  .tpm-card-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    font-size: 12.5px;
    color: var(--fg);
  }
  .tpm-card-head strong {
    font-weight: 500;
  }
  .tpm-card-head .dim {
    font-size: 11px;
  }
  .tpm-card-note {
    margin: 0;
    font-size: 12px;
    line-height: 1.55;
    color: var(--fg-soft);
  }
  .tpm-card-note.dim {
    color: var(--fg-muted);
  }

  /* Bar visual */
  .tpm-bar {
    width: 100%;
    height: 6px;
    border-radius: 999px;
    background: color-mix(in oklch, var(--fg-muted) 15%, var(--bg-inset));
    overflow: hidden;
  }
  .tpm-bar-fill {
    height: 100%;
    background: var(--ok);
    transition: width 220ms ease;
  }
  .tpm-bar-fill[data-tone='warn'] {
    background: var(--warn);
  }
  .tpm-bar-fill[data-tone='alert'] {
    background: var(--alert);
  }

  /* Stat row */
  .tpm-stat-row {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 10px;
  }
  .tpm-stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .tpm-stat-k {
    font-size: 10px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .tpm-stat-v {
    font-size: 14px;
    color: var(--fg);
  }

  /* Advisory list */
  .tpm-adv-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .tpm-adv {
    border-radius: 6px;
    background: var(--bg-card);
    border: 1px solid var(--border-hair);
  }
  .tpm-adv-head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    border: 0;
    background: transparent;
    color: var(--fg-soft);
    font-family: var(--font-mono);
    font-size: 11.5px;
    text-align: left;
    cursor: pointer;
  }
  .tpm-adv-chev {
    display: inline-block;
    transition: transform 120ms ease;
    font-size: 9px;
    color: var(--fg-muted);
  }
  .tpm-adv-chev.open {
    transform: rotate(90deg);
  }
  .tpm-adv-kind[data-tone='warn'] {
    color: var(--warn);
  }
  .tpm-adv-kind[data-tone='alert'] {
    color: var(--alert);
  }
  .tpm-adv-count {
    margin-left: auto;
    font-size: 10.5px;
  }
  .tpm-adv-rows {
    list-style: none;
    margin: 0;
    padding: 0 12px 10px 30px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    border-top: 1px solid var(--border-hair);
  }
  .tpm-adv-row {
    padding-top: 8px;
  }
  .tpm-adv-row p {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--fg-soft);
    line-height: 1.5;
    white-space: pre-wrap;
  }
  .tpm-adv-when {
    font-size: 10.5px;
  }

  /* Files tab */
  .tpm-files-head {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .tpm-chip-row {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .tpm-chip {
    padding: 4px 9px;
    font-size: 11px;
  }
  .tpm-chip.active {
    color: var(--fg);
    background: var(--bg-card);
    border-color: var(--border);
  }
  .tpm-chip-n {
    margin-left: 4px;
    font-size: 10px;
  }
  .tpm-files-note {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
  }
  .tpm-files {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .tpm-files thead th {
    text-align: left;
    color: var(--fg-muted);
    font-weight: 500;
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border-hair);
  }
  .tpm-files .num {
    text-align: right;
  }
  .tpm-files tbody td {
    padding: 6px 8px;
    border-bottom: 1px solid var(--border-hair);
    color: var(--fg-soft);
  }
  .tpm-file-path {
    max-width: 280px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tpm-file-status {
    display: inline-flex;
    padding: 1px 6px;
    border-radius: 999px;
    font-size: 10px;
    font-family: var(--font-mono);
    border: 1px solid var(--border-hair);
  }
  .tpm-file-status[data-tone='ok'] {
    color: var(--ok);
    border-color: color-mix(in oklch, var(--ok) 35%, transparent);
    background: color-mix(in oklch, var(--ok) 10%, var(--bg-card-2));
  }
  .tpm-file-status[data-tone='warn'] {
    color: var(--warn);
    border-color: color-mix(in oklch, var(--warn) 35%, transparent);
    background: color-mix(in oklch, var(--warn) 10%, var(--bg-card-2));
  }

  /* Error block (shared between Context + Files tabs) */
  .tpm-error {
    border: 1px solid color-mix(in oklch, var(--warn) 35%, transparent);
    background: color-mix(in oklch, var(--warn) 8%, var(--bg-card-2));
    border-radius: 8px;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    align-items: flex-start;
  }
  .tpm-error p {
    margin: 0;
    font-size: 12px;
    color: var(--fg);
  }
</style>
