<!--
  SessionTimeline — makes the PARALLELISM behind the headline AI time
  visible, and does so with the SAME measure as the headline. The
  headline number is a global interval-union of every session's
  gap-capped ACTIVE intervals; this component is built from those exact
  active intervals so its concurrency breakdown sums to it:

    1. A lane-packed Gantt chart. Each session is one lane row. Its
       presence span (started_at→ended_at) is a faint hairline track;
       its active_intervals are drawn as solid segments on top, so idle
       gaps are visible. Lane packing still uses the presence span so a
       session stays in one lane (classic interval-partitioning).

    2. A concurrency breakdown computed by a sweep-line over the flat
       list of every session's ACTIVE INTERVALS: for each level k it
       reports the total wall-clock during which exactly k intervals
       overlapped. Those level totals sum to total_active_minutes — the
       headline elapsed wall-clock — by construction.

  Nothing here is "removed" — the overlap is the story. The union is the
  honest elapsed wall-clock; the timeline shows the active segments and
  why raw-sum ≠ elapsed.

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
   * Every session with a parseable presence span (started_at <= ended_at).
   * `start`/`end` are the PRESENCE span (epoch-ms) — used for lane packing
   * and the faint track. `active` is the session's gap-capped active
   * sub-intervals (epoch-ms), parsed from active_intervals — the SAME
   * intervals whose union is report.total_active_minutes. Sorted by start
   * ascending, as lane packing requires.
   */
  interface MsInterval {
    start: number;
    end: number;
  }
  interface TimedSession {
    s: ProductivitySessionStat;
    start: number;
    end: number;
    active: MsInterval[];
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
      // Parse the session's gap-capped active sub-intervals; clamp each
      // to the presence span so a malformed interval can't escape it.
      const active: MsInterval[] = [];
      for (const iv of s.active_intervals ?? []) {
        const a = Date.parse(iv.start);
        const b = Date.parse(iv.end);
        if (Number.isNaN(a) || Number.isNaN(b) || b <= a) continue;
        active.push({
          start: Math.max(start, a),
          end: Math.min(end, b)
        });
      }
      list.push({ s, start, end, active, idx: i });
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
   * Greedy interval partitioning over the PRESENCE spans. Walking
   * sessions in start order, each is placed in the first lane whose
   * previous session has already ended (laneEnd <= this.start); otherwise
   * a new lane opens. Packing uses started_at→ended_at (presence) so a
   * session never spills across lanes even though its drawn segments are
   * the gap-capped active intervals.
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

  /**
   * Peak parallelism — the maximum number of ACTIVE intervals overlapping
   * at any instant. This is the max k over the active-interval sweep, not
   * the lane count (lanes can over-count because they pack by presence
   * span, which includes idle gaps).
   */
  const peakConcurrency = $derived(
    concurrencyRaw.reduce((m, l) => Math.max(m, l.k), 0)
  );

  // --- concurrency sweep-line ----------------------------------------------

  /**
   * Sweep-line over the flat list of every session's ACTIVE INTERVALS
   * (not presence spans). Each active interval emits a +1 event at its
   * start and a −1 event at its end. Walking the sorted events
   * left-to-right, the running counter is the number of active intervals
   * live in the slice up to the next event. We accumulate that slice's
   * duration into the bucket for the current concurrency level.
   *
   * Result: levels[k] = total wall-clock minutes during which EXACTLY k
   * active intervals overlapped. Because every active interval here is a
   * sub-interval of the SAME set whose union is report.total_active_minutes,
   * the sum of all levels equals total_active_minutes (the headline) — see
   * the runtime check in `concurrencyConsistent` below.
   */
  interface ConcLevel {
    k: number;
    minutes: number;
  }

  /** Raw (unfiltered) per-level sweep result over all active intervals. */
  const concurrencyRaw = $derived.by<ConcLevel[]>(() => {
    if (!hasData) return [];
    type Ev = { t: number; delta: number };
    const events: Ev[] = [];
    for (const t of timed) {
      for (const iv of t.active) {
        events.push({ t: iv.start, delta: 1 });
        events.push({ t: iv.end, delta: -1 });
      }
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
      .sort((a, b) => a.k - b.k);
  });

  /**
   * Total wall-clock covered by at least one ACTIVE interval — the union
   * of all active intervals. This is the same quantity the backend
   * reports as total_active_minutes; we use the raw (unfiltered) sweep so
   * the equality holds exactly.
   */
  const unionMinutes = $derived(
    concurrencyRaw.reduce((acc, l) => acc + l.minutes, 0)
  );

  /**
   * Consistency assertion: the active-interval sweep must reconstruct the
   * headline. Allow 1 minute of slack for whole-minute rounding on the
   * backend. A mismatch means the timeline and headline diverged — the
   * exact bug this component was reworked to prevent — so we surface it.
   */
  const concurrencyConsistent = $derived(
    Math.abs(unionMinutes - (report.total_active_minutes || 0)) <= 1
  );

  $effect(() => {
    if (hasData && !concurrencyConsistent) {
      console.warn(
        '[SessionTimeline] concurrency sweep',
        Math.round(unionMinutes),
        'min != report.total_active_minutes',
        report.total_active_minutes
      );
    }
  });

  /** Display levels — tiny slivers folded out of the legend/list. */
  const concurrency = $derived(
    concurrencyRaw.filter((l) => l.minutes >= 0.5)
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

  /**
   * Left/width percentages for the faint PRESENCE track of a session
   * (started_at→ended_at, idle gaps included) — the hairline behind the
   * solid active segments.
   */
  function trackGeom(t: TimedSession): { leftPct: number; widthPct: number } {
    const leftPct = ((t.start - windowStart) / spanMs) * 100;
    const widthPct = ((t.end - t.start) / spanMs) * 100;
    return { leftPct, widthPct };
  }

  /**
   * Left/width percentages for one ACTIVE sub-interval of a session,
   * positioned within the same window — the solid drawn segments.
   */
  function segGeom(iv: MsInterval): { leftPct: number; widthPct: number } {
    const leftPct = ((iv.start - windowStart) / spanMs) * 100;
    const widthPct = ((iv.end - iv.start) / spanMs) * 100;
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

  /** Per-row accessible label — covers the whole session lane row. */
  function barLabel(t: TimedSession): string {
    const s = t.s;
    const segs = t.active.length;
    const segNote =
      segs > 1 ? ` across ${segs} active segments` : segs === 1 ? '' : ' (no active span)';
    return `${s.cli || 'session'} · ${s.repo || 'unknown repo'} · ${formatClock(
      s.started_at
    )}–${formatClock(s.ended_at)} presence · ${formatHM(
      s.active_minutes
    )} active${segNote} · ${s.message_count} message${
      s.message_count === 1 ? '' : 's'
    }`;
  }

  /** Accessible label for a single active segment within a session row. */
  function segLabel(t: TimedSession, iv: MsInterval): string {
    return `${t.s.cli || 'session'} active ${formatClock(
      new Date(iv.start).toISOString()
    )}–${formatClock(new Date(iv.end).toISOString())}`;
  }

  /** Row height in px for each Gantt lane. */
  const LANE_H = 26;
</script>

<section class="st" aria-label="Session parallelism timeline">
  <header class="st-hd">
    <h3 class="st-title">Parallel session timeline</h3>
    <p class="st-sub">
      Each row is one AI session. Solid segments are gap-capped active
      wall-clock; the faint track behind them is the session's full
      presence span, so idle gaps show as breaks. When segments stack,
      agents ran in parallel — and the concurrency breakdown below sums to
      exactly the elapsed AI wall-clock.
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
        <dt>Window span</dt>
        <dd class="ad-mono ad-tnum st-stat-v">{totalSpan}</dd>
        <div class="st-stat-sub">
          earliest start → latest end (includes idle time)
        </div>
      </div>
      <div class="st-stat">
        <dt>Elapsed AI wall-clock</dt>
        <dd class="ad-mono ad-tnum st-stat-v">{unionHeadline}</dd>
        <div class="st-stat-sub">union of all active intervals</div>
      </div>
    </dl>

    <!-- Lane-packed Gantt — faint presence track + solid active segments -->
    <div
      class="st-gantt"
      role="img"
      aria-label="Gantt chart of {timed.length} sessions; bars show gap-capped active segments over faint presence tracks"
    >
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
          {@const track = trackGeom(row)}
          {@const key = cliKey(row.s.cli)}
          {@const top = row.lane * LANE_H + 3}
          <!-- faint hairline presence track (started_at → ended_at) -->
          <div
            class="st-track st-track--{key}"
            style:left="{track.leftPct}%"
            style:width="max(3px, {track.widthPct}%)"
            style:top="{top + 8}px"
            title={barLabel(row)}
            aria-label={barLabel(row)}
          ></div>

          {#if row.active.length === 0}
            <!-- no measurable active span — a presence-only marker -->
            <div
              class="st-bar st-bar--idle st-bar--{key}"
              style:left="{track.leftPct}%"
              style:width="max(3px, {track.widthPct}%)"
              style:top="{top}px"
              aria-hidden="true"
            >
              <span class="st-bar-label">{row.s.repo || row.s.cli || '—'}</span>
            </div>
          {:else}
            {#each row.active as iv, j (j)}
              {@const seg = segGeom(iv)}
              <div
                class="st-bar st-bar--{key}"
                style:left="{seg.leftPct}%"
                style:width="max(3px, {seg.widthPct}%)"
                style:top="{top}px"
                title={segLabel(row, iv)}
                aria-label={segLabel(row, iv)}
              >
                {#if j === 0}
                  <span class="st-bar-label"
                    >{row.s.repo || row.s.cli || '—'}</span
                  >
                {/if}
              </div>
            {/each}
          {/if}
        {/each}
      </div>
    </div>

    <!-- Concurrency breakdown — built from the SAME active intervals -->
    {#if concurrency.length > 0}
      <div class="st-conc">
        <div class="st-conc-hd">
          <span class="st-conc-title ad-mono">Concurrency breakdown</span>
          <span class="st-conc-note"
            >wall-clock at each parallelism level — sums to the {unionHeadline}
            elapsed AI wall-clock
            {#if !concurrencyConsistent}
              <span class="st-conc-warn" title="sweep total {formatHM(
                unionMinutes
              )} does not match the headline"
                >(≠ headline — check data)</span
              >
            {/if}</span
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
          <!-- explicit total — must equal the elapsed AI wall-clock -->
          <li class="st-conc-item st-conc-item--total">
            <span class="st-conc-k ad-mono">=</span>
            <span class="st-conc-v ad-mono ad-tnum"
              >{formatHM(unionMinutes)}</span
            >
            <span class="st-conc-desc">elapsed AI wall-clock</span>
          </li>
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

  /* faint hairline presence track (started_at → ended_at) behind the
     solid active segments — shows idle gaps as the bare track */
  .st-track {
    position: absolute;
    height: 4px;
    border-radius: 2px;
    background: var(--ad-border-soft);
    opacity: 0.7;
    cursor: default;
    z-index: 0;
  }
  .st-track--claude {
    background: color-mix(in oklch, var(--ad-claude) 22%, var(--ad-border-soft));
  }
  .st-track--codex {
    background: color-mix(in oklch, var(--ad-codex) 22%, var(--ad-border-soft));
  }

  /* one active-interval segment */
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
    z-index: 1;
  }
  /* presence-only marker: a session with no measurable active span */
  .st-bar--idle {
    background: transparent !important;
    border-style: dashed;
    opacity: 0.55;
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
  .st-conc-warn {
    color: var(--ad-danger);
    font-weight: 600;
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
  /* the explicit "= Xh Ym" total row */
  .st-conc-item--total {
    padding-left: var(--ad-s2);
    border-left: 1px solid var(--ad-border-soft);
  }
  .st-conc-item--total .st-conc-k {
    color: var(--ad-faint);
  }
  .st-conc-item--total .st-conc-v {
    color: var(--ad-fg);
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
