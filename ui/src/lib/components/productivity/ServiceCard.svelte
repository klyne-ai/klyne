<!--
  ServiceCard — per-repo panel for the productivity dashboard.

  Each repo is a full-width dashboard panel: a header (repo name, path,
  manual tag, risk chips), a row of six derived metric tiles, then an
  expandable detail block (branches + worklog note) collapsed by default.

  Branches are keyed by index — detached worktrees all report "HEAD",
  so names are not unique.
-->
<script lang="ts">
  import type { ProductivityService } from '$lib/api';

  interface Props {
    service: ProductivityService;
  }

  const { service }: Props = $props();

  // ---- expansion state (Svelte 5 runes) -----------------------------------

  // Whole detail block, collapsed by default.
  let detailOpen = $state(false);

  // Which branches have their commit list expanded — keyed by index,
  // since branch names are not unique across detached worktrees.
  let expanded = $state<Set<number>>(new Set());

  function toggle(i: number): void {
    const next = new Set(expanded);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    expanded = next;
  }

  // Header risk-chip expansion state (per service.risks index) — opens
  // the same kind of inline evidence list the bottom Open Loops shows,
  // so the chips are self-contained.
  let riskExpanded = $state<Set<number>>(new Set());

  function toggleRisk(i: number): void {
    const next = new Set(riskExpanded);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    riskExpanded = next;
  }

  // Worklog reflection sub-section — collapsed by default.
  let worklogOpen = $state(false);

  // ---- formatters ---------------------------------------------------------

  // Format a minute count as "~Xh Ym". Returns '' for non-positive input.
  function hm(min: number): string {
    if (!min || min <= 0) return '';
    const h = Math.floor(min / 60);
    const m = min % 60;
    if (h <= 0) return `~${m}m`;
    return `~${h}h ${m}m`;
  }

  // Format a minute count as "Xh Ym" / "Nm" (no leading "~"). For tile
  // values, the per-CLI split, and per-branch ship spans.
  function hmPlain(min: number): string {
    if (!min || min <= 0) return '0m';
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

  function shortSha(sha: string): string {
    return sha.slice(0, 7);
  }

  // Format an RFC3339 timestamp as a short local time (HH:MM). Returns ''
  // when the input is missing or unparseable.
  function shortTime(iso: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  // Parse an RFC3339 timestamp to epoch-ms, or null when unparseable.
  function epoch(iso: string): number | null {
    if (!iso) return null;
    const t = new Date(iso).getTime();
    return Number.isNaN(t) ? null : t;
  }

  // Short calendar date for a timestamp ("19 May 13:50"). '' when empty.
  function shortDateTime(iso: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleString([], {
      day: '2-digit',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit'
    });
  }

  // Coarse "N ago" for a timestamp. '' for empty / the Go zero value
  // (0001-01-01, which parses to a deeply-negative epoch).
  function relTime(iso: string): string {
    if (!iso) return '';
    const t = new Date(iso).getTime();
    if (Number.isNaN(t) || t <= 0) return '';
    const mins = Math.round((Date.now() - t) / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const h = Math.floor(mins / 60);
    if (h < 24) return `${h}h ago`;
    return `${Math.floor(h / 24)}d ago`;
  }

  // ---- derived metrics (tile values) --------------------------------------

  const branches = $derived(service.branches ?? []);

  // TASKS — distinct non-empty ticket IDs across all in-window branches.
  const tickets = $derived.by(() => {
    const seen = new Set<string>();
    for (const br of branches) {
      const id = (br.ticket_id ?? '').trim();
      if (id) seen.add(id);
    }
    return [...seen];
  });

  // Other ticket IDs found ONLY on risk branches (worktrees that are
  // open — uncommitted or unpushed — but had no commits in the window
  // and so don't appear under `branches`). These would otherwise be
  // invisible on the panel even though the user clearly has them in
  // flight; surface them as secondary chips on the Tasks tile.
  const ticketPattern = /\b([a-zA-Z]{2,}-\d+)\b/;
  const otherTickets = $derived.by(() => {
    const inWindow = new Set(tickets);
    const seen = new Set<string>();
    for (const r of service.risks ?? []) {
      const m = (r.branch ?? '').match(ticketPattern);
      if (m) {
        const id = m[1].toUpperCase();
        if (!inWindow.has(id)) seen.add(id);
      }
    }
    return [...seen];
  });

  // SHIPPED — branch counts by ship state.
  const shipCounts = $derived.by(() => {
    let merged = 0;
    let pushed = 0;
    let local = 0;
    for (const br of branches) {
      if (br.ship === 'merged-to-default') merged++;
      else if (br.ship === 'pushed-to-remote') pushed++;
      else local++;
    }
    return { merged, pushed, local };
  });

  // SHIPPED headline — the dominant ship state, prominently.
  const shipHeadline = $derived.by(() => {
    const { merged, pushed, local } = shipCounts;
    if (merged > 0) return { value: String(merged), unit: 'merged' };
    if (pushed > 0) return { value: String(pushed), unit: 'pushed' };
    if (local > 0) return { value: String(local), unit: 'local' };
    return { value: '0', unit: 'shipped' };
  });

  // COMMITS — total UNIQUE user commits across the project, deduped by sha.
  // Sibling worktrees share commits, so we must not sum branch arrays.
  const uniqueUserCommits = $derived.by(() => {
    const shas = new Set<string>();
    for (const br of branches) {
      for (const c of br.commits ?? []) {
        if (c.is_user && c.sha) shas.add(c.sha);
      }
    }
    return shas.size;
  });

  // PRs MERGED — GitHub PRs the user merged in the window (gh-sourced,
  // sorted ascending by merged_at by the backend).
  const mergedPrs = $derived(service.merged_prs ?? []);
  const latestPr = $derived(
    mergedPrs.length > 0 ? mergedPrs[mergedPrs.length - 1] : null
  );
  // Freshness of the gh-sourced PR data — '' when there is none.
  const prsAsOf = $derived(relTime(service.merged_prs_as_of));

  // Freshness of the local origin/* mirror — '' when this clone has
  // never been fetched. The dashboard auto-refreshes stale remotes on
  // the same 2h cycle as the PR cache.
  const gitAsOf = $derived(relTime(service.git_fetched_at));

  // Project-level work span: earliest first_commit_at → latest
  // last_commit_at across all branches (local-git only).
  const projectSpanMinutes = $derived.by(() => {
    let min: number | null = null;
    let max: number | null = null;
    for (const br of branches) {
      const f = epoch(br.first_commit_at);
      const l = epoch(br.last_commit_at);
      if (f !== null) min = min === null ? f : Math.min(min, f);
      if (l !== null) max = max === null ? l : Math.max(max, l);
    }
    if (min === null || max === null || max < min) return null;
    return Math.round((max - min) / 60000);
  });

  // TIME-TO-SHIP — prefer the true PR-based span (first commit → PR
  // merge, from gh) when merged PRs exist; else fall back to the local
  // first→last-commit work span. Returns { minutes, caption } or null.
  const timeToShip = $derived.by(() => {
    if (latestPr && latestPr.time_to_ship_minutes > 0) {
      return {
        minutes: latestPr.time_to_ship_minutes,
        caption: `#${latestPr.number} opened → merged`
      };
    }
    if (projectSpanMinutes !== null) {
      return { minutes: projectSpanMinutes, caption: 'first → last commit' };
    }
    return null;
  });

  // AI TIME — per-CLI minutes; Claude + Codex always shown (Codex at 0 too).
  const minutesByCli = $derived(service.minutes_by_cli ?? {});
  const claudeMinutes = $derived(minutesByCli['claude'] ?? 0);
  const codexMinutes = $derived(minutesByCli['codex'] ?? 0);
  // Any non-claude / non-codex CLIs, so nothing is silently dropped.
  const otherCli = $derived.by(() =>
    Object.entries(minutesByCli)
      .filter(([cli, min]) => cli !== 'claude' && cli !== 'codex' && min > 0)
      .sort((a, b) => b[1] - a[1])
  );

  // ---- worklog markdown (escape-first, minimal subset) --------------------

  // Inline span: only **bold** is supported. Input is already escaped.
  function inline(text: string): string {
    return text.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
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

  // ---- ship / risk metadata -----------------------------------------------

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
</script>

<article class="svc-panel ad-card">
  <!-- ===== header ===== -->
  <header class="svc-hd">
    <div class="svc-id">
      <div class="svc-id-line">
        <h3 class="svc-repo">{service.repo}</h3>
        {#if service.manual_only}
          <span class="svc-manual" title="No AI session recorded for this repo">
            manual — no AI session
          </span>
        {/if}
        {#if gitAsOf}
          <span
            class="svc-freshness ad-mono"
            title="origin/* refs last refreshed via `git fetch`"
          >
            git · {gitAsOf}
          </span>
        {/if}
        {#if prsAsOf}
          <span
            class="svc-freshness ad-mono"
            title="GitHub PR data last fetched (cached up to 2h)"
          >
            gh · {prsAsOf}
          </span>
        {/if}
      </div>
      <div class="svc-path ad-mono ad-truncate" title={service.project_path}>
        {service.project_path}
      </div>
    </div>

    {#if service.risks && service.risks.length > 0}
      <ul class="risk-list" aria-label="Repository risks">
        {#each service.risks as r, ri (ri)}
          {@const meta = risk(r.kind)}
          {@const age = hm(r.age_minutes)}
          {@const brLabel = r.branch && r.branch !== 'HEAD' ? r.branch : ''}
          {@const rCommits = r.commits ?? []}
          {@const rFiles = r.files ?? []}
          {@const hasEv =
            (r.kind === 'unpushed' && rCommits.length > 0) ||
            (r.kind === 'done-uncommitted' && rFiles.length > 0)}
          {@const isOpen = riskExpanded.has(ri)}
          <li class="risk-chip-wrap">
            <button
              type="button"
              class="risk-chip {meta.cls}"
              class:risk-chip--open={isOpen}
              onclick={() => toggleRisk(ri)}
              aria-expanded={isOpen}
              aria-controls="rk-ev-{ri}"
              disabled={!hasEv}
              title={hasEv
                ? isOpen
                  ? 'Hide evidence'
                  : 'Show which commits / files'
                : 'No evidence list available'}
            >
              <span class="risk-kind">{meta.label}</span>
              {#if brLabel}
                <span class="risk-branch ad-mono" title={r.worktree_path}>{brLabel}</span>
              {/if}
              <span class="risk-detail">{r.detail}</span>
              {#if age}<span class="risk-age ad-tnum">{age} ago</span>{/if}
              {#if hasEv}
                <span
                  class="risk-caret"
                  class:risk-caret--open={isOpen}
                  aria-hidden="true">▸</span>
              {/if}
            </button>
            {#if isOpen}
              <div id="rk-ev-{ri}" class="risk-ev">
                {#if r.kind === 'unpushed'}
                  <ul class="risk-ev-list">
                    {#each rCommits.slice(0, 10) as c, ci (ci)}
                      <li class="risk-ev-row">
                        <code class="risk-ev-sha ad-mono">{c.sha}</code>
                        <span class="risk-ev-subject" title={c.subject}>{c.subject}</span>
                      </li>
                    {/each}
                    {#if rCommits.length > 10}
                      <li class="risk-ev-more">
                        + {rCommits.length - 10} more shown of {rCommits.length} listed
                      </li>
                    {/if}
                  </ul>
                {:else if r.kind === 'done-uncommitted'}
                  <ul class="risk-ev-list">
                    {#each rFiles.slice(0, 12) as f, fi (fi)}
                      <li class="risk-ev-file ad-mono" title={f}>{f}</li>
                    {/each}
                    {#if rFiles.length > 12}
                      <li class="risk-ev-more">+ {rFiles.length - 12} more</li>
                    {/if}
                  </ul>
                {/if}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </header>

  <!-- ===== metric tile row ===== -->
  <div class="tile-row" role="list" aria-label="Project metrics">
    <!-- TASKS -->
    <div class="tile" role="listitem">
      {#if tickets.length > 0 || otherTickets.length > 0}
        <span class="tile-label">Tasks</span>
        <span class="tile-value ad-tnum">{tickets.length + otherTickets.length}</span>
        <ul class="tile-chips">
          {#each tickets as t, ti (ti)}
            <li class="mini-chip ad-mono" title="{t} · committed in window">{t}</li>
          {/each}
          {#each otherTickets as t, oi (oi)}
            <li
              class="mini-chip mini-chip--secondary ad-mono"
              title="{t} · open in another worktree (no commits in window)"
            >{t}</li>
          {/each}
        </ul>
        {#if otherTickets.length > 0}
          <span class="tile-caption">
            {tickets.length} in window
            {#if otherTickets.length > 0}
              · {otherTickets.length} other worktree{otherTickets.length === 1 ? '' : 's'}
            {/if}
          </span>
        {/if}
      {:else}
        <span class="tile-label">Branches</span>
        <span class="tile-value ad-tnum">{branches.length}</span>
        <span class="tile-caption">no tickets</span>
      {/if}
    </div>

    <!-- SHIPPED -->
    <div class="tile" role="listitem">
      <span class="tile-label">Shipped</span>
      <span class="tile-value">
        <span class="ad-tnum">{shipHeadline.value}</span>
        <span class="tile-value-unit">{shipHeadline.unit}</span>
      </span>
      <span class="tile-caption ad-tnum">
        {#if shipCounts.merged > 0}<span class="dot-sep dot-merged">{shipCounts.merged} merged</span>{/if}
        {#if shipCounts.pushed > 0}<span class="dot-sep">· {shipCounts.pushed} pushed</span>{/if}
        {#if shipCounts.local > 0}<span class="dot-sep">· {shipCounts.local} local</span>{/if}
        {#if shipCounts.merged === 0 && shipCounts.pushed === 0 && shipCounts.local === 0}
          nothing shipped
        {/if}
      </span>
    </div>

    <!-- COMMITS -->
    <div class="tile" role="listitem">
      <span class="tile-label">Commits</span>
      <span class="tile-value ad-tnum">{uniqueUserCommits}</span>
      <span class="tile-caption">unique · you</span>
    </div>

    <!-- PRs MERGED -->
    <div class="tile" role="listitem">
      <span class="tile-label">PRs merged</span>
      {#if mergedPrs.length > 0}
        <span class="tile-value ad-tnum">{mergedPrs.length}</span>
        <ul class="tile-chips">
          {#each mergedPrs as pr, pi (pi)}
            <li class="mini-chip ad-mono ad-tnum" title={pr.title}>#{pr.number}</li>
          {/each}
        </ul>
        {#if prsAsOf}
          <span class="tile-caption" title="GitHub data is cached; refresh interval defaults to 2h">
            from GitHub · {prsAsOf}
          </span>
        {/if}
      {:else}
        <span class="tile-value tile-value--empty">—</span>
        <span class="tile-caption">
          {#if prsAsOf}no PRs in window · checked {prsAsOf}{:else}gh data unavailable{/if}
        </span>
      {/if}
    </div>

    <!-- TIME-TO-SHIP -->
    <div class="tile" role="listitem">
      <span class="tile-label">Time to ship</span>
      {#if timeToShip !== null}
        <span class="tile-value ad-tnum">{hmPlain(timeToShip.minutes)}</span>
        <span class="tile-caption">{timeToShip.caption}</span>
      {:else}
        <span class="tile-value tile-value--empty">—</span>
        <span class="tile-caption">first → last commit</span>
      {/if}
    </div>

    <!-- AI TIME -->
    <div class="tile tile--wide" role="listitem">
      <span class="tile-label">AI time</span>
      <ul class="ai-split">
        <li class="ai-row">
          <span class="ai-name ai-name--claude">Claude</span>
          <span class="ai-time ad-mono ad-tnum">{hmPlain(claudeMinutes)}</span>
        </li>
        <li class="ai-row">
          <span class="ai-name ai-name--codex">Codex</span>
          <span class="ai-time ad-mono ad-tnum">{hmPlain(codexMinutes)}</span>
        </li>
        {#each otherCli as [cli, min], oi (oi)}
          <li class="ai-row">
            <span class="ai-name">{cliLabel(cli)}</span>
            <span class="ai-time ad-mono ad-tnum">{hmPlain(min)}</span>
          </li>
        {/each}
      </ul>
    </div>
  </div>

  <!-- ===== expandable detail ===== -->
  <div class="detail">
    <button
      type="button"
      class="detail-toggle"
      onclick={() => (detailOpen = !detailOpen)}
      aria-expanded={detailOpen}
      aria-controls="svc-detail"
    >
      <span class="caret" class:open={detailOpen} aria-hidden="true">▸</span>
      {detailOpen ? 'Hide detail' : 'Show detail'}
      <span class="detail-meta ad-mono">
        {branches.length} branch{branches.length === 1 ? '' : 'es'}
        {#if mergedPrs.length > 0} · {mergedPrs.length} merged PR{mergedPrs.length === 1 ? '' : 's'}{/if}
        {#if service.reflection_markdown} · worklog note{/if}
      </span>
    </button>

    {#if detailOpen}
      <div id="svc-detail" class="detail-body">
        <!-- ---- merged PRs (gh-sourced) ---- -->
        {#if mergedPrs.length > 0}
          <section class="pr-section">
            <h4 class="section-h">
              Merged PRs
              <span class="section-meta ad-mono">
                from GitHub{#if prsAsOf} · {prsAsOf}{/if}
              </span>
            </h4>
            <ul class="pr-list">
              {#each mergedPrs as pr, pi (pi)}
                <li class="pr-row">
                  <span class="pr-num ad-mono ad-tnum">#{pr.number}</span>
                  <span class="pr-title ad-truncate" title={pr.title}>{pr.title}</span>
                  <span class="pr-meta ad-mono ad-tnum">
                    merged {shortDateTime(pr.merged_at)}
                    {#if pr.time_to_ship_minutes > 0}
                      · shipped in {hmPlain(pr.time_to_ship_minutes)}
                    {/if}
                  </span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        <!-- ---- branches ---- -->
        {#if branches.length === 0}
          <p class="svc-empty">No branch activity in this window.</p>
        {:else}
          <ul class="branch-list">
            {#each branches as br, bi (bi)}
              {@const sm = ship(br.ship)}
              {@const time = hm(br.attributed_minutes)}
              {@const isOpen = expanded.has(bi)}
              {@const commitCount = br.commits ? br.commits.length : 0}
              <li class="branch">
                <div class="branch-row">
                  <span class="ship-badge ad-badge {sm.cls}">{sm.label}</span>

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

                {#if commitCount > 0}
                  <button
                    type="button"
                    class="commit-toggle"
                    onclick={() => toggle(bi)}
                    aria-expanded={isOpen}
                    aria-controls="commits-{bi}"
                  >
                    <span class="caret" class:open={isOpen} aria-hidden="true">▸</span>
                    {commitCount} commit{commitCount === 1 ? '' : 's'}
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
                          <code class="commit-sha ad-mono">{shortSha(c.sha)}</code>
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

        <!-- ---- worklog note ---- -->
        {#if service.reflection_markdown}
          <details class="worklog" bind:open={worklogOpen}>
            <summary class="worklog-summary">
              <span class="caret" class:open={worklogOpen} aria-hidden="true">▸</span>
              Worklog note
            </summary>
            <!-- renderWorklog() escapes all HTML entities before applying a
                 minimal Markdown subset, so this @html input is safe. -->
            <div class="worklog-body">
              {@html renderWorklog(service.reflection_markdown)}
            </div>
          </details>
        {/if}
      </div>
    {/if}
  </div>
</article>

<style>
  .svc-panel {
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 10px;
    overflow: hidden;
  }

  /* ---- header ---- */
  .svc-hd {
    padding: 14px 16px 12px;
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

  .svc-freshness {
    font-size: 10px;
    font-weight: 500;
    color: var(--ad-faint);
    padding: 1px 6px;
    border-radius: 4px;
    background: color-mix(in oklch, var(--ad-bg-2) 50%, transparent);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
    letter-spacing: 0.02em;
  }

  .svc-path {
    margin-top: 3px;
    font-size: var(--ad-fs-xs);
    color: var(--ad-faint);
    max-width: 100%;
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

  .risk-chip-wrap {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
    max-width: 100%;
  }

  .risk-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    font: inherit;
    font-size: 11px;
    line-height: 1.4;
    padding: 3px 9px;
    border-radius: 7px;
    border: 1px solid transparent;
    text-align: left;
    cursor: pointer;
    transition: background 100ms ease;
  }
  .risk-chip:disabled {
    cursor: default;
  }
  .risk-chip:hover:not(:disabled) {
    filter: brightness(1.08);
  }

  .risk-caret {
    margin-left: 2px;
    font-size: 9px;
    color: currentColor;
    opacity: 0.7;
    transition: transform 160ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .risk-caret--open {
    transform: rotate(90deg);
  }

  .risk-ev {
    padding: 6px 9px 7px;
    border-radius: 7px;
    background: color-mix(in oklch, var(--ad-bg-2) 60%, transparent);
    border: 1px solid var(--ad-border-soft);
  }
  .risk-ev-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .risk-ev-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
    font-size: 11px;
  }
  .risk-ev-sha {
    color: var(--ad-fg-2);
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 4px;
    padding: 1px 5px;
    flex: none;
  }
  .risk-ev-subject {
    color: var(--ad-muted);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
  }
  .risk-ev-file {
    font-size: 11px;
    color: var(--ad-fg-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .risk-ev-more {
    font-size: 10.5px;
    color: var(--ad-faint);
    font-style: italic;
  }

  .risk-kind {
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-size: 9.5px;
    white-space: nowrap;
  }

  .risk-branch {
    font-size: 10.5px;
    font-weight: 500;
    color: var(--ad-fg-2);
    padding: 1px 6px;
    border-radius: 4px;
    background: color-mix(in oklch, var(--ad-bg-2) 60%, transparent);
    white-space: nowrap;
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .risk-detail {
    color: var(--ad-fg-2);
  }

  .risk-age {
    color: var(--ad-faint);
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

  /* ---- metric tile row ---- */
  .tile-row {
    display: grid;
    grid-template-columns: repeat(6, minmax(0, 1fr));
    gap: 10px;
    padding: 14px 16px;
  }

  @media (max-width: 1080px) {
    .tile-row {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
  @media (max-width: 560px) {
    .tile-row {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .tile {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
    padding: 11px 12px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 9px;
  }

  /* AI-time tile carries two rows — let it span wider when room allows. */
  .tile--wide {
    grid-column: span 1;
  }

  .tile-label {
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--ad-faint);
  }

  .tile-value {
    display: flex;
    align-items: baseline;
    gap: 5px;
    font-size: 26px;
    font-weight: 650;
    line-height: 1.05;
    letter-spacing: -0.02em;
    color: var(--ad-fg);
  }

  .tile-value--empty {
    color: var(--ad-faint);
    font-weight: 500;
  }

  .tile-value-unit {
    font-size: 12px;
    font-weight: 600;
    color: var(--ad-muted);
    letter-spacing: 0;
  }

  .tile-caption {
    font-size: 10px;
    color: var(--ad-faint);
    line-height: 1.4;
  }

  .dot-sep {
    white-space: nowrap;
  }
  .dot-merged {
    color: var(--ad-live);
    font-weight: 600;
  }

  /* chips inside tiles (ticket IDs, PR numbers) */
  .tile-chips {
    list-style: none;
    margin: 1px 0 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    overflow: hidden;
  }

  .mini-chip {
    font-size: 10px;
    font-weight: 500;
    padding: 1.5px 6px;
    border-radius: 5px;
    color: var(--ad-fg-2);
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Secondary chip: tickets active in OTHER worktrees with no
     in-window commits. Dimmer so they read as "open elsewhere". */
  .mini-chip--secondary {
    color: var(--ad-faint);
    background: transparent;
    border-style: dashed;
  }

  /* AI-time split rows */
  .ai-split {
    list-style: none;
    margin: 2px 0 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .ai-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
  }

  .ai-name {
    font-size: 11px;
    font-weight: 600;
    color: var(--ad-fg-2);
  }
  .ai-name--claude {
    color: var(--ad-claude);
  }
  .ai-name--codex {
    color: var(--ad-codex);
  }

  .ai-time {
    font-size: 13px;
    font-weight: 600;
    color: var(--ad-fg);
  }

  /* ---- detail block ---- */
  .detail {
    border-top: 1px solid var(--ad-border-soft);
  }

  .detail-toggle {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 9px 16px;
    background: transparent;
    border: 0;
    cursor: pointer;
    font-family: var(--ad-font-mono);
    font-size: 11px;
    color: var(--ad-faint);
    transition: color 120ms ease;
  }
  .detail-toggle:hover {
    color: var(--ad-fg-2);
  }

  .detail-meta {
    margin-left: auto;
    font-size: 10.5px;
    color: var(--ad-faint);
  }

  .caret {
    display: inline-block;
    font-size: 9px;
    transition: transform 160ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  .caret.open {
    transform: rotate(90deg);
  }

  .detail-body {
    border-top: 1px solid var(--ad-border-soft);
  }

  /* ---- merged-PR section ---- */
  .pr-section {
    padding: 12px 16px;
    border-bottom: 1px solid var(--ad-border-soft);
    background: color-mix(in oklch, var(--ad-bg-2) 30%, transparent);
  }

  .section-h {
    margin: 0 0 8px;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ad-faint);
    display: flex;
    align-items: baseline;
    gap: 8px;
  }

  .section-meta {
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.02em;
    color: var(--ad-faint);
    text-transform: none;
  }

  .pr-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .pr-row {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }

  .pr-num {
    flex: none;
    font-size: 12px;
    font-weight: 600;
    color: var(--ad-live);
    background: color-mix(in oklch, var(--ad-live) 12%, transparent);
    border: 1px solid color-mix(in oklch, var(--ad-live) 35%, var(--ad-border));
    border-radius: 5px;
    padding: 1.5px 7px;
  }

  .pr-title {
    flex: 1 1 auto;
    min-width: 0;
    font-size: var(--ad-fs-sm);
    color: var(--ad-fg-2);
  }

  .pr-meta {
    flex: none;
    font-size: 10.5px;
    color: var(--ad-faint);
    white-space: nowrap;
  }

  /* ---- branches ---- */
  .svc-empty {
    margin: 0;
    padding: 18px 16px;
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
    padding: 12px 16px;
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
    /* Clamp long narratives to ~3 lines. */
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
    padding: 9px 16px;
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

  .worklog-body {
    padding: 0 16px 13px;
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
