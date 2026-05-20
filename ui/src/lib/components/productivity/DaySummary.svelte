<!--
  DaySummary — "What was done" panel for the productivity dashboard.

  The rest of the dashboard answers "how much" (time, commits, repos).
  This panel answers "what" — it surfaces klyne's worklog reflection
  narrative for the day: the written account of the work, not the
  metrics around it.

  Three states, keyed off report.reflection_status / reflection_markdown:
    - reflection present  → rendered Markdown prose
    - reflection absent   → calm informational empty state with the
                            backend nudge + a run-a-reflection CTA
  A small status pill (current = green, stale/missing = amber) sits in
  the header so the freshness of the narrative is always visible.

  Markdown rendering reuses $lib/markdown.ts (renderMarkdown — marked +
  DOMPurify): the helper escapes/sanitizes, and the sanitized string is
  injected via {@html}. Raw input is never injected.

  House style mirrors the rest of klyne's UI: --ad-* design tokens,
  .ad-card / .ad-badge / .ad-dot utilities, scoped <style>.
-->
<script lang="ts">
  import type { ProductivityReport } from '$lib/api';
  import { renderMarkdown } from '$lib/markdown.js';

  interface Props {
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  /** Trimmed reflection body — treats whitespace-only as absent. */
  const body = $derived((report.reflection_markdown ?? '').trim());

  /** Whether we have a real narrative to render. */
  const hasReflection = $derived(body.length > 0);

  /**
   * Sanitized HTML for the reflection body. renderMarkdown runs the
   * source through marked + DOMPurify, so the output string is safe to
   * inject via {@html}. We never {@html} the raw input.
   */
  const bodyHtml = $derived(hasReflection ? renderMarkdown(body) : '');

  /** Status pill: green when current, amber for stale / missing / other. */
  const isCurrent = $derived(report.reflection_status === 'current');

  const statusLabel = $derived.by(() => {
    switch (report.reflection_status) {
      case 'current':
        return 'current';
      case 'stale':
        return 'stale';
      case 'missing':
        return 'missing';
      default:
        return report.reflection_status || 'unknown';
    }
  });

  /** Optional backend prompt shown in the empty / stale states. */
  const nudge = $derived((report.nudge ?? '').trim());
</script>

<section class="ad-card day-summary" aria-labelledby="day-summary-title">
  <!-- Header: title + freshness pill -->
  <div class="ds-hd">
    <div class="ds-hd-l">
      <span class="ds-icon" aria-hidden="true">✎</span>
      <h2 id="day-summary-title" class="ds-title">What was done</h2>
    </div>
    <span
      class="ad-badge ds-pill {isCurrent ? 'ds-pill--ok' : 'ds-pill--warn'}"
      title="Worklog reflection is {statusLabel}"
    >
      <span
        class="ad-dot {isCurrent ? 'ds-dot--ok' : 'ds-dot--warn'}"
        aria-hidden="true"
      ></span>
      {statusLabel}
    </span>
  </div>

  <div class="ds-bd">
    {#if hasReflection}
      <!-- The day's narrative. bodyHtml is sanitized by renderMarkdown. -->
      <div class="ds-prose">{@html bodyHtml}</div>
      {#if !isCurrent && nudge}
        <!-- Reflection exists but is stale — surface the nudge to refresh. -->
        <p class="ds-note">{nudge}</p>
      {/if}
    {:else}
      <!-- Informational empty state — not an error. -->
      <div class="ds-empty">
        <span class="ds-empty-mark" aria-hidden="true">○</span>
        <div class="ds-empty-text">
          {#if nudge}
            <p class="ds-empty-lead">{nudge}</p>
          {/if}
          <p class="ds-empty-cta">
            No worklog reflection for this day yet — run a klyne reflection
            to capture what was done.
          </p>
        </div>
      </div>
    {/if}
  </div>
</section>

<style>
  .day-summary {
    display: flex;
    flex-direction: column;
  }

  /* --- Header --------------------------------------------------------- */
  .ds-hd {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--ad-s3);
    padding: var(--ad-s3) var(--ad-s4);
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .ds-hd-l {
    display: flex;
    align-items: center;
    gap: var(--ad-s2);
    min-width: 0;
  }
  .ds-icon {
    font-size: var(--ad-fs-md);
    color: var(--ad-accent);
  }
  .ds-title {
    margin: 0;
    font-family: var(--ad-font-display);
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    line-height: var(--ad-lh-tight);
    letter-spacing: -0.005em;
    color: var(--ad-fg);
  }

  /* --- Freshness pill ------------------------------------------------- */
  .ds-pill {
    flex: none;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 10px;
    font-weight: 600;
  }
  .ds-pill--ok {
    color: var(--ad-live);
    background: var(--ad-active-bg);
    border-color: color-mix(in oklch, var(--ad-live) 35%, transparent);
  }
  .ds-pill--warn {
    color: var(--ad-warn);
    background: var(--ad-compact-bg);
    border-color: color-mix(in oklch, var(--ad-warn) 35%, transparent);
  }
  .ds-dot--ok {
    background: var(--ad-live);
  }
  .ds-dot--warn {
    background: var(--ad-warn);
  }

  /* --- Body ----------------------------------------------------------- */
  .ds-bd {
    padding: var(--ad-s4);
  }

  /* Stale note appended below an existing (but outdated) reflection. */
  .ds-note {
    margin: var(--ad-s4) 0 0;
    padding-top: var(--ad-s3);
    border-top: 1px dashed var(--ad-border-soft);
    color: var(--ad-muted);
    font-size: var(--ad-fs-xs);
    line-height: var(--ad-lh-base);
  }

  /* --- Empty state --------------------------------------------------- */
  .ds-empty {
    display: flex;
    align-items: flex-start;
    gap: var(--ad-s3);
    padding: var(--ad-s2) 0;
  }
  .ds-empty-mark {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: 999px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    color: var(--ad-faint);
    font-size: 11px;
    line-height: 1;
  }
  .ds-empty-text {
    min-width: 0;
  }
  .ds-empty-lead {
    margin: 0 0 var(--ad-s2);
    color: var(--ad-fg-2);
    font-size: var(--ad-fs-md);
    line-height: var(--ad-lh-base);
  }
  .ds-empty-cta {
    margin: 0;
    color: var(--ad-muted);
    font-size: var(--ad-fs-sm);
    line-height: var(--ad-lh-base);
  }

  /* --- Rendered Markdown prose ---------------------------------------
     The reflection HTML is injected via {@html}, so Svelte's scoping
     pass never sees those tags — :global() is required to style them.
     Goal: readable long-form prose, consistent with klyne typography. */
  .ds-prose {
    color: var(--ad-fg-2);
    font-size: var(--ad-fs-base);
    line-height: var(--ad-lh-base);
  }
  .ds-prose :global(> :first-child) {
    margin-top: 0;
  }
  .ds-prose :global(> :last-child) {
    margin-bottom: 0;
  }
  .ds-prose :global(p) {
    margin: 0 0 0.7em;
  }
  .ds-prose :global(h1),
  .ds-prose :global(h2),
  .ds-prose :global(h3),
  .ds-prose :global(h4) {
    margin: 1em 0 0.4em;
    font-family: var(--ad-font-display);
    font-weight: 600;
    line-height: var(--ad-lh-tight);
    color: var(--ad-fg);
  }
  .ds-prose :global(h1) {
    font-size: var(--ad-fs-lg);
  }
  .ds-prose :global(h2) {
    font-size: var(--ad-fs-md);
  }
  .ds-prose :global(h3),
  .ds-prose :global(h4) {
    font-size: var(--ad-fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--ad-fg-2);
  }
  .ds-prose :global(strong) {
    font-weight: 600;
    color: var(--ad-fg);
  }
  .ds-prose :global(em) {
    font-style: italic;
  }
  .ds-prose :global(a) {
    color: var(--ad-accent-2);
    text-decoration: none;
  }
  .ds-prose :global(a:hover) {
    text-decoration: underline;
  }
  .ds-prose :global(ul),
  .ds-prose :global(ol) {
    margin: 0.4em 0 0.7em;
    padding-left: 1.3em;
  }
  .ds-prose :global(li) {
    margin: 0.2em 0;
  }
  .ds-prose :global(li::marker) {
    color: var(--ad-faint);
  }
  .ds-prose :global(code) {
    font-family: var(--ad-font-mono);
    font-size: 0.9em;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 3px;
    padding: 0 4px;
  }
  .ds-prose :global(pre) {
    margin: 0.6em 0;
    padding: var(--ad-s2) var(--ad-s3);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
    overflow-x: auto;
    font-size: var(--ad-fs-xs);
    line-height: 1.45;
  }
  .ds-prose :global(pre code) {
    background: transparent;
    border: 0;
    padding: 0;
    font-size: inherit;
  }
  .ds-prose :global(blockquote) {
    margin: 0.6em 0;
    padding: 0.2em 0 0.2em 0.9em;
    border-left: 2px solid var(--ad-border);
    color: var(--ad-muted);
  }
  .ds-prose :global(hr) {
    margin: 0.9em 0;
    border: 0;
    border-top: 1px solid var(--ad-border-soft);
  }
</style>
