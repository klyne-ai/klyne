<!--
  RiskPanel — "Open Loops" risk surface for the productivity dashboard.

  Flattens every ProductivityRisk across every service into one list (each
  item keeps its owning repo name), groups by kind, and renders a compact,
  prominent panel. amber = unpushed work, red = done-but-uncommitted work.
  When nothing is at risk it shows a calm positive state instead.
-->
<script lang="ts">
  import type { ProductivityReport, ProductivityRisk } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }
  const { report }: Props = $props();

  /** A risk lifted out of its service, carrying the repo it belongs to. */
  interface FlatRisk extends ProductivityRisk {
    repo: string;
  }

  /** kind → display config. Keeps the vocabulary consistent and gives each
   *  group its accent colour, sub-heading, and ordering. */
  const KIND_META: Record<
    string,
    { label: string; accent: string; tint: string; order: number }
  > = {
    'done-uncommitted': {
      label: 'Done but uncommitted',
      accent: 'var(--ad-danger)',
      tint: 'color-mix(in oklch, var(--ad-danger) 12%, transparent)',
      order: 0
    },
    unpushed: {
      label: 'Committed but unpushed',
      accent: 'var(--ad-warn)',
      tint: 'color-mix(in oklch, var(--ad-warn) 12%, transparent)',
      order: 1
    }
  };

  function metaFor(kind: string) {
    return (
      KIND_META[kind] ?? {
        label: kind,
        accent: 'var(--ad-faint)',
        tint: 'color-mix(in oklch, var(--ad-faint) 12%, transparent)',
        order: 99
      }
    );
  }

  /** Flatten all risks across every service, tagging each with its repo. */
  const allRisks: FlatRisk[] = $derived(
    (report.services ?? []).flatMap((svc) =>
      (svc.risks ?? []).map((risk) => ({ ...risk, repo: svc.repo }))
    )
  );

  /** Group the flat list by kind, sorted by severity (done-uncommitted first). */
  const groups: { kind: string; risks: FlatRisk[] }[] = $derived.by(() => {
    const byKind = new Map<string, FlatRisk[]>();
    for (const r of allRisks) {
      const bucket = byKind.get(r.kind);
      if (bucket) bucket.push(r);
      else byKind.set(r.kind, [r]);
    }
    return [...byKind.entries()]
      .map(([kind, risks]) => ({ kind, risks }))
      .sort((a, b) => metaFor(a.kind).order - metaFor(b.kind).order);
  });

  /** Format a minutes count as "~Xh Ym ago" (drops the hour part when zero). */
  function ageLabel(minutes: number): string {
    const h = Math.floor(minutes / 60);
    const m = minutes % 60;
    return h > 0 ? `~${h}h ${m}m ago` : `~${m}m ago`;
  }
</script>

<section
  class="card"
  aria-labelledby="open-loops-title"
  style="overflow: hidden;"
>
  <div class="card-hd" style="justify-content: space-between;">
    <div style="display: flex; align-items: center; gap: 8px; min-width: 0;">
      <span aria-hidden="true" style="font-size: 13px;">🌀</span>
      <h2
        id="open-loops-title"
        style="margin: 0; font-size: 13px; font-weight: 600; letter-spacing: -0.005em;"
      >
        Open Loops
      </h2>
    </div>
    {#if allRisks.length > 0}
      <span
        class="mono"
        style="font-size: 11px; font-weight: 600; color: var(--ad-danger);"
        aria-label="{allRisks.length} items at risk"
      >
        {allRisks.length} at risk
      </span>
    {/if}
  </div>

  {#if allRisks.length === 0}
    <!-- Calm positive state — nothing is at risk. -->
    <div
      style="display: flex; align-items: center; gap: 10px; padding: 18px 14px; color: var(--ad-muted); font-size: 12.5px;"
    >
      <span
        aria-hidden="true"
        style="display: inline-flex; align-items: center; justify-content: center; width: 22px; height: 22px; border-radius: 999px; background: color-mix(in oklch, var(--ad-live) 16%, transparent); color: var(--ad-live); font-size: 12px; font-weight: 700;"
      >✓</span>
      <span>No open loops — everything committed and pushed.</span>
    </div>
  {:else}
    <div style="display: flex; flex-direction: column;">
      {#each groups as group, gi (group.kind)}
        {@const meta = metaFor(group.kind)}
        <div
          style="padding: 10px 14px; border-top: {gi === 0
            ? 'none'
            : '1px solid var(--ad-border-soft)'};"
        >
          <!-- Group sub-heading with count. -->
          <div
            style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;"
          >
            <span
              aria-hidden="true"
              style="width: 7px; height: 7px; border-radius: 999px; background: {meta.accent}; flex: none;"
            ></span>
            <span
              style="font-size: 10.5px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.08em; color: {meta.accent};"
            >
              {meta.label}
            </span>
            <span class="mono faint" style="font-size: 11px;">
              {group.risks.length}
            </span>
          </div>

          <!-- Risk rows. Keyed by index — repo+detail can repeat. -->
          <ul
            style="margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 4px;"
          >
            {#each group.risks as risk, ri (ri)}
              <li
                style="display: flex; align-items: baseline; gap: 8px; padding: 6px 8px; border-radius: 6px; border-left: 2px solid {meta.accent}; background: {meta.tint}; font-size: 12px; line-height: 1.45;"
              >
                <span
                  class="mono"
                  style="font-weight: 600; color: var(--ad-fg); flex: none; max-width: 38%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
                  title={risk.repo}
                >
                  {risk.repo}
                </span>
                <span style="color: var(--ad-muted); flex: 1 1 auto; min-width: 0;">
                  {risk.detail}
                </span>
                {#if risk.age_minutes > 0}
                  <span
                    class="mono faint"
                    style="font-size: 10.5px; flex: none; white-space: nowrap;"
                  >
                    {ageLabel(risk.age_minutes)}
                  </span>
                {/if}
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>
  {/if}
</section>
