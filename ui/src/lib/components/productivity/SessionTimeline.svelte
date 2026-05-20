<!--
  SessionTimeline — makes the PARALLELISM behind the headline AI time
  visible. The headline number is a global interval-union of every
  session's active wall-clock; this component shows the structure that
  union collapses:

    1. A lane-packed Gantt chart. Sessions are sorted by start and
       greedily assigned to the first lane whose last session already
       ended — classic interval-partitioning. Lane count = PEAK
       concurrency (the most sessions that ever ran at once).

    2. A concurrency breakdown computed by a sweep-line over every
       [started_at, ended_at] interval: for each level k it reports the
       total wall-clock during which exactly k sessions overlapped.

  Nothing here is "removed" — the overlap is the story. The union is the
  honest elapsed wall-clock; the timeline shows why raw-sum ≠ elapsed.

  House style: --ad-* design tokens, .ad-mono / .ad-tnum utilities,
  scoped <style>, theme-aware, CSS/SVG only.
-->
<script lang="ts">
  import type { ProductivityReport, ProductivitySessionStat } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  // --- formatting helpers --------------------------------------------------

  /** Format minutes as "Xh Ym" — always shows hours so totals line up. */
  function formatHM(value: number): string {
    const mins = Math.max(0, Math.round(value || 0));
    const h = Math.floor(mins / 60);
    const m = mins % 60;
    return `${h}h ${m}m`;
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

  /** A normalized, lowercase CLI key — drives the color class. */
  function cliKey(cli: string): string {
    return (cli || '').trim().toLowerCase();
  }

  // --- normalized, time-valid sessions -------------------------------------

  /**
   * Every session with parseable timestamps and start <= end. Each gets a
   * numeric [start, end] (epoch-ms) so the layout / sweep-line don't keep
   * re-parsing. Sorted by start ascending — required by lane packing.
   */
  interface TimedSession {
    s: ProductivitySessionStat;
    start: number;
    end: number;
    idx: number;
  }

  const timed = $derived.by<TimedSession[]>(() => {
    const list: TimedSession[] = [];
    const raw = report.sessions ?? [];
    for (let i = 0; i < raw.length; i++) {
      const s = raw[i];
      const start = Date.parse(s.started_at);
      let end = Date.parse(s.ended_at);
      if (Number.isNaN(start)) continue;
      if (Number.isNaN(end) || end < start) end = start;
      list.push({ s, start, end, idx: i });
    }
    list.sort((a, b) => a.start - b.start || a.end - b.end);
    return list;
  });

  const hasData = $derived(timed.length > 0);

  // --- window [earliest start, latest end] ---------------------------------

  const windowStart = $derived(
    hasData ? Math.min(...timed.map((t) => t.start)) : 0
  );
  const windowEnd = $derived(
    hasData ? Math.max(...timed.map((t) => t.end)) : 0
  );
  /** Total span in ms; floored at 1 minute so positioning never divides by 0. */
  const spanMs = $derived(Math.max(60_000, windowEnd - windowStart));
  const spanMinutes = $derived((windowEnd - windowStart) / 60_000);

  // --- lane packing (interval partitioning) --------------------------------

  /**
   * Greedy interval partitioning. Walking sessions in start order, each is
   * placed in the first lane whose previous session has already ended
   * (laneEnd <= this.start); otherwise a new lane opens. The number of
   * lanes equals PEAK concurrency — the most sessions ever running at once.
   */
  interface LaidOut extends TimedSession {
    lane: number;
  }

  const layout = $derived.by<{ rows: LaidOut[]; lanes: number }>(() => {
    const laneEnds: number[] = [];
    const rows: LaidOut[] = [];
    for (const t of timed) {
      let lane = -1;
      for (let i = 0; i < laneEnds.length; i++) {
        if (laneEnds[i] <= t.start) {
          lane = i;
          break;
        }
      }
      if (lane === -1) {
        lane = laneEnds.length;
        laneEnds.push(t.end);
      } else {
        laneEnds[lane] = t.end;
      }
      rows.push({ ...t, lane });
    }
    return { rows, lanes: Math.max(1, laneEnds.length) };
  });

  /** Peak concurrency — directly the lane count from interval partitioning. */
  const peakConcurrency = $derived(layout.lanes);

  // --- concurrency sweep-line ----------------------------------------------

  /**
   * Sweep-line over all intervals. Each session emits a +1 event at its
   * start and a −1 event at its end. Sorting the events and walking them
   * left-to-right, the running counter is the number of sessions live in
   * the slice up to the next event. We accumulate that slice's duration
   * into the bucket for the current concurrency level.
   *
   * Result: levels[k] = total wall-clock minutes during which EXACTLY k
   * sessions overlapped. Sum of all levels == the union span.
   */
  interface ConcLevel {
    k: number;
    minutes: number;
  }

  const concurrency = $derived.by<ConcLevel[]>(() => {
    if (!hasData) return [];
    type Ev = { t: number; delta: number };
    const events: Ev[] = [];
    for (const t of timed) {
      events.push({ t: t.start, delta: 1 });
      events.push({ t: t.end, delta: -1 });
    }
    // Process ends before starts at the same instant so a back-to-back
    // handoff doesn't register a phantom +1 of concurrency.
    events.sort((a, b) => a.t - b.t || a.delta - b.delta);

    const byLevel = new Map<number, number>();
    let live = 0;
    let prev = events.length > 0 ? events[0].t : 0;
    for (const ev of events) {
      if (ev.t > prev && live > 0) {
        byLevel.set(live, (byLevel.get(live) ?? 0) + (ev.t - prev));
      }
      live += ev.delta;
      prev = ev.t;
    }
    return [...byLevel.entries()]
      .map(([k, ms]) => ({ k, minutes: ms / 60_000 }))
      .filter((l) => l.minutes >= 0.5)
      .sort((a, b) => a.k - b.k);
  });

  /** Total wall-clock covered by at least one session (the union span). */
  const unionMinutes = $derived(
    concurrency.reduce((acc, l) => acc + l.minutes, 0)
  );

  // --- hour-tick axis ------------------------------------------------------

  /** Hour boundaries inside the window, as { ms, leftPct, label }. */
  const hourTicks = $derived.by(() => {
    if (!hasData) return [] as { leftPct: number; label: string }[];
    const ticks: { leftPct: number; label: string }[] = [];
    const HOUR = 3_600_000;
    // First whole hour at or after windowStart.
    let t = Math.ceil(windowStart / HOUR) * HOUR;
    // Cap the count so a multi-day window doesn't render hundreds of ticks.
    let guard = 0;
    while (t <= windowEnd && guard < 64) {
      ticks.push({
        leftPct: ((t - windowStart) / spanMs) * 100,
        label: new Date(t).toLocaleTimeString([], {
          hour: '2-digit',
          minute: '2-digit',
          hour12: false
        })
      });
      t += HOUR;
      guard++;
    }
    return ticks;
  });

  /** Left/width percentages for one session's bar within the window. */
  function barGeom(t: TimedSession): { leftPct: number; widthPct: number } {
    const leftPct = ((t.start - windowStart) / spanMs) * 100;
    const widthPct = ((t.end - t.start) / spanMs) * 100;
    return { leftPct, widthPct };
  }

  // --- headline numbers (presented as "shown", never "discarded") ----------

  /** The headline union — the true elapsed wall-clock. */
  const unionHeadline = $derived(formatHM(report.total_active_minutes));
  /** Earliest→latest wall-clock span (whether busy or idle). */
  const totalSpan = $derived(formatHM(spanMinutes));

  /** Compact "1× 6h 10m · 2× 4h 05m" string for the breakdown summary. */
  const concurrencySummary = $derived(
    concurrency.map((l) => `${l.k}× ${formatHM(l.minutes)}`).join(' · ')
  );

  /** Per-bar accessible label. */
  function barLabel(t: TimedSession): string {
    const s = t.s;
    return `${s.cli || 'session'} · ${s.repo || 'unknown repo'} · ${formatClock(
      s.started_at
    )}–${formatClock(s.ended_at)} · ${formatHM(s.active_minutes)} active · ${
      s.message_count
    } message${s.message_count === 1 ? '' : 's'}`;
  }

  /** Row height in px for each Gantt lane. */
  const LANE_H = 26;
</script>

<section class="st" aria-label="Session parallelism timeline">
  <header class="st-hd">
    <h3 class="st-title">Parallel session timeline</h3>
    <p class="st-sub">
      Each bar is one AI session. When bars stack, agents ran in parallel —
      that overlap is exactly why the raw per-session sum runs longer than the
      true elapsed wall-clock.
    </p>
  </header>

  {#if !hasData}
    <p class="st-empty">No sessions in this window.</p>
  {:else}
    <!-- Headline stats — overlap is shown, not removed -->
    <dl class="st-stats">
      <div class="st-stat st-stat--peak">
        <dt>Peak parallelism</dt>
        <dd class="ad-mono ad-tnum st-stat-v">
          {peakConcurrency}
          <span class="st-stat-unit"
            >session{peakConcurrency === 1 ? '' : 's'} at once</span
          >
        </dd>
      </div>
      <div class="st-stat">
        <dt>Total span</dt>
        <dd class="ad-mono ad-tnum st-stat-v">{totalSpan}</dd>
        <div class="st-stat-sub">earliest start → latest end</div>
      </div>
      <div class="st-stat">
        <dt>Elapsed AI wall-clock</dt>
        <dd class="ad-mono ad-tnum st-stat-v">{unionHeadline}</dd>
        <div class="st-stat-sub">union of all session intervals</div>
      </div>
    </dl>

    <!-- Lane-packed Gantt -->
    <div class="st-gantt" role="img" aria-label="Gantt chart of {timed.length} sessions across {peakConcurrency} parallel lanes">
      <!-- hour-tick axis -->
      <div class="st-axis" aria-hidden="true">
        {#each hourTicks as tick, i (i)}
          <div class="st-tick" style:left="{tick.leftPct}%">
            <span class="st-tick-line"></span>
            <span class="st-tick-label ad-mono">{tick.label}</span>
          </div>
        {/each}
      </div>

      <!-- lanes -->
      <div
        class="st-lanes"
        style:height="{layout.lanes * LANE_H}px"
      >
        <!-- tick gridlines behind the bars -->
        {#each hourTicks as tick, i (i)}
          <span
            class="st-grid"
            style:left="{tick.leftPct}%"
            aria-hidden="true"
          ></span>
        {/each}

        {#each layout.rows as row, i (row.s.session_id || i)}
          {@const geom = barGeom(row)}
          {@const key = cliKey(row.s.cli)}
          <div
            class="st-bar st-bar--{key}"
            style:left="{geom.leftPct}%"
            style:width="max(3px, {geom.widthPct}%)"
            style:top="{row.lane * LANE_H + 3}px"
            title={barLabel(row)}
            aria-label={barLabel(row)}
          >
            <span class="st-bar-label">{row.s.repo || row.s.cli || '—'}</span>
          </div>
        {/each}
      </div>
    </div>

    <!-- Concurrency breakdown — the "break up" of overlap -->
    {#if concurrency.length > 0}
      <div class="st-conc">
        <div class="st-conc-hd">
          <span class="st-conc-title ad-mono">Concurrency breakdown</span>
          <span class="st-conc-note"
            >wall-clock at each parallelism level — sums to {formatHM(
              unionMinutes
            )} elapsed</span
          >
        </div>

        <!-- stacked proportional bar -->
        <div
          class="st-conc-bar"
          role="img"
          aria-label="Time spent at each concurrency level"
        >
          {#each concurrency as level (level.k)}
            {@const pct =
              unionMinutes > 0 ? (level.minutes / unionMinutes) * 100 : 0}
            <div
              class="st-conc-seg st-conc-seg--{Math.min(level.k, 5)}"
              style:width="{pct}%"
              title="{level.k} session{level.k === 1
                ? ''
                : 's'} in parallel — {formatHM(level.minutes)}"
            ></div>
          {/each}
        </div>

        <!-- legend / list -->
        <ul class="st-conc-list">
          {#each concurrency as level (level.k)}
            <li class="st-conc-item">
              <span
                class="st-conc-swatch st-conc-seg--{Math.min(level.k, 5)}"
                aria-hidden="true"
              ></span>
              <span class="st-conc-k ad-mono ad-tnum">{level.k}×</span>
              <span class="st-conc-v ad-mono ad-tnum"
                >{formatHM(level.minutes)}</span
              >
              <span class="st-conc-desc"
                >{level.k === 1
                  ? 'single session'
                  : `${level.k} sessions in parallel`}</span
              >
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {/if}
</section>

<style>
  .st {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s3);
  }

  /* ---- header ---- */
  .st-hd {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s1);
  }
  .st-title {
    margin: 0;
    font-family: var(--ad-font-display);
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--ad-fg);
  }
  .st-sub {
    margin: 0;
    font-size: var(--ad-fs-xs);
    line-height: var(--ad-lh-base);
    color: var(--ad-muted);
    max-width: 68ch;
  }

  /* ---- empty state ---- */
  .st-empty {
    margin: 0;
    padding: 18px 15px;
    font-size: var(--ad-fs-sm);
    color: var(--ad-faint);
    text-align: center;
  }

  /* ---- headline stats ---- */
  .st-stats {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--ad-s2);
  }
  .st-stat {
    flex: 1 1 auto;
    min-width: 150px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--ad-s2) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }
  .st-stat--peak {
    border-color: color-mix(in oklch, var(--ad-accent) 40%, var(--ad-border));
    background: color-mix(in oklch, var(--ad-accent) 8%, var(--ad-bg-2));
  }
  .st-stat dt {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-weight: 600;
    color: var(--ad-faint);
  }
  .st-stat-v {
    margin: 0;
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    color: var(--ad-fg);
    display: flex;
    align-items: baseline;
    gap: 6px;
  }
  .st-stat-unit {
    font-family: var(--ad-font);
    font-size: var(--ad-fs-xs);
    font-weight: 400;
    color: var(--ad-muted);
  }
  .st-stat-sub {
    font-size: 10px;
    line-height: var(--ad-lh-base);
    color: var(--ad-muted);
  }

  /* ---- gantt ---- */
  .st-gantt {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: var(--ad-s2) var(--ad-s3) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }

  /* hour-tick axis */
  .st-axis {
    position: relative;
    height: 14px;
  }
  .st-tick {
    position: absolute;
    top: 0;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    align-items: center;
  }
  .st-tick-label {
    font-size: 9px;
    color: var(--ad-faint);
    white-space: nowrap;
    letter-spacing: 0.02em;
  }

  /* lane area */
  .st-lanes {
    position: relative;
    width: 100%;
  }

  /* vertical gridlines aligned to hour ticks */
  .st-grid {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 1px;
    background: var(--ad-border-soft);
    opacity: 0.6;
  }

  /* one session bar */
  .st-bar {
    position: absolute;
    height: 20px;
    border-radius: 4px;
    display: flex;
    align-items: center;
    overflow: hidden;
    padding: 0 5px;
    box-sizing: border-box;
    border: 1px solid transparent;
    cursor: default;
    transition: filter 120ms ease;
  }
  .st-bar:hover {
    filter: brightness(1.12);
    z-index: 2;
  }
  .st-bar-label {
    font-family: var(--ad-font-mono);
    font-size: 9.5px;
    font-weight: 600;
    line-height: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* CLI-coded bar colors */
  .st-bar--claude {
    background: color-mix(in oklch, var(--ad-claude) 36%, var(--ad-bg-2));
    border-color: color-mix(in oklch, var(--ad-claude) 60%, var(--ad-border));
    color: var(--ad-claude);
  }
  .st-bar--codex {
    background: color-mix(in oklch, var(--ad-codex) 36%, var(--ad-bg-2));
    border-color: color-mix(in oklch, var(--ad-codex) 60%, var(--ad-border));
    color: var(--ad-codex);
  }
  /* fallback for any other / unknown CLI */
  .st-bar:not(.st-bar--claude):not(.st-bar--codex) {
    background: color-mix(in oklch, var(--ad-accent-2) 32%, var(--ad-bg-2));
    border-color: color-mix(in oklch, var(--ad-accent-2) 55%, var(--ad-border));
    color: var(--ad-accent-2);
  }

  /* ---- concurrency breakdown ---- */
  .st-conc {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s2);
    padding: var(--ad-s2) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }
  .st-conc-hd {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 6px;
  }
  .st-conc-title {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-weight: 600;
    color: var(--ad-faint);
  }
  .st-conc-note {
    font-size: 10.5px;
    color: var(--ad-muted);
  }

  /* stacked proportional bar */
  .st-conc-bar {
    display: flex;
    height: 18px;
    width: 100%;
    border-radius: var(--ad-r-sm);
    overflow: hidden;
    border: 1px solid var(--ad-border-soft);
    background: var(--ad-bg);
  }
  .st-conc-seg {
    height: 100%;
    min-width: 2px;
  }

  /* concurrency-level palette — escalating intensity */
  .st-conc-seg--1 {
    background: color-mix(in oklch, var(--ad-accent) 30%, var(--ad-panel));
  }
  .st-conc-seg--2 {
    background: color-mix(in oklch, var(--ad-accent) 55%, var(--ad-panel));
  }
  .st-conc-seg--3 {
    background: var(--ad-accent);
  }
  .st-conc-seg--4 {
    background: color-mix(in oklch, var(--ad-warn) 80%, var(--ad-accent));
  }
  .st-conc-seg--5 {
    background: var(--ad-danger);
  }

  /* legend / list */
  .st-conc-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--ad-s1) var(--ad-s4);
  }
  .st-conc-item {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .st-conc-swatch {
    width: 10px;
    height: 10px;
    border-radius: 2px;
    flex: none;
  }
  .st-conc-k {
    font-size: 11.5px;
    font-weight: 600;
    color: var(--ad-fg);
  }
  .st-conc-v {
    font-size: 11.5px;
    font-weight: 600;
    color: var(--ad-fg-2);
  }
  .st-conc-desc {
    font-size: 10.5px;
    color: var(--ad-muted);
  }
</style>
