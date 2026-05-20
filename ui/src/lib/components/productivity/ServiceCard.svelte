<!--
  ServiceCard — per-repo card for the productivity dashboard.
  The core repeating unit: one git repo, its branches, and any risks.
  Branches are keyed by index (detached worktrees all report "HEAD").
-->
<script lang="ts">
  import type {
    ProductivityService,
    ProductivityBranch,
    ProductivityCommit,
    ProductivityRisk
  } from '$lib/api';

  interface Props {
    service: ProductivityService;
  }

  const { service }: Props = $props();

  // Which branches have their commit list expanded — keyed by index,
  // since branch names are not unique across detached worktrees.
  let expanded = $state<Set<number>>(new Set());

  function toggle(i: number): void {
    const next = new Set(expanded);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    expanded = next;
  }

  // Format a minute count as "~Xh Ym". Returns '' for non-positive input.
  function hm(min: number): string {
    if (!min || min <= 0) return '';
    const h = Math.floor(min / 60);
    const m = min % 60;
    if (h <= 0) return `~${m}m`;
    return `~${h}h ${m}m`;
  }

  // Format a minute count as "Xh Ym" / "Nm" (no leading "~"). For the
  // per-CLI split and the per-branch ship span.
  function hmPlain(min: number): string {
    const h = Math.floor(min / 60);
    const m = min % 60;
    if (h <= 0) return `${m}m`;
    return `${h}h ${m}m`;
  }

  // Human-readable branch ship span. 0 means <2 commits → no real span.
  function shipSpan(min: number): string {
    if (!min || min <= 0) return 'single commit';
    return hmPlain(min);
  }

  // Title-case a CLI key for display ("claude" → "Claude").
  function cliLabel(cli: string): string {
    return cli.length === 0 ? cli : cli[0].toUpperCase() + cli.slice(1);
  }

  // Per-CLI time split for the service header — non-zero entries only.
  const cliSplit = $derived(
    Object.entries(service.minutes_by_cli ?? {})
      .filter(([, min]) => min > 0)
      .sort((a, b) => b[1] - a[1])
  );

  // Worklog reflection section — collapsed by default.
  let worklogOpen = $state(false);

  // Format an RFC3339 timestamp as a short local time (HH:MM). Returns ''
  // when the input is missing or unparseable.
  function shortTime(iso: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  // Minimal, safe Markdown → HTML. Escapes all HTML entities first, then
  // applies a tiny subset: **bold**, `- ` / `* ` bullet lines, and line
  // breaks. Never receives or emits unescaped input.
  function renderWorklog(md: string): string {
    const escaped = md
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
    const lines = escaped.split(/\r?\n/);
    const out: string[] = [];
    let inList = false;
    for (const raw of lines) {
      const line = raw.trimEnd();
      const bullet = line.match(/^\s*[-*]\s+(.*)$/);
      if (bullet) {
        if (!inList) {
          out.push('<ul>');
          inList = true;
        }
        out.push(`<li>${inline(bullet[1])}</li>`);
        continue;
      }
      if (inList) {
        out.push('</ul>');
        inList = false;
      }
      if (line.length === 0) {
        out.push('<br />');
      } else {
        out.push(`<p>${inline(line)}</p>`);
      }
    }
    if (inList) out.push('</ul>');
    return out.join('');
  }

  // Inline span: only **bold** is supported. Input is already escaped.
  function inline(text: string): string {
    return text.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  }

  // Ship-state → badge color class + readable label.
  const shipMeta: Record<string, { cls: string; label: string }> = {
    'merged-to-default': { cls: 'ship--merged', label: 'merged' },
    'pushed-to-remote': { cls: 'ship--pushed', label: 'pushed' },
    'committed-local-only': { cls: 'ship--local', label: 'local only' }
  };
  function ship(s: string): { cls: string; label: string } {
    return shipMeta[s] ?? { cls: 'ship--local', label: s };
  }

  // Risk kind → chip color class + readable label.
  const riskMeta: Record<string, { cls: string; label: string }> = {
    unpushed: { cls: 'risk--warn', label: 'unpushed' },
    'done-uncommitted': { cls: 'risk--danger', label: 'done · uncommitted' }
  };
  function risk(kind: string): { cls: string; label: string } {
    return riskMeta[kind] ?? { cls: 'risk--warn', label: kind };
  }

  function shortSha(sha: string): string {
    return sha.slice(0, 7);
  }
</script>

<article class="svc-card">
  <header class="svc-hd">
    <div class="svc-id">
      <div class="svc-id-line">
        <h3 class="svc-repo">{service.repo}</h3>
        {#if service.manual_only}
          <span class="svc-manual" title="No AI session recorded for this repo">
            manual — no AI session
          </span>
        {/if}
      </div>
      <div class="svc-path ad-mono ad-truncate" title={service.project_path}>
        {service.project_path}
      </div>

      {#if cliSplit.length > 0}
        <ul class="cli-split" aria-label="AI time by CLI">
          {#each cliSplit as [cli, min], ci (ci)}
            <li class="cli-chip">
              <span class="cli-name">{cliLabel(cli)}</span>
              <span class="cli-time ad-mono ad-tnum">{hmPlain(min)}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    {#if service.risks.length > 0}
      <ul class="risk-list" aria-label="Repository risks">
        {#each service.risks as r, ri (ri)}
          {@const meta = risk(r.kind)}
          {@const age = hm(r.age_minutes)}
          <li class="risk-chip {meta.cls}">
            <span class="risk-kind">{meta.label}</span>
            <span class="risk-detail">{r.detail}</span>
            {#if age}<span class="risk-age">{age} ago</span>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </header>

  {#if service.branches.length === 0}
    <p class="svc-empty">No branch activity in this window.</p>
  {:else}
    <ul class="branch-list">
      {#each service.branches as br, bi (bi)}
        {@const sm = ship(br.ship)}
        {@const time = hm(br.attributed_minutes)}
        {@const isOpen = expanded.has(bi)}
        <li class="branch">
          <div class="branch-row">
            <span class="ship-badge {sm.cls}">{sm.label}</span>

            <span class="branch-name ad-mono ad-truncate" title={br.name}>
              {br.name}
            </span>

            {#if br.ticket_id}
              <span class="ticket-chip ad-mono">{br.ticket_id}</span>
            {/if}

            <span class="branch-meta ad-mono ad-tnum">
              {#if br.ahead > 0}<span title="commits ahead of remote">↑{br.ahead}</span>{/if}
              {#if br.behind > 0}<span title="commits behind remote">↓{br.behind}</span>{/if}
            </span>

            {#if time}
              <span class="branch-time ad-mono ad-tnum" title="Attributed work time">
                {time}
              </span>
            {/if}

            <span
              class="branch-span ad-mono ad-tnum"
              title="Time to ship — span between first and last commit"
            >
              <span class="span-label">ship span</span>
              {shipSpan(br.ship_span_minutes)}
            </span>
          </div>

          {#if br.narrative}
            <p class="branch-narrative">{br.narrative}</p>
          {/if}

          {#if br.commits.length > 0}
            <button
              type="button"
              class="commit-toggle"
              onclick={() => toggle(bi)}
              aria-expanded={isOpen}
              aria-controls="commits-{bi}"
            >
              <span class="commit-caret" class:open={isOpen} aria-hidden="true">▸</span>
              {br.commits.length} commit{br.commits.length === 1 ? '' : 's'}
            </button>

            {#if isOpen}
              {@const firstAt = shortTime(br.first_commit_at)}
              {@const lastAt = shortTime(br.last_commit_at)}
              {#if firstAt && lastAt}
                <p class="commit-window ad-mono ad-tnum">
                  {firstAt} → {lastAt}
                </p>
              {/if}
              <ul id="commits-{bi}" class="commit-list">
                {#each br.commits as c, ci (ci)}
                  <li class="commit-row" class:commit-row--ai={!c.is_user}>
                    <code class="commit-sha">{shortSha(c.sha)}</code>
                    <span class="commit-subject ad-truncate" title={c.subject}>
                      {c.subject}
                    </span>
                  </li>
                {/each}
              </ul>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if service.reflection_markdown}
    <details class="worklog" bind:open={worklogOpen}>
      <summary class="worklog-summary">
        <span class="worklog-caret" class:open={worklogOpen} aria-hidden="true">▸</span>
        Worklog note
      </summary>
      <!-- renderWorklog() escapes all HTML entities before applying a
           minimal Markdown subset, so this @html input is safe. -->
      <div class="worklog-body">
        {@html renderWorklog(service.reflection_markdown)}
      </div>
    </details>
  {/if}
</article>

<style>
  .svc-card {
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 10px;
    overflow: hidden;
  }

  /* ---- header ---- */
  .svc-hd {
    padding: 13px 15px;
    border-bottom: 1px solid var(--ad-border-soft);
    background: linear-gradient(
      180deg,
      color-mix(in oklch, var(--ad-bg-2) 60%, transparent),
      transparent
    );
  }

  .svc-id-line {
    display: flex;
    align-items: baseline;
    gap: 9px;
    flex-wrap: wrap;
  }

  .svc-repo {
    margin: 0;
    font-size: var(--ad-fs-lg);
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--ad-fg);
  }

  .svc-manual {
    font-size: 10.5px;
    font-weight: 500;
    padding: 1.5px 7px;
    border-radius: 999px;
    color: var(--ad-faint);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
  }

  .svc-path {
    margin-top: 3px;
    font-size: var(--ad-fs-xs);
    color: var(--ad-faint);
    max-width: 100%;
  }

  /* ---- per-CLI time split ---- */
  .cli-split {
    list-style: none;
    margin: 8px 0 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .cli-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 999px;
    color: var(--ad-fg-2);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
  }

  .cli-name {
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  .cli-time {
    color: var(--ad-muted);
  }

  /* ---- risk chips ---- */
  .risk-list {
    list-style: none;
    margin: 11px 0 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .risk-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    font-size: 11px;
    line-height: 1.4;
    padding: 3px 9px;
    border-radius: 7px;
    border: 1px solid transparent;
  }

  .risk-kind {
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-size: 9.5px;
    white-space: nowrap;
  }

  .risk-detail {
    color: var(--ad-fg-2);
  }

  .risk-age {
    color: var(--ad-faint);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .risk--warn {
    color: var(--ad-warn);
    background: color-mix(in oklch, var(--ad-warn) 12%, transparent);
    border-color: color-mix(in oklch, var(--ad-warn) 35%, var(--ad-border));
  }

  .risk--danger {
    color: var(--ad-danger);
    background: color-mix(in oklch, var(--ad-danger) 12%, transparent);
    border-color: color-mix(in oklch, var(--ad-danger) 38%, var(--ad-border));
  }

  /* ---- branches ---- */
  .svc-empty {
    margin: 0;
    padding: 18px 15px;
    font-size: var(--ad-fs-sm);
    color: var(--ad-faint);
    text-align: center;
  }

  .branch-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .branch {
    padding: 12px 15px;
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .branch:last-child {
    border-bottom: none;
  }

  .branch-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .ship-badge {
    display: inline-flex;
    align-items: center;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 2.5px 8px;
    border-radius: 999px;
    border: 1px solid transparent;
    white-space: nowrap;
    flex: none;
  }

  .ship--merged {
    color: var(--ad-live);
    background: color-mix(in oklch, var(--ad-live) 14%, transparent);
    border-color: color-mix(in oklch, var(--ad-live) 38%, var(--ad-border));
  }
  .ship--pushed {
    color: var(--ad-codex);
    background: color-mix(in oklch, var(--ad-codex) 16%, transparent);
    border-color: color-mix(in oklch, var(--ad-codex) 40%, var(--ad-border));
  }
  .ship--local {
    color: var(--ad-warn);
    background: color-mix(in oklch, var(--ad-warn) 12%, transparent);
    border-color: color-mix(in oklch, var(--ad-warn) 35%, var(--ad-border));
  }

  .branch-name {
    font-size: var(--ad-fs-sm);
    color: var(--ad-fg-2);
    min-width: 0;
    flex: 1 1 120px;
  }

  .ticket-chip {
    font-size: 10.5px;
    font-weight: 500;
    padding: 2px 7px;
    border-radius: 5px;
    color: var(--ad-fg-2);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
    flex: none;
  }

  .branch-meta {
    display: inline-flex;
    gap: 6px;
    font-size: 11px;
    color: var(--ad-faint);
    flex: none;
  }

  .branch-time {
    font-size: 11.5px;
    color: var(--ad-muted);
    white-space: nowrap;
    flex: none;
  }

  .branch-span {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
    font-size: 11.5px;
    color: var(--ad-muted);
    white-space: nowrap;
    flex: none;
  }

  .span-label {
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--ad-faint);
  }

  .branch-narrative {
    margin: 7px 0 0;
    font-size: var(--ad-fs-sm);
    line-height: 1.55;
    color: var(--ad-muted);
    /* Clamp long narratives to ~3 lines; full text via title attr-free wrap. */
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  /* ---- commit expansion ---- */
  .commit-toggle {
    margin-top: 8px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: transparent;
    border: 0;
    padding: 2px 0;
    cursor: pointer;
    font-family: var(--ad-font-mono);
    font-size: 11px;
    color: var(--ad-faint);
    transition: color 120ms ease;
  }
  .commit-toggle:hover {
    color: var(--ad-fg-2);
  }

  .commit-caret {
    display: inline-block;
    font-size: 9px;
    transition: transform 160ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .commit-caret.open {
    transform: rotate(90deg);
  }

  .commit-window {
    margin: 7px 0 0;
    font-size: 10.5px;
    color: var(--ad-faint);
    letter-spacing: 0.02em;
  }

  .commit-list {
    list-style: none;
    margin: 8px 0 0;
    padding: 8px 10px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 7px;
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .commit-row {
    display: flex;
    align-items: baseline;
    gap: 9px;
    min-width: 0;
  }

  .commit-sha {
    font-family: var(--ad-font-mono);
    font-size: 11px;
    color: var(--ad-fg-2);
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 4px;
    padding: 1px 5px;
    flex: none;
  }

  .commit-row--ai .commit-sha {
    color: var(--ad-claude);
    border-color: color-mix(in oklch, var(--ad-claude) 35%, var(--ad-border-soft));
  }

  .commit-subject {
    font-size: var(--ad-fs-sm);
    color: var(--ad-muted);
    min-width: 0;
  }

  /* ---- worklog note ---- */
  .worklog {
    border-top: 1px solid var(--ad-border-soft);
    background: linear-gradient(
      180deg,
      transparent,
      color-mix(in oklch, var(--ad-bg-2) 50%, transparent)
    );
  }

  .worklog-summary {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 9px 15px;
    cursor: pointer;
    font-family: var(--ad-font-mono);
    font-size: 11px;
    color: var(--ad-faint);
    list-style: none;
    transition: color 120ms ease;
  }
  .worklog-summary::-webkit-details-marker {
    display: none;
  }
  .worklog-summary:hover {
    color: var(--ad-fg-2);
  }

  .worklog-caret {
    display: inline-block;
    font-size: 9px;
    transition: transform 160ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .worklog-caret.open {
    transform: rotate(90deg);
  }

  .worklog-body {
    padding: 0 15px 13px;
    font-size: var(--ad-fs-sm);
    line-height: 1.55;
    color: var(--ad-muted);
  }
  .worklog-body :global(p) {
    margin: 0 0 6px;
  }
  .worklog-body :global(p:last-child) {
    margin-bottom: 0;
  }
  .worklog-body :global(ul) {
    margin: 0 0 6px;
    padding-left: 18px;
  }
  .worklog-body :global(li) {
    margin: 2px 0;
  }
  .worklog-body :global(strong) {
    color: var(--ad-fg-2);
    font-weight: 600;
  }
  .worklog-body :global(br) {
    line-height: 0.5;
  }
</style>
