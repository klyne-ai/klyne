<!--
  Advisors — inbox of every advisory klyne has fired into CLI sessions.
  Filterable by kind. Each row opens the offending session via SessionDrawer.
  Filter persisted in ?kind= URL param.
-->
<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { relTime } from '$lib/format.js';
  import { sessionUrl } from '$lib/dashboard/url-state.js';
  import { advisorsStore } from '$lib/advisors.svelte.js';
  import type { AdvisoryKind } from '$lib/types.js';

  // ---------------------------------------------------------------------------
  // Types
  // ---------------------------------------------------------------------------

  type KindFilter = 'all' | AdvisoryKind;

  interface KindMeta {
    label: string;
    /** CSS custom property reference for the left-rail colour. */
    railVar: string;
    tone: 'warn' | 'alert' | null;
  }

  // ---------------------------------------------------------------------------
  // Kind metadata
  // ---------------------------------------------------------------------------

  const KIND_META: Record<AdvisoryKind, KindMeta> = {
    acceleration: { label: 'acceleration',   railVar: 'var(--warn)',  tone: 'warn'  },
    topic_shift:  { label: 'topic shift',    railVar: 'var(--warn)',  tone: 'warn'  },
    stale:        { label: 'stale context',  railVar: 'var(--warn)',  tone: 'warn'  },
    window_50:    { label: '5h window 50%',  railVar: 'var(--warn)',  tone: 'warn'  },
    window_75:    { label: '5h window 75%',  railVar: 'var(--alert)', tone: 'alert' },
    hard_ceiling: { label: 'hard ceiling',   railVar: 'var(--alert)', tone: 'alert' },
    unknown:      { label: 'other',          railVar: 'var(--fg-muted)', tone: null },
  };

  function metaFor(kind: AdvisoryKind): KindMeta {
    return KIND_META[kind] ?? KIND_META.unknown;
  }

  // ---------------------------------------------------------------------------
  // Filter chip definitions — spec §5.7 order
  // ---------------------------------------------------------------------------

  const CHIPS: { key: KindFilter; label: string; tone: 'warn' | 'alert' | null }[] = [
    { key: 'all',          label: 'all',          tone: null    },
    { key: 'acceleration', label: 'acceleration',  tone: 'warn'  },
    { key: 'topic_shift',  label: 'topic shift',   tone: 'warn'  },
    { key: 'stale',        label: 'stale context', tone: 'warn'  },
    { key: 'hard_ceiling', label: 'hard ceiling',  tone: 'alert' },
  ];

  // ---------------------------------------------------------------------------
  // Store aliases
  // ---------------------------------------------------------------------------

  const advisories = $derived(advisorsStore.advisories);
  const loading = $derived(advisorsStore.loading);
  const error = $derived(advisorsStore.error);

  // Filter comes from the URL ?kind= param.
  const activeKind = $derived.by<KindFilter>(() => {
    const v = $page.url.searchParams.get('kind');
    if (
      v === 'acceleration' || v === 'topic_shift' || v === 'stale' ||
      v === 'hard_ceiling' || v === 'window_50'  || v === 'window_75' ||
      v === 'unknown'
    ) return v as AdvisoryKind;
    return 'all';
  });

  function setFilter(f: KindFilter): void {
    const url = new URL($page.url);
    if (f === 'all') url.searchParams.delete('kind');
    else url.searchParams.set('kind', f);
    void goto(url.toString(), { replaceState: true, keepFocus: true });
  }

  // ---------------------------------------------------------------------------
  // Derived data
  // ---------------------------------------------------------------------------

  const filtered = $derived(
    activeKind === 'all'
      ? advisories
      : advisories.filter((a) => a.kind === activeKind)
  );

  /** Count per chip key. */
  const counts = $derived.by(() => {
    const c: Record<string, number> = { all: advisories.length };
    for (const a of advisories) {
      c[a.kind] = (c[a.kind] ?? 0) + 1;
    }
    return c;
  });

  // last-24h count (server may return unbounded; track it client-side)
  const last24hCount = $derived.by(() => {
    const cutoff = Date.now() - 24 * 60 * 60 * 1000;
    return advisories.filter((a) => a.ts >= cutoff).length;
  });

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------

  function shortSession(id: string): string {
    return id.length > 8 ? id.slice(0, 8) : id;
  }

  function projectName(path: string): string {
    if (!path) return '(unknown)';
    const parts = path.split('/').filter(Boolean);
    return parts.length > 0 ? (parts[parts.length - 1] ?? path) : path;
  }

  function openSession(sessionId: string): void {
    const url = sessionUrl($page.url.pathname + $page.url.search, sessionId);
    void goto(url);
  }

  // ---------------------------------------------------------------------------
  // (no local fetch — data comes from advisorsStore, populated by +layout.svelte)
  // ---------------------------------------------------------------------------
</script>

<div class="advisors-page">
  <!-- ── Page head ── -->
  <header class="page-head">
    <div class="head-left">
      <h1>Advisors</h1>
      <p class="lede">
        Every advisory klyne has fired across your CLI sessions. Each row is a real injection
        into your chat via the <span class="mono">UserPromptSubmit</span> hook — they were
        delivered to the AI's context at the moments shown, even if you didn't see them rendered
        visibly in the chat UI.
      </p>
    </div>
    <div class="head-right">
      {#if !loading}
        <span class="mono dim stat-label">
          {advisories.length} total · {last24hCount} last 24h
        </span>
      {/if}
    </div>
  </header>

  <!-- ── Filter chips ── -->
  <div class="chip-row" role="group" aria-label="Filter by advisor kind">
    {#each CHIPS as chip}
      <button
        class="k-btn"
        class:k-btn--active={activeKind === chip.key}
        onclick={() => setFilter(chip.key)}
        aria-pressed={activeKind === chip.key}
      >
        {#if chip.tone}
          <span
            class="dot"
            class:dot-warn={chip.tone === 'warn'}
            class:dot-alert={chip.tone === 'alert'}
          ></span>
        {/if}
        {chip.label}
        <span class="dim chip-count">{counts[chip.key] ?? 0}</span>
      </button>
    {/each}
  </div>

  <!-- ── Content ── -->
  {#if loading && advisories.length === 0}
    <p class="muted loading-msg">Loading advisories…</p>
  {:else if error}
    <p class="error-msg">⚠ {error}</p>
    <p class="muted">Is the klyne daemon running?</p>
  {:else if advisories.length === 0}
    <div class="empty-box">
      No advisories indexed yet. After the daemon ingests your sessions, every
      <span class="mono">UserPromptSubmit</span> hook firing from
      <span class="mono">klyne advise</span> will appear here.
    </div>
  {:else if filtered.length === 0}
    <div class="empty-box">No advisories match this filter.</div>
  {:else}
    <ul class="card-list" aria-label="Advisory list">
      {#each filtered as adv (adv.message_id)}
        {@const meta = metaFor(adv.kind)}
        <li>
          <!-- Rendered as a button for full keyboard + AT support -->
          <button
            class="adv-card"
            style="--rail: {meta.railVar}"
            onclick={() => openSession(adv.session_id)}
            aria-label="Open session {adv.session_id} — {meta.label} advisory"
          >
            <!-- Row 1: kicker · cli · session · project · ago -->
            <div class="card-row1">
              <div class="card-meta">
                <span class="kicker" style="color: {meta.railVar}">{meta.label.toUpperCase()}</span>
                <span class="mono dim small">{adv.cli || 'claude'}</span>
                <span class="mono dim small session-id">{shortSession(adv.session_id)}</span>
                <span class="dim small">in {projectName(adv.project_path)}</span>
              </div>
              <time
                class="mono dim small ago"
                title={new Date(adv.ts).toISOString()}
                datetime={new Date(adv.ts).toISOString()}
              >{relTime(adv.ts)}</time>
            </div>
            <!-- Row 2: body text -->
            <p class="card-body">{adv.content}</p>
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .advisors-page {
    padding: 1.5rem;
    max-width: 1100px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  /* ── Page head ── */
  .page-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }
  .head-left { flex: 1; min-width: 0; }
  .head-right {
    flex-shrink: 0;
    padding-top: 6px;
  }
  h1 {
    margin: 0 0 6px;
    font-size: 22px;
    font-weight: 600;
    color: var(--fg);
    letter-spacing: -0.02em;
  }
  .lede {
    margin: 0;
    font-size: 13.5px;
    color: var(--fg-muted);
    line-height: 1.55;
    max-width: 680px;
  }
  .stat-label {
    font-size: 11px;
    white-space: nowrap;
  }

  /* ── Filter chip row ── */
  .chip-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }

  /* ── State dots ── */
  .dot {
    display: inline-block;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    margin-right: 4px;
    vertical-align: middle;
    flex-shrink: 0;
  }
  .dot-warn  { background: var(--warn); }
  .dot-alert { background: var(--alert); }

  .chip-count { margin-left: 4px; }

  /* ── Card list ── */
  .card-list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  /* ── Advisory card (button) ── */
  .adv-card {
    /* Reset button styles */
    appearance: none;
    border: none;
    font: inherit;
    text-align: left;
    cursor: pointer;

    /* Card chrome */
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
    padding: 14px 18px;
    border-radius: 8px;
    background: var(--bg-card, var(--bg-2, #1c1c1e));
    border: 1px solid var(--border);
    border-left: 3px solid var(--rail);
    transition: background 0.12s ease;
  }
  .adv-card:hover,
  .adv-card:focus-visible {
    background: var(--bg-card-hover, var(--bg-3, #252528));
    outline: none;
  }
  .adv-card:focus-visible {
    box-shadow: 0 0 0 2px var(--accent, #6c6aff);
  }

  /* ── Card row 1 ── */
  .card-row1 {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 12px;
    flex-wrap: wrap;
  }
  .card-meta {
    display: flex;
    align-items: baseline;
    gap: 10px;
    flex-wrap: wrap;
    min-width: 0;
  }
  .kicker {
    text-transform: uppercase;
    font-size: 10.5px;
    letter-spacing: 0.06em;
    font-weight: 700;
    flex-shrink: 0;
  }
  .session-id {
    opacity: 0.7;
  }
  .ago {
    flex-shrink: 0;
    white-space: nowrap;
  }

  /* ── Card body ── */
  .card-body {
    margin: 0;
    font-size: 12.75px;
    color: var(--fg-soft, var(--fg-muted));
    line-height: 1.55;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* ── Utility ── */
  .muted { color: var(--fg-muted); }
  .mono  { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .dim   { color: var(--fg-dim, var(--fg-muted)); opacity: 0.75; }
  .small { font-size: 11px; }

  /* ── Status messages ── */
  .loading-msg { font-size: 14px; }
  .error-msg {
    color: var(--alert, #e05b5b);
    font-size: 14px;
    margin: 0;
  }
  .empty-box {
    padding: 2rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
    color: var(--fg-muted);
    font-size: 14px;
    text-align: center;
  }
</style>
