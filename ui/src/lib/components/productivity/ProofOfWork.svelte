<!--
  ProofOfWork — the evidence panel behind the dashboard's headline AI time.

  The "Total AI time" number is a global interval-UNION of every AI
  session's active wall-clock. A naive sum of per-session active_minutes
  is *higher* because parallel agents overlap in wall-clock time. This
  panel shows both numbers side by side — measured (union) vs raw sum —
  so the shrinkage is transparent, then lists every contributing session
  as proof of work.

  House style mirrors the rest of klyne's UI: --ad-* design tokens,
  .ad-card / .ad-badge / .ad-mono / .ad-tnum utilities, scoped <style>.
-->
<script lang="ts">
  import type { ProductivityReport, ProductivitySessionStat } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  /** When false, only the top N sessions by active_minutes are shown. */
  let showAll = $state(false);

  /** How many sessions to show before the "show all" toggle kicks in. */
  const TOP_N = 12;

  /** Format minutes as "Xh Ym" — or "Nm" under an hour. Clamps negatives. */
  function formatMinutes(value: number): string {
    const mins = Math.max(0, Math.round(value || 0));
    const h = Math.floor(mins / 60);
    const m = mins % 60;
    return h > 0 ? `${h}h ${m}m` : `${m}m`;
  }

  /** Parse an ISO timestamp to a local HH:MM string; "—" if unparseable. */
  function formatClock(iso: string): string {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '—';
    return new Date(t).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    });
  }

  /** A normalized, lowercase CLI key — for the color-coded badge class. */
  function cliKey(cli: string): string {
    return (cli || '').trim().toLowerCase();
  }

  // --- Headline numbers ----------------------------------------------------

  const sessions = $derived<ProductivitySessionStat[]>(report.sessions ?? []);

  const sessionCount = $derived(sessions.length);

  /** The true global interval-union — the headline "Total AI time". */
  const measured = $derived(formatMinutes(report.total_active_minutes));

  /**
   * The naive sum of every session's own active_minutes. This is higher
   * than `measured` whenever sessions overlap in wall-clock (parallel
   * agents) — the union collapses that double-counting.
   */
  const rawSumMinutes = $derived(
    sessions.reduce((acc, s) => acc + Math.max(0, s.active_minutes || 0), 0)
  );
  const rawSum = $derived(formatMinutes(rawSumMinutes));

  /** True when overlap actually shrank the number — drives the note copy. */
  const overlapRemoved = $derived(
    rawSumMinutes > Math.round(report.total_active_minutes || 0)
  );

  /** How much wall-clock the union removed, formatted. */
  const overlapSaved = $derived(
    formatMinutes(rawSumMinutes - Math.round(report.total_active_minutes || 0))
  );

  // --- Session list (newest first; capped unless showAll) ------------------

  /** Every session sorted by started_at descending — newest meaningful first. */
  const sortedSessions = $derived.by(() => {
    return [...sessions].sort(
      (a, b) => Date.parse(b.started_at) - Date.parse(a.started_at)
    );
  });

  /**
   * The rows actually rendered. Until the user opts in, this is the top
   * TOP_N by active_minutes (the heaviest contributors) — still newest
   * first within that subset so the list reads chronologically.
   */
  const visibleSessions = $derived.by(() => {
    if (showAll || sortedSessions.length <= TOP_N) return sortedSessions;
    const topIds = new Set(
      [...sortedSessions]
        .sort((a, b) => (b.active_minutes || 0) - (a.active_minutes || 0))
        .slice(0, TOP_N)
        .map((s) => s.session_id)
    );
    return sortedSessions.filter((s) => topIds.has(s.session_id));
  });

  const hasMore = $derived(sortedSessions.length > TOP_N);
</script>

<section class="ad-card pow" aria-label="Proof of work">
  <header class="pow-hd">
    <h2 class="pow-title">Proof of work</h2>
    <p class="pow-lead">
      The headline AI time is the <strong>interval-union</strong> of every
      session's active wall-clock — overlapping parallel agents are counted
      once, not twice.
    </p>
  </header>

  <!-- Union vs sum, side by side -->
  <dl class="pow-figures">
    <div class="pow-fig pow-fig--primary">
      <dt>Measured</dt>
      <dd class="ad-mono ad-tnum pow-fig-v">{measured}</dd>
      <div class="pow-fig-sub">true union of session intervals</div>
    </div>

    <div class="pow-fig">
      <dt>Sessions</dt>
      <dd class="ad-mono ad-tnum pow-fig-v">{sessionCount}</dd>
      <div class="pow-fig-sub">contributing to the total</div>
    </div>

    <div class="pow-fig">
      <dt>Raw session sum</dt>
      <dd class="ad-mono ad-tnum pow-fig-v pow-fig-v--muted">{rawSum}</dd>
      <div class="pow-fig-sub">before overlaps removed</div>
    </div>
  </dl>

  {#if sessionCount > 0}
    <p class="pow-note">
      {#if overlapRemoved}
        Raw session time sums to
        <span class="ad-mono ad-tnum">{rawSum}</span>; overlaps removed —
        the union is <span class="ad-mono ad-tnum">{overlapSaved}</span> shorter
        because sessions ran in parallel wall-clock.
      {:else}
        Raw session time sums to
        <span class="ad-mono ad-tnum">{rawSum}</span> — no overlap detected,
        so the union equals the sum.
      {/if}
    </p>
  {/if}

  <!-- Session evidence list -->
  {#if sessionCount === 0}
    <p class="pow-empty">No sessions in this window.</p>
  {:else}
    <div class="pow-list-hd" aria-hidden="true">
      <span class="pow-col pow-col--cli">CLI</span>
      <span class="pow-col pow-col--repo">Repo</span>
      <span class="pow-col pow-col--span">Span</span>
      <span class="pow-col pow-col--time">Active</span>
      <span class="pow-col pow-col--msgs">Messages</span>
    </div>

    <ul class="pow-list">
      {#each visibleSessions as s, i (s.session_id || i)}
        {@const key = cliKey(s.cli)}
        <li class="pow-row">
          <span
            class="ad-badge pow-cli pow-cli--{key}"
            title={s.session_id}
          >
            <span class="ad-dot pow-cli-dot pow-cli-dot--{key}" aria-hidden="true"
            ></span>
            {s.cli || 'unknown'}
          </span>

          <span class="pow-repo ad-truncate" title={s.repo}>
            {s.repo || '—'}
          </span>

          <span class="pow-span ad-mono ad-tnum">
            {formatClock(s.started_at)}–{formatClock(s.ended_at)}
          </span>

          <span class="pow-time ad-mono ad-tnum">
            {formatMinutes(s.active_minutes)}
          </span>

          <span class="pow-msgs ad-mono ad-tnum">
            {s.message_count} msg{s.message_count === 1 ? '' : 's'}
          </span>
        </li>
      {/each}
    </ul>

    {#if hasMore}
      <button
        type="button"
        class="pow-toggle"
        onclick={() => (showAll = !showAll)}
        aria-expanded={showAll}
      >
        {#if showAll}
          Show top {TOP_N} only
        {:else}
          Show all {sessionCount} sessions
        {/if}
      </button>
    {/if}
  {/if}
</section>

<style>
  .pow {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s4);
    padding: var(--ad-s4) var(--ad-s5);
  }

  /* ---- header ---- */
  .pow-hd {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s2);
  }
  .pow-title {
    margin: 0;
    font-family: var(--ad-font-display);
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--ad-fg);
  }
  .pow-lead {
    margin: 0;
    font-size: var(--ad-fs-sm);
    line-height: var(--ad-lh-base);
    color: var(--ad-muted);
    max-width: 64ch;
  }
  .pow-lead strong {
    color: var(--ad-fg-2);
    font-weight: 600;
  }

  /* ---- union vs sum figures ---- */
  .pow-figures {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--ad-s2);
  }
  .pow-fig {
    flex: 1 1 auto;
    min-width: 132px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--ad-s2) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }
  .pow-fig--primary {
    border-color: color-mix(in oklch, var(--ad-accent) 40%, var(--ad-border));
    background: color-mix(in oklch, var(--ad-accent) 8%, var(--ad-bg-2));
  }
  .pow-fig dt {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-weight: 600;
    color: var(--ad-faint);
  }
  .pow-fig-v {
    margin: 0;
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    color: var(--ad-fg);
  }
  .pow-fig-v--muted {
    color: var(--ad-muted);
  }
  .pow-fig-sub {
    font-size: 10px;
    line-height: var(--ad-lh-base);
    color: var(--ad-muted);
  }

  /* ---- the union-vs-sum explanatory note ---- */
  .pow-note {
    margin: 0;
    padding: var(--ad-s2) var(--ad-s3);
    font-size: var(--ad-fs-xs);
    line-height: var(--ad-lh-base);
    color: var(--ad-muted);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }
  .pow-note .ad-mono {
    color: var(--ad-fg-2);
  }

  /* ---- empty state ---- */
  .pow-empty {
    margin: 0;
    padding: 18px 15px;
    font-size: var(--ad-fs-sm);
    color: var(--ad-faint);
    text-align: center;
  }

  /* ---- session list ---- */
  .pow-list-hd,
  .pow-row {
    display: grid;
    grid-template-columns: 88px minmax(0, 1fr) 116px 78px 78px;
    gap: var(--ad-s3);
    align-items: center;
  }

  .pow-list-hd {
    padding: 0 var(--ad-s2) var(--ad-s2);
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .pow-col {
    font-family: var(--ad-font-mono);
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--ad-faint);
  }
  .pow-col--time,
  .pow-col--msgs {
    text-align: right;
  }

  .pow-list {
    list-style: none;
    margin: 0;
    padding: 0;
    /* 73 sessions is a lot — cap height and let the list scroll. */
    max-height: 340px;
    overflow-y: auto;
  }

  .pow-row {
    padding: var(--ad-s2);
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .pow-row:last-child {
    border-bottom: none;
  }

  /* ---- CLI badge, color-coded ---- */
  .pow-cli {
    justify-self: start;
    text-transform: lowercase;
  }
  .pow-cli--claude {
    color: var(--ad-claude);
    background: color-mix(in oklch, var(--ad-claude) 14%, transparent);
    border-color: color-mix(in oklch, var(--ad-claude) 38%, var(--ad-border));
  }
  .pow-cli--codex {
    color: var(--ad-codex);
    background: color-mix(in oklch, var(--ad-codex) 16%, transparent);
    border-color: color-mix(in oklch, var(--ad-codex) 40%, var(--ad-border));
  }
  .pow-cli-dot--claude {
    background: var(--ad-claude);
  }
  .pow-cli-dot--codex {
    background: var(--ad-codex);
  }

  .pow-repo {
    font-size: var(--ad-fs-sm);
    color: var(--ad-fg-2);
    min-width: 0;
  }

  .pow-span {
    font-size: 11.5px;
    color: var(--ad-muted);
    white-space: nowrap;
  }

  .pow-time {
    font-size: 11.5px;
    font-weight: 600;
    color: var(--ad-fg);
    text-align: right;
  }

  .pow-msgs {
    font-size: 11.5px;
    color: var(--ad-faint);
    text-align: right;
    white-space: nowrap;
  }

  /* ---- show-all toggle ---- */
  .pow-toggle {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    background: transparent;
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
    padding: 5px 11px;
    cursor: pointer;
    font-family: var(--ad-font-mono);
    font-size: 11px;
    color: var(--ad-faint);
    transition:
      color 120ms ease,
      border-color 120ms ease;
  }
  .pow-toggle:hover {
    color: var(--ad-fg-2);
    border-color: var(--ad-border);
  }
</style>
