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
  import SessionTimeline from './SessionTimeline.svelte';

  interface Props {
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  /** When false, only the top N sessions by active_minutes are shown. */
  let showAll = $state(false);

  /** How many sessions to show before the "show all" toggle kicks in. */
  const TOP_N = 12;

  /** Per-session is the default — the granular evidence; per-repo
   *  collapses each repo's sessions into one row with a union-active
   *  total so duplicate-looking rows on the same project disappear. */
  let groupBy = $state<'session' | 'repo'>('session');

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

  /** True when sessions overlapped — i.e. agents ran in parallel. */
  const hasOverlap = $derived(
    rawSumMinutes > Math.round(report.total_active_minutes || 0)
  );

  /**
   * The amount of parallel work — wall-clock minutes that two or more
   * sessions shared. It is the gap between raw sum and elapsed union, and
   * it is *shown* in the timeline below, not discarded.
   */
  const parallelMinutes = $derived(
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

  // --- per-repo rollup (groupBy='repo') ------------------------------------

  /** One row per repo when the user picks "By repo". Active is the
   *  interval UNION of the repo's sessions (not naive sum) so when two
   *  sessions ran in parallel on the same repo we don't double-count. */
  interface RepoRow {
    repo: string;
    sessions: number;
    /** cli → number of sessions for that CLI on this repo. */
    cliCounts: Array<[string, number]>;
    /** Earliest started_at across the repo's sessions (epoch-ms). */
    startedAt: number;
    /** Latest ended_at across the repo's sessions (epoch-ms). */
    endedAt: number;
    /** Active minutes — union of every active interval on this repo. */
    activeMinutes: number;
    /** Total in-window messages across the repo. */
    messages: number;
  }

  /** Compute the union (in ms) of a list of [start, end) intervals.
   *  Sort + sweep — handles overlap correctly so parallel sessions on
   *  the same repo aren't double-counted. */
  function unionMs(intervals: Array<[number, number]>): number {
    if (intervals.length === 0) return 0;
    const sorted = [...intervals].sort((a, b) => a[0] - b[0]);
    let total = 0;
    let curStart = sorted[0][0];
    let curEnd = sorted[0][1];
    for (let i = 1; i < sorted.length; i++) {
      const [a, b] = sorted[i];
      if (a > curEnd) {
        total += curEnd - curStart;
        curStart = a;
        curEnd = b;
      } else if (b > curEnd) {
        curEnd = b;
      }
    }
    total += curEnd - curStart;
    return total;
  }

  const repoRows = $derived.by<RepoRow[]>(() => {
    const byRepo = new Map<string, ProductivitySessionStat[]>();
    for (const s of sessions) {
      const key = s.repo || '—';
      const bucket = byRepo.get(key);
      if (bucket) bucket.push(s);
      else byRepo.set(key, [s]);
    }
    const rows: RepoRow[] = [];
    for (const [repo, list] of byRepo) {
      const starts: number[] = [];
      const ends: number[] = [];
      const intervals: Array<[number, number]> = [];
      const cliMap = new Map<string, number>();
      let messages = 0;
      for (const s of list) {
        const st = Date.parse(s.started_at);
        const en = Date.parse(s.ended_at);
        if (!Number.isNaN(st)) starts.push(st);
        if (!Number.isNaN(en)) ends.push(en);
        for (const iv of s.active_intervals ?? []) {
          const a = Date.parse(iv.start);
          const b = Date.parse(iv.end);
          if (!Number.isNaN(a) && !Number.isNaN(b) && b > a) {
            intervals.push([a, b]);
          }
        }
        const ck = (s.cli || 'unknown').toLowerCase();
        cliMap.set(ck, (cliMap.get(ck) ?? 0) + 1);
        messages += s.message_count || 0;
      }
      rows.push({
        repo,
        sessions: list.length,
        cliCounts: [...cliMap.entries()].sort((a, b) => b[1] - a[1]),
        startedAt: starts.length ? Math.min(...starts) : 0,
        endedAt: ends.length ? Math.max(...ends) : 0,
        activeMinutes: Math.round(unionMs(intervals) / 60_000),
        messages
      });
    }
    return rows.sort((a, b) => b.activeMinutes - a.activeMinutes);
  });
</script>

<section class="ad-card pow" aria-label="Proof of work">
  <header class="pow-hd">
    <h2 class="pow-title">Proof of work</h2>
    <p class="pow-lead">
      The headline AI time is the <strong>true elapsed wall-clock</strong> across
      every session. When agents run in parallel the raw per-session sum runs
      longer than real time — the timeline below shows that parallel structure
      in full.
    </p>
  </header>

  <!-- Union vs sum, side by side -->
  <dl class="pow-figures">
    <div class="pow-fig pow-fig--primary">
      <dt>Elapsed AI time</dt>
      <dd class="ad-mono ad-tnum pow-fig-v">{measured}</dd>
      <div class="pow-fig-sub">true wall-clock across all sessions</div>
    </div>

    <div class="pow-fig">
      <dt>Sessions</dt>
      <dd class="ad-mono ad-tnum pow-fig-v">{sessionCount}</dd>
      <div class="pow-fig-sub">contributing to the total</div>
    </div>

    <div class="pow-fig">
      <dt>Raw session sum</dt>
      <dd class="ad-mono ad-tnum pow-fig-v pow-fig-v--muted">{rawSum}</dd>
      <div class="pow-fig-sub">work done across parallel agents</div>
    </div>
  </dl>

  {#if sessionCount > 0}
    <p class="pow-note">
      {#if hasOverlap}
        Per-session work sums to
        <span class="ad-mono ad-tnum">{rawSum}</span>, while only
        <span class="ad-mono ad-tnum">{measured}</span> of wall-clock actually
        elapsed — <span class="ad-mono ad-tnum">{parallelMinutes}</span> of that
        work happened <strong>in parallel</strong>. That parallelism is the
        productivity story: the timeline below breaks down exactly how many
        sessions ran at once, and for how long.
      {:else}
        Per-session work sums to
        <span class="ad-mono ad-tnum">{rawSum}</span> — sessions ran one at a
        time, so the elapsed wall-clock equals the sum.
      {/if}
    </p>
  {/if}

  <!-- Parallelism made visible: lane-packed Gantt + concurrency breakdown -->
  <SessionTimeline {report} />

  <!-- Session evidence list -->
  {#if sessionCount === 0}
    <p class="pow-empty">No sessions in this window.</p>
  {:else}
    <!-- view toggle: per-session evidence vs per-repo rollup -->
    <div class="pow-view" role="tablist" aria-label="Group sessions by">
      <button
        type="button"
        class="pow-view-btn"
        class:pow-view-btn--active={groupBy === 'session'}
        aria-pressed={groupBy === 'session'}
        onclick={() => (groupBy = 'session')}
      >
        By session
        <span class="pow-view-count ad-mono ad-tnum">{sessionCount}</span>
      </button>
      <button
        type="button"
        class="pow-view-btn"
        class:pow-view-btn--active={groupBy === 'repo'}
        aria-pressed={groupBy === 'repo'}
        onclick={() => (groupBy = 'repo')}
      >
        By repo
        <span class="pow-view-count ad-mono ad-tnum">{repoRows.length}</span>
      </button>
    </div>

    {#if groupBy === 'session'}
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
    {:else}
      <!-- per-repo rollup: one row per repo, active is interval-UNION -->
      <div class="pow-list-hd pow-list-hd--repo" aria-hidden="true">
        <span class="pow-col pow-col--cli">CLI</span>
        <span class="pow-col pow-col--repo">Repo</span>
        <span class="pow-col pow-col--sess">Sessions</span>
        <span class="pow-col pow-col--span">Span</span>
        <span class="pow-col pow-col--time">Active</span>
        <span class="pow-col pow-col--msgs">Messages</span>
      </div>

      <ul class="pow-list">
        {#each repoRows as r, i (r.repo + '|' + i)}
          <li class="pow-row pow-row--repo">
            <span class="pow-cli-mix">
              {#each r.cliCounts as [cli, count] (cli)}
                {@const k = cliKey(cli)}
                <span
                  class="ad-badge pow-cli pow-cli--{k}"
                  title="{count} {cli} session{count === 1 ? '' : 's'}"
                >
                  <span class="ad-dot pow-cli-dot pow-cli-dot--{k}" aria-hidden="true"></span>
                  {cli}{count > 1 ? `·${count}` : ''}
                </span>
              {/each}
            </span>

            <span class="pow-repo ad-truncate" title={r.repo}>
              {r.repo || '—'}
            </span>

            <span class="pow-sess ad-mono ad-tnum">
              {r.sessions}
            </span>

            <span class="pow-span ad-mono ad-tnum">
              {formatClock(new Date(r.startedAt).toISOString())}–{formatClock(
                new Date(r.endedAt).toISOString()
              )}
            </span>

            <span class="pow-time ad-mono ad-tnum">
              {formatMinutes(r.activeMinutes)}
            </span>

            <span class="pow-msgs ad-mono ad-tnum">
              {r.messages} msg{r.messages === 1 ? '' : 's'}
            </span>
          </li>
        {/each}
      </ul>
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
  .pow-note strong {
    color: var(--ad-fg-2);
    font-weight: 600;
  }

  /* ---- empty state ---- */
  .pow-empty {
    margin: 0;
    padding: 18px 15px;
    font-size: var(--ad-fs-sm);
    color: var(--ad-faint);
    text-align: center;
  }

  /* ---- view-mode toggle (session / repo) ---- */
  .pow-view {
    display: inline-flex;
    align-self: flex-start;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
    padding: 2px;
    gap: 2px;
  }
  .pow-view-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font: inherit;
    font-size: 11.5px;
    font-weight: 500;
    color: var(--ad-faint);
    background: transparent;
    border: 0;
    border-radius: 5px;
    padding: 4px 10px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
  }
  .pow-view-btn:hover {
    color: var(--ad-fg-2);
  }
  .pow-view-btn--active {
    color: var(--ad-fg);
    background: var(--ad-panel);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
  }
  .pow-view-count {
    font-size: 10px;
    color: var(--ad-faint);
    background: var(--ad-bg);
    border: 1px solid var(--ad-border-soft);
    border-radius: 4px;
    padding: 0 5px;
    min-width: 18px;
    text-align: center;
  }
  .pow-view-btn--active .pow-view-count {
    color: var(--ad-fg-2);
    border-color: var(--ad-border);
  }

  /* ---- session list ---- */
  .pow-list-hd,
  .pow-row {
    display: grid;
    grid-template-columns: 88px minmax(0, 1fr) 116px 78px 78px;
    gap: var(--ad-s3);
    align-items: center;
  }

  /* per-repo rollup adds a Sessions column between Repo and Span. */
  .pow-list-hd--repo,
  .pow-row--repo {
    grid-template-columns: minmax(140px, auto) minmax(0, 1fr) 64px 116px 78px 78px;
  }

  .pow-cli-mix {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .pow-sess {
    font-size: 11.5px;
    font-weight: 600;
    color: var(--ad-fg-2);
    text-align: right;
  }
  .pow-col--sess {
    text-align: right;
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
