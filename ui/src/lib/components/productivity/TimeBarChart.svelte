<!--
  TimeBarChart — "time per service" horizontal bar chart. Shows how much
  attributed AI session time landed in each repo for a productivity report.
  CSS bars, theme-token colors — matches klyne's insights chart idiom.
-->
<script lang="ts">
  import type { ProductivityReport, ProductivityService } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }
  const { report }: Props = $props();

  interface ServiceTime {
    repo: string;
    minutes: number;
  }

  /** Sum attributed AI minutes across every branch of a service. */
  function serviceMinutes(svc: ProductivityService): number {
    return svc.branches.reduce((sum, b) => sum + (b.attributed_minutes || 0), 0);
  }

  // Services with AI session time, sorted descending. These get bars.
  const ranked: ServiceTime[] = $derived.by(() => {
    const items = report.services
      .map((s) => ({ repo: s.repo, minutes: serviceMinutes(s) }))
      .filter((s) => s.minutes > 0);
    items.sort((a, b) => b.minutes - a.minutes);
    return items;
  });

  // Services with no attributed time — listed greyed at the bottom.
  const idle: ServiceTime[] = $derived.by(() =>
    report.services
      .map((s) => ({ repo: s.repo, minutes: serviceMinutes(s) }))
      .filter((s) => s.minutes <= 0)
  );

  // Bar widths are proportional to minutes / max(minutes). A single
  // service (or any service equal to the max) fills the bar at 100%.
  const maxMinutes: number = $derived(Math.max(1, ...ranked.map((s) => s.minutes)));
  const totalMinutes: number = $derived(ranked.reduce((sum, s) => sum + s.minutes, 0));

  /** Format a minute count as "Xh Ym" (drops the hours part when zero). */
  function fmtDuration(mins: number): string {
    const m = Math.round(mins);
    const h = Math.floor(m / 60);
    const rem = m % 60;
    if (h <= 0) return `${rem}m`;
    return `${h}h ${rem}m`;
  }
</script>

<div class="chart-card enter enter-2">
  <div class="row" style="justify-content: space-between; align-items: baseline; margin-bottom: 14px;">
    <div>
      <h3 style="margin: 0;">Time per service</h3>
      <div class="sub" style="margin: 0;">attributed AI session time · per repo</div>
    </div>
    {#if ranked.length > 0}
      <span class="mono" style="font-size: 11px; color: var(--ad-fg-2);">
        {fmtDuration(totalMinutes)} total
      </span>
    {/if}
  </div>

  {#if ranked.length === 0}
    <div
      class="faint mono"
      style="padding: 22px 12px; text-align: center; font-size: 12px;"
    >
      No AI session time in this window.
    </div>
  {:else}
    <div style="display: flex; flex-direction: column; gap: 9px;">
      {#each ranked as svc, i (i)}
        {@const widthPct = (svc.minutes / maxMinutes) * 100}
        <div
          style="display: grid; grid-template-columns: 150px 1fr; gap: 12px; align-items: center;"
        >
          <span
            class="mono"
            style="font-size: 12px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; letter-spacing: -0.005em;"
            title={svc.repo}
          >
            {svc.repo}
          </span>
          <div
            style="height: 18px; position: relative; background: var(--ad-bg-2); border-radius: 5px; border: 1px solid var(--ad-border-soft);"
            role="img"
            aria-label="{svc.repo}: {fmtDuration(svc.minutes)} of AI session time"
          >
            <div
              style="position: absolute; inset: 0; width: {widthPct}%; min-width: 3px; background: var(--ad-claude); border-radius: 4px; transition: width 700ms cubic-bezier(.2,.8,.2,1);"
            ></div>
            <span
              class="mono tnum"
              style="position: absolute; top: 0; bottom: 0; left: calc({widthPct}% + 8px); display: flex; align-items: center; font-size: 11px; font-weight: 600; color: var(--ad-fg-2); white-space: nowrap;"
            >
              {fmtDuration(svc.minutes)}
            </span>
          </div>
        </div>
      {/each}
    </div>

    {#if idle.length > 0}
      <div class="hr" style="margin: 14px 0 10px;"></div>
      <div style="display: flex; flex-direction: column; gap: 7px;">
        {#each idle as svc, i (i)}
          <div
            style="display: grid; grid-template-columns: 150px 1fr; gap: 12px; align-items: center;"
          >
            <span
              class="mono faint"
              style="font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
              title={svc.repo}
            >
              {svc.repo}
            </span>
            <span class="mono faint" style="font-size: 11px;">(no AI session)</span>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>
