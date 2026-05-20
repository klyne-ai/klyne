<!--
  RiskPanel — "Open Loops" risk surface for the productivity dashboard.

  Flattens every ProductivityRisk across every service into one list (each
  item keeps its owning repo, branch and worktree), groups by kind, and
  renders a compact panel.

  Each row is expandable to show the concrete evidence behind the count
  — for "unpushed": the actual ahead commits (SHA + subject); for
  "done-uncommitted": the actual uncommitted file paths. amber =
  unpushed work, red = done-but-uncommitted. When nothing is at risk
  the panel shows a calm positive state.
-->
<script lang="ts">
  import type { ProductivityReport, ProductivityRisk } from '$lib/api';

  interface Props {
    report: ProductivityReport;
  }
  const { report }: Props = $props();

  /** A risk lifted out of its service, carrying the repo it belongs to
   *  and a stable id used as the expansion key. */
  interface FlatRisk extends ProductivityRisk {
    uid: string;
    repo: string;
  }

  /** kind → display config. Keeps the vocabulary consistent and gives
   *  each group its accent colour, sub-heading, and ordering. */
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

  /** Flatten all risks across every service, tagging each with its repo
   *  and a stable uid (svc:risk indices). */
  const allRisks: FlatRisk[] = $derived(
    (report.services ?? []).flatMap((svc, si) =>
      (svc.risks ?? []).map((risk, ri) => ({
        ...risk,
        uid: `${si}:${ri}`,
        repo: svc.repo
      }))
    )
  );

  /** Group the flat list by kind, sorted by severity. */
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

  // Per-row expansion state — keyed by FlatRisk.uid.
  let expanded = $state<Set<string>>(new Set());
  function toggle(uid: string): void {
    const next = new Set(expanded);
    if (next.has(uid)) next.delete(uid);
    else next.add(uid);
    expanded = next;
  }

  /** "~Xh Ym ago" (drops the hour part when zero). */
  function ageLabel(minutes: number): string {
    const h = Math.floor(minutes / 60);
    const m = minutes % 60;
    return h > 0 ? `~${h}h ${m}m ago` : `~${m}m ago`;
  }

  function basename(path: string): string {
    if (!path) return '';
    const parts = path.replace(/\/+$/, '').split('/');
    return parts[parts.length - 1] || path;
  }

  /** Branch label for display. Detached worktrees report "HEAD" — fall
   *  back to the worktree-dir basename so the user can still tell rows
   *  apart. Empty when neither is known. */
  function branchLabel(risk: FlatRisk): string {
    if (risk.branch && risk.branch !== 'HEAD') return risk.branch;
    if (risk.worktree_path) return basename(risk.worktree_path);
    return '';
  }

  // Display caps so panel payload stays bounded — extra items are
  // counted as "+N more".
  const commitDisplayCap = 12;
  const fileDisplayCap = 15;
</script>

<section class="op-card" aria-labelledby="open-loops-title">
  <header class="op-hd">
    <div class="op-hd-left">
      <span aria-hidden="true" class="op-icon">🌀</span>
      <h2 id="open-loops-title" class="op-title">Open Loops</h2>
    </div>
    {#if allRisks.length > 0}
      <span class="op-count ad-mono" aria-label="{allRisks.length} items at risk">
        {allRisks.length} at risk
      </span>
    {/if}
  </header>

  {#if allRisks.length === 0}
    <div class="op-clear">
      <span aria-hidden="true" class="op-check">✓</span>
      <span>No open loops — everything committed and pushed.</span>
    </div>
  {:else}
    <div class="op-body">
      {#each groups as group, gi (group.kind)}
        {@const meta = metaFor(group.kind)}
        <div
          class="op-group"
          class:op-group--first={gi === 0}
          style="--op-accent: {meta.accent}; --op-tint: {meta.tint};"
        >
          <div class="op-group-hd">
            <span aria-hidden="true" class="op-dot"></span>
            <span class="op-group-label">{meta.label}</span>
            <span class="op-group-n ad-mono">{group.risks.length}</span>
          </div>

          <ul class="op-list">
            {#each group.risks as risk (risk.uid)}
              {@const isOpen = expanded.has(risk.uid)}
              {@const br = branchLabel(risk)}
              {@const wtBase = basename(risk.worktree_path)}
              {@const hasEvidence =
                (risk.kind === 'unpushed' && (risk.commits?.length ?? 0) > 0) ||
                (risk.kind === 'done-uncommitted' && (risk.files?.length ?? 0) > 0)}
              <li class="op-item">
                <button
                  type="button"
                  class="op-row"
                  onclick={() => toggle(risk.uid)}
                  aria-expanded={isOpen}
                  aria-controls="op-evidence-{risk.uid}"
                  disabled={!hasEvidence}
                  title={hasEvidence
                    ? isOpen
                      ? 'Hide evidence'
                      : 'Show which commits/files'
                    : 'No evidence list available'}
                >
                  <span
                    class="op-caret"
                    class:op-caret--open={isOpen}
                    class:op-caret--hidden={!hasEvidence}
                    aria-hidden="true">▸</span>
                  <span class="op-repo ad-mono" title={risk.repo}>{risk.repo}</span>
                  {#if br}
                    <span
                      class="op-branch ad-mono"
                      title="branch {risk.branch || '(detached)'} · worktree {wtBase}"
                    >
                      · {br}
                    </span>
                  {/if}
                  <span class="op-detail">{risk.detail}</span>
                  {#if risk.age_minutes > 0}
                    <span class="op-age ad-mono">{ageLabel(risk.age_minutes)}</span>
                  {/if}
                </button>

                {#if isOpen}
                  <div id="op-evidence-{risk.uid}" class="op-evidence">
                    {#if risk.kind === 'unpushed'}
                      <p class="op-ev-label ad-mono">
                        Commits ahead of origin
                        {#if risk.branch}
                          on <span class="ad-tnum">{risk.branch}</span>
                        {/if}
                      </p>
                      <ul class="op-ev-commits">
                        {#each risk.commits.slice(0, commitDisplayCap) as c, ci (ci)}
                          <li class="op-ev-commit">
                            <code class="op-ev-sha ad-mono">{c.sha}</code>
                            <span class="op-ev-subject" title={c.subject}>{c.subject}</span>
                          </li>
                        {/each}
                        {#if risk.commits.length > commitDisplayCap}
                          <li class="op-ev-more">
                            + {risk.commits.length - commitDisplayCap} more shown of {risk.commits.length} listed
                          </li>
                        {/if}
                      </ul>
                    {:else if risk.kind === 'done-uncommitted'}
                      <p class="op-ev-label ad-mono">Uncommitted paths</p>
                      <ul class="op-ev-files">
                        {#each risk.files.slice(0, fileDisplayCap) as f, fi (fi)}
                          <li class="op-ev-file ad-mono" title={f}>{f}</li>
                        {/each}
                        {#if risk.files.length > fileDisplayCap}
                          <li class="op-ev-more">
                            + {risk.files.length - fileDisplayCap} more
                          </li>
                        {/if}
                      </ul>
                    {/if}
                    {#if risk.worktree_path}
                      <p class="op-ev-where ad-mono" title={risk.worktree_path}>
                        worktree: {wtBase}
                      </p>
                    {/if}
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>
  {/if}
</section>

<style>
  .op-card {
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 10px;
    overflow: hidden;
  }

  .op-hd {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 14px;
    border-bottom: 1px solid var(--ad-border-soft);
  }

  .op-hd-left {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .op-icon {
    font-size: 13px;
  }
  .op-title {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    letter-spacing: -0.005em;
    color: var(--ad-fg);
  }
  .op-count {
    font-size: 11px;
    font-weight: 600;
    color: var(--ad-danger);
  }

  /* calm positive state */
  .op-clear {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 18px 14px;
    color: var(--ad-muted);
    font-size: 12.5px;
  }
  .op-check {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: 999px;
    background: color-mix(in oklch, var(--ad-live) 16%, transparent);
    color: var(--ad-live);
    font-size: 12px;
    font-weight: 700;
  }

  .op-body {
    display: flex;
    flex-direction: column;
  }

  .op-group {
    padding: 10px 14px;
    border-top: 1px solid var(--ad-border-soft);
  }
  .op-group--first {
    border-top: none;
  }

  .op-group-hd {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }
  .op-dot {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    background: var(--op-accent);
    flex: none;
  }
  .op-group-label {
    font-size: 10.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--op-accent);
  }
  .op-group-n {
    font-size: 11px;
    color: var(--ad-faint);
  }

  .op-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .op-item {
    border-radius: 6px;
    border-left: 2px solid var(--op-accent);
    background: var(--op-tint);
    overflow: hidden;
  }

  .op-row {
    width: 100%;
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 6px 8px;
    background: transparent;
    border: 0;
    cursor: pointer;
    text-align: left;
    font: inherit;
    font-size: 12px;
    line-height: 1.45;
    color: inherit;
    transition: background 100ms ease;
  }
  .op-row:hover:not(:disabled) {
    background: color-mix(in oklch, var(--op-accent) 6%, transparent);
  }
  .op-row:disabled {
    cursor: default;
  }

  .op-caret {
    display: inline-block;
    font-size: 9px;
    color: var(--ad-faint);
    flex: none;
    transition: transform 160ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .op-caret--open {
    transform: rotate(90deg);
  }
  .op-caret--hidden {
    visibility: hidden;
  }

  .op-repo {
    font-weight: 600;
    color: var(--ad-fg);
    flex: none;
    max-width: 28%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .op-branch {
    color: var(--ad-fg-2);
    flex: none;
    max-width: 28%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .op-detail {
    color: var(--ad-muted);
    flex: 1 1 auto;
    min-width: 0;
  }

  .op-age {
    font-size: 10.5px;
    color: var(--ad-faint);
    flex: none;
    white-space: nowrap;
  }

  /* expanded evidence */
  .op-evidence {
    padding: 8px 10px 10px 24px;
    border-top: 1px dashed color-mix(in oklch, var(--op-accent) 35%, transparent);
    background: color-mix(in oklch, var(--ad-bg-2) 35%, transparent);
  }
  .op-ev-label {
    margin: 0 0 6px;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ad-faint);
  }

  .op-ev-commits,
  .op-ev-files {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .op-ev-commit {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
    font-size: 11.5px;
  }
  .op-ev-sha {
    color: var(--ad-fg-2);
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 4px;
    padding: 1px 5px;
    flex: none;
  }
  .op-ev-subject {
    color: var(--ad-muted);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
  }

  .op-ev-file {
    font-size: 11.5px;
    color: var(--ad-fg-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .op-ev-more {
    font-size: 10.5px;
    color: var(--ad-faint);
    font-style: italic;
  }

  .op-ev-where {
    margin: 8px 0 0;
    font-size: 10px;
    color: var(--ad-faint);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
