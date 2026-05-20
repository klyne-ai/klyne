<!--
  SummaryBar — top summary bar of the productivity dashboard.

  A single compact bar: the day heading, a reflection-status pill (with
  optional nudge), and a row of stat tiles aggregated across every
  service/branch in the report. All numbers are $derived so the bar
  stays in sync if the parent swaps in a fresh report.

  House style mirrors the rest of klyne's UI: --ad-* design tokens,
  .ad-card / .ad-badge / .ad-mono / .ad-tnum utilities, scoped <style>.
-->
<script lang="ts">
  import type { ProductivityReport } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  // --- Aggregations across every branch of every service -------------------

  /** Format minutes as "Xh Ym" — clamps negatives, "0h 0m" for empty. */
  function formatMinutes(value: number): string {
    const mins = Math.max(0, Math.round(value || 0));
    const h = Math.floor(mins / 60);
    const m = mins % 60;
    return `${h}h ${m}m`;
  }

  /**
   * Headline "Total AI time" — the backend's true global interval-union
   * of every session's active wall-clock across all CLIs. Replaces the old
   * sum-of-branch-minutes, which double-counted overlapping sessions.
   */
  const totalTime = $derived(formatMinutes(report.total_active_minutes));

  /** Per-CLI global union, ordered claude-first then any others, formatted. */
  const cliBreakdown = $derived.by(() => {
    const by = report.minutes_by_cli ?? {};
    const order = ['claude', 'codex'];
    const keys = [
      ...order.filter((k) => k in by || k === 'claude' || k === 'codex'),
      ...Object.keys(by).filter((k) => !order.includes(k))
    ];
    const seen = new Set<string>();
    const out: { cli: string; label: string; time: string }[] = [];
    for (const k of keys) {
      if (seen.has(k)) continue;
      seen.add(k);
      out.push({
        cli: k,
        label: k.charAt(0).toUpperCase() + k.slice(1),
        time: formatMinutes(by[k] ?? 0)
      });
    }
    return out;
  });

  /** Count of contributing sessions behind the headline number. */
  const sessionCount = $derived(report.sessions?.length ?? 0);

  const serviceCount = $derived(report.services.length);

  const branchCount = $derived(
    report.services.reduce((sum, svc) => sum + svc.branches.length, 0)
  );

  /** Unique user-authored commits — deduped by sha so a commit shared
      across sibling worktree branches is counted once. */
  const commitCount = $derived.by(() => {
    const seen = new Set<string>();
    for (const svc of report.services) {
      for (const br of svc.branches) {
        for (const c of br.commits) {
          if (c.is_user) seen.add(c.sha);
        }
      }
    }
    return seen.size;
  });

  /** Branch counts bucketed by ship state. */
  const shipped = $derived.by(() => {
    const counts = { merged: 0, pushed: 0, local: 0 };
    for (const svc of report.services) {
      for (const br of svc.branches) {
        if (br.ship === 'merged-to-default') counts.merged++;
        else if (br.ship === 'pushed-to-remote') counts.pushed++;
        else if (br.ship === 'committed-local-only') counts.local++;
      }
    }
    return counts;
  });

  const openRisks = $derived(
    report.services.reduce((sum, svc) => sum + svc.risks.length, 0)
  );

  // --- Reflection status ---------------------------------------------------

  const reflectionCurrent = $derived(report.reflection_status === 'current');

  const reflectionLabel = $derived.by(() => {
    switch (report.reflection_status) {
      case 'current':
        return 'reflection current';
      case 'stale':
        return 'reflection stale';
      case 'missing':
        return 'reflection missing';
      default:
        return `reflection ${report.reflection_status || 'unknown'}`;
    }
  });
</script>

<header class="ad-card summary-bar" aria-label="Productivity summary">
  <!-- Row 1: day heading + reflection status -->
  <div class="sb-top">
    <h1 class="sb-title">
      <span class="sb-title-kicker">Productivity</span>
      <span class="sb-title-sep" aria-hidden="true">·</span>
      <span class="ad-mono sb-title-day">{report.day}</span>
    </h1>

    <div class="sb-reflection">
      <span
        class="ad-badge {reflectionCurrent ? 'ad-badge--active' : 'ad-badge--compact'}"
      >
        <span
          class="ad-dot {reflectionCurrent ? 'ad-dot--active' : 'ad-dot--compact'}"
          aria-hidden="true"
        ></span>
        {reflectionLabel}
      </span>
      {#if report.nudge}
        <span class="sb-nudge">{report.nudge}</span>
      {/if}
    </div>
  </div>

  <!-- Row 2: stat tiles -->
  <dl class="sb-tiles">
    <div class="sb-tile sb-tile--wide">
      <dt>Total AI time</dt>
      <dd class="ad-mono ad-tnum sb-tile-v">{totalTime}</dd>
      {#if cliBreakdown.length}
        <div class="sb-cli">
          {#each cliBreakdown as c, i (c.cli)}
            {#if i > 0}
              <span class="sb-cli-sep" aria-hidden="true">·</span>
            {/if}
            <span class="sb-cli-item">
              <span class="sb-cli-l">{c.label}</span>
              <span class="ad-mono ad-tnum">{c.time}</span>
            </span>
          {/each}
        </div>
      {/if}
      {#if sessionCount > 0}
        <div class="sb-cli-cap">
          union of {sessionCount} session{sessionCount === 1 ? '' : 's'} — overlaps removed
        </div>
      {/if}
    </div>

    <div class="sb-tile">
      <dt>Services</dt>
      <dd class="ad-mono ad-tnum sb-tile-v">{serviceCount}</dd>
    </div>

    <div class="sb-tile">
      <dt>Branches</dt>
      <dd class="ad-mono ad-tnum sb-tile-v">{branchCount}</dd>
    </div>

    <div class="sb-tile">
      <dt>Commits</dt>
      <dd class="ad-mono ad-tnum sb-tile-v">{commitCount}</dd>
    </div>

    <div class="sb-tile sb-tile--wide">
      <dt>Shipped</dt>
      <dd class="ad-mono ad-tnum sb-tile-v sb-ship">
        <span>{shipped.merged}<span class="sb-ship-l"> merged</span></span>
        <span class="sb-ship-sep" aria-hidden="true">·</span>
        <span>{shipped.pushed}<span class="sb-ship-l"> pushed</span></span>
        <span class="sb-ship-sep" aria-hidden="true">·</span>
        <span>{shipped.local}<span class="sb-ship-l"> local</span></span>
      </dd>
    </div>

    <div class="sb-tile">
      <dt>Open risks</dt>
      <dd
        class="ad-mono ad-tnum sb-tile-v"
        class:sb-tile-v--alert={openRisks > 0}
      >
        {openRisks}
      </dd>
    </div>
  </dl>
</header>

<style>
  .summary-bar {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s4);
    padding: var(--ad-s4) var(--ad-s5);
  }

  /* --- Row 1 ------------------------------------------------------------ */
  .sb-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--ad-s4);
    flex-wrap: wrap;
  }

  .sb-title {
    margin: 0;
    display: flex;
    align-items: baseline;
    gap: var(--ad-s2);
    font-family: var(--ad-font-display);
    font-size: var(--ad-fs-xl);
    font-weight: 600;
    line-height: var(--ad-lh-tight);
    color: var(--ad-fg);
  }
  .sb-title-kicker {
    letter-spacing: -0.01em;
  }
  .sb-title-sep {
    color: var(--ad-faint);
    font-weight: 400;
  }
  .sb-title-day {
    color: var(--ad-fg-2);
    font-size: var(--ad-fs-lg);
    font-weight: 500;
  }

  .sb-reflection {
    display: flex;
    align-items: center;
    gap: var(--ad-s3);
    flex-wrap: wrap;
    min-width: 0;
  }
  .sb-nudge {
    color: var(--ad-muted);
    font-size: var(--ad-fs-xs);
    line-height: var(--ad-lh-base);
    max-width: 36ch;
  }

  /* --- Row 2: stat tiles ----------------------------------------------- */
  .sb-tiles {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--ad-s2);
  }

  .sb-tile {
    flex: 1 1 auto;
    min-width: 96px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--ad-s2) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }
  .sb-tile--wide {
    flex: 2 1 auto;
    min-width: 180px;
  }

  .sb-tile dt {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-weight: 600;
    color: var(--ad-faint);
  }
  .sb-tile-v {
    margin: 0;
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    color: var(--ad-fg);
  }
  .sb-tile-v--alert {
    color: var(--ad-danger);
  }

  /* --- Per-CLI breakdown under the Total AI time tile ------------------ */
  .sb-cli {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 6px;
    font-size: var(--ad-fs-md);
    color: var(--ad-fg-2);
  }
  .sb-cli-item {
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
  }
  .sb-cli-l {
    color: var(--ad-faint);
    font-weight: 400;
    font-size: var(--ad-fs-xs);
  }
  .sb-cli-sep {
    color: var(--ad-border);
    font-weight: 400;
  }
  .sb-cli-cap {
    color: var(--ad-muted);
    font-size: 10px;
    line-height: var(--ad-lh-base);
  }

  .sb-ship {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: var(--ad-fs-md);
  }
  .sb-ship-l {
    color: var(--ad-faint);
    font-weight: 400;
    font-size: var(--ad-fs-xs);
  }
  .sb-ship-sep {
    color: var(--ad-border);
    font-weight: 400;
  }
</style>
