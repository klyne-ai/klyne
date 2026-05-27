<!--
  WhatWasDoneCard — per-service productivity card.

  v2 (2026-05-27): when `card.narrative` is present, render the redesigned
  layout — service summary paragraph + 4 stat tiles + sectioned per-ticket
  narrative cards (Features shipped / Bugs fixed / Decisions /
  Investigated · no fix landed) + optional followup line.

  v1 fallback: legacy tldr + chip row + expandable detail list. Used for
  cards persisted before the narrative redesign and for the deterministic
  ComposeWWD path (cold start with no LLM compile yet).

  Visual language matches the existing `.refl-card` block in
  routes/productivity/+page.svelte; styles use the shared
  `var(--fg)`, `var(--bg-card)`, etc. tokens — no raw hex.
-->
<script lang="ts">
  import type { WhatWasDoneCard, WWDDetail, WWDKind, WWDNarrativeCard, WWDRef } from '$lib/api.js';

  interface Props {
    card: WhatWasDoneCard;
    /** Optional click handler — opens the originating worklog entry. */
    onOpenEntry?: (session_id: string) => void;
  }
  let { card, onOpenEntry }: Props = $props();

  const hasNarrative = $derived(!!card.narrative && card.narrative.cards.length > 0);

  // --- v1 legacy state ---------------------------------------------------
  let expanded = $state(false);
  const details: WWDDetail[] = $derived(card.tier2?.details ?? []);
  const entryCount: number = $derived(details.length);

  type PillRow = { key: string; label: string; count: number; tone: Tone };
  type Tone = 'ok' | 'warn' | 'alert' | 'info' | 'muted' | 'accent';

  function kindTone(kind: WWDKind | string): Tone {
    switch (kind) {
      case 'SHIPPED':      return 'ok';
      case 'MAJOR':        return 'info';
      case 'FIXED':        return 'alert';
      case 'DECISION':     return 'accent';
      case 'INVESTIGATED': return 'muted';
      case 'IN_PROGRESS':  return 'warn';
      default:             return 'muted';
    }
  }

  const PILL_ORDER: readonly { key: string; alt?: string; label: string; kind: WWDKind }[] = [
    { key: 'shipped',      label: 'shipped',      kind: 'SHIPPED' },
    { key: 'major',        label: 'major',        kind: 'MAJOR' },
    { key: 'fixed',        label: 'fixed',        kind: 'FIXED' },
    { key: 'decision',     alt: 'decisions',      label: 'decisions',     kind: 'DECISION' },
    { key: 'investigated', label: 'investigated', kind: 'INVESTIGATED' },
    { key: 'in_progress',  label: 'in progress',  kind: 'IN_PROGRESS' },
  ] as const;

  const pillRows: PillRow[] = $derived.by(() => {
    const pc = (card.tier1?.pill_counts ?? {}) as Record<string, number>;
    const rows: PillRow[] = [];
    for (const p of PILL_ORDER) {
      const n = pc[p.key] ?? (p.alt ? pc[p.alt] : 0) ?? 0;
      if (n > 0) rows.push({ key: p.key, label: p.label, count: n, tone: kindTone(p.kind) });
    }
    return rows;
  });

  // --- v2 narrative state ------------------------------------------------
  type NarrativeSection = { key: string; label: string; kinds: WWDKind[]; cards: WWDNarrativeCard[] };

  const SECTION_ORDER: readonly { key: string; label: string; kinds: WWDKind[] }[] = [
    { key: 'shipped',      label: 'Features shipped',             kinds: ['SHIPPED', 'MAJOR'] },
    { key: 'fixed',        label: 'Bugs fixed',                   kinds: ['FIXED'] },
    { key: 'decisions',    label: 'Decisions',                    kinds: ['DECISION'] },
    { key: 'investigated', label: 'Investigated · no fix landed', kinds: ['INVESTIGATED'] },
    { key: 'in_progress',  label: 'In progress',                  kinds: ['IN_PROGRESS'] },
  ] as const;

  const sections: NarrativeSection[] = $derived.by(() => {
    if (!card.narrative) return [];
    const cards = card.narrative.cards;
    const out: NarrativeSection[] = [];
    for (const s of SECTION_ORDER) {
      const matched = cards.filter(c => s.kinds.includes(c.kind));
      if (matched.length > 0) {
        out.push({ key: s.key, label: s.label, kinds: [...s.kinds], cards: matched });
      }
    }
    return out;
  });

  const stats = $derived(card.narrative?.stats);

  // --- markdown body renderer -------------------------------------------
  // Minimal markdown: `inline code`, **bold**, _italic_. Anything else
  // renders verbatim. We avoid pulling in a full markdown lib for one
  // narrow case.
  function renderBody(s: string): string {
    const esc = (x: string) => x
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
    let html = esc(s);
    html = html.replace(/`([^`\n]+)`/g, '<code>$1</code>');
    html = html.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/_([^_\n]+)_/g, '<em>$1</em>');
    // Paragraph breaks on blank lines, line breaks on single newlines.
    const paras = html.split(/\n{2,}/).map(p => p.replace(/\n/g, '<br>'));
    return paras.map(p => `<p>${p}</p>`).join('');
  }

  function refClass(r: WWDRef): string {
    return `ref ref-${r.type}`;
  }

  function handleRowClick(d: WWDDetail) {
    if (onOpenEntry) onOpenEntry(d.session_id);
  }
  function handleRowKey(e: KeyboardEvent, d: WWDDetail) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleRowClick(d);
    }
  }
  function toggle() { expanded = !expanded; }
</script>

<article class="wwd-card">
  {#if hasNarrative && card.narrative}
    <!-- ==================== V2 NARRATIVE LAYOUT ==================== -->
    <header class="nv-head">
      <span class="mono nv-service">{card.service}</span>
      {#if card.llm_compiled}
        <span class="nv-badge mono" title="Card composed by /klyne:productivity-sync">opus · auto</span>
      {/if}
    </header>

    <div class="nv-tiles">
      <div class="nv-tile">
        <div class="nv-tile-num">{stats?.shipped ?? 0}</div>
        <div class="nv-tile-label">features shipped</div>
      </div>
      <div class="nv-tile">
        <div class="nv-tile-num">{stats?.fixed ?? 0}</div>
        <div class="nv-tile-label">bugs fixed</div>
      </div>
      <div class="nv-tile">
        <div class="nv-tile-num">{stats?.decisions ?? 0}</div>
        <div class="nv-tile-label">decisions</div>
      </div>
      <div class="nv-tile">
        <div class="nv-tile-num">{stats?.investigated ?? 0}</div>
        <div class="nv-tile-label">investigated</div>
      </div>
    </div>

    {#if card.narrative.summary}
      <p class="nv-summary">{card.narrative.summary}</p>
    {/if}

    {#each sections as section (section.key)}
      <section class="nv-section">
        <h4 class="nv-section-head">{section.label}</h4>
        {#each section.cards as c, i (c.kind + '|' + (c.ticket_id ?? '') + '|' + i)}
          {@const tone = kindTone(c.kind)}
          <article class="nv-card">
            <div class="nv-card-stripe nv-card-stripe-{tone}"></div>
            <div class="nv-card-body">
              <header class="nv-card-head">
                <span class="nv-card-kind nv-card-kind-{tone}">{c.kind.toLowerCase().replace('_', ' ')}</span>
                {#if c.ticket_id}
                  <span class="nv-card-ticket mono">·  {c.ticket_id}</span>
                {/if}
              </header>
              <h5 class="nv-card-title">{c.title}</h5>
              <div class="nv-card-prose">{@html renderBody(c.body)}</div>
              {#if c.refs && c.refs.length > 0}
                <div class="nv-card-refs">
                  {#each c.refs as r, ri (r.type + '|' + r.text + '|' + ri)}
                    <span class={refClass(r)}>{r.text}</span>
                  {/each}
                </div>
              {/if}
            </div>
          </article>
        {/each}
      </section>
    {/each}

    {#if card.narrative.followup}
      <aside class="nv-followup">
        <span class="nv-followup-label">Open question for tomorrow</span>
        <p>{card.narrative.followup}</p>
      </aside>
    {/if}
  {:else}
    <!-- ==================== V1 LEGACY LAYOUT ==================== -->
    <header class="wwd-head">
      <span class="mono wwd-service">{card.service}</span>
      {#if card.llm_compiled}
        <span class="wwd-sonnet-badge mono" title="Card composed by /klyne:productivity-sync (Sonnet)">sonnet · auto</span>
      {/if}
      <div class="wwd-pills">
        {#each pillRows as r (r.key)}
          <span class="pill pill-{r.tone} wwd-pill">{r.count} {r.label}</span>
        {/each}
      </div>
      <span class="grow"></span>
      <div class="wwd-meta">
        <span class="mono dim">{card.tier1?.turn_count ?? 0} turns</span>
        <span class="mono dim wwd-sep">·</span>
        <span class="mono dim">{card.tier1?.commit_count ?? 0} commits</span>
      </div>
    </header>

    {#if card.tier1?.tldr}
      <p class="wwd-tldr">{card.tier1.tldr}</p>
    {/if}

    {#if card.tier1?.top_evidence && card.tier1.top_evidence.length > 0}
      <div class="wwd-top-ev">
        {#each card.tier1.top_evidence as ev, i (ev + '|' + i)}
          {#if i > 0}<span class="mono dim wwd-sep">·</span>{/if}
          <span class="mono wwd-ev-tok">{ev}</span>
        {/each}
      </div>
    {/if}

    {#if entryCount > 0}
      <button
        class="wwd-disclose"
        onclick={toggle}
        aria-expanded={expanded}
        type="button"
      >
        <span class="wwd-caret" class:open={expanded}>▸</span>
        <span class="mono">{expanded ? 'hide' : 'view'} {entryCount} {entryCount === 1 ? 'entry' : 'entries'}</span>
      </button>

      {#if expanded}
        <ol class="wwd-details">
          {#each details as d, i (d.session_id + '|' + i)}
            {@const tone = kindTone(d.kind)}
            <li>
              {#if onOpenEntry}
                <div
                  class="wwd-detail clickable"
                  role="button"
                  tabindex="0"
                  onclick={() => handleRowClick(d)}
                  onkeydown={(e) => handleRowKey(e, d)}
                  title="Open worklog entry"
                >
                  <span class="pill pill-{tone} wwd-detail-chip">{d.kind}</span>
                  <span class="mono wwd-detail-when">{d.when}</span>
                  <div class="wwd-detail-text">{d.text}</div>
                  <div class="wwd-detail-ev">
                    {#each d.evidence as ev, ei (ev + '|' + ei)}
                      <span class="mono dim">{ev}</span>
                    {/each}
                  </div>
                </div>
              {:else}
                <div class="wwd-detail">
                  <span class="pill pill-{tone} wwd-detail-chip">{d.kind}</span>
                  <span class="mono wwd-detail-when">{d.when}</span>
                  <div class="wwd-detail-text">{d.text}</div>
                  <div class="wwd-detail-ev">
                    {#each d.evidence as ev, ei (ev + '|' + ei)}
                      <span class="mono dim">{ev}</span>
                    {/each}
                  </div>
                </div>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
    {/if}
  {/if}
</article>

<style>
  /* ============================ shared ============================ */
  .wwd-card {
    border: 1px solid var(--border-soft);
    border-radius: 10px;
    background: var(--bg-card-2);
    padding: 18px 22px 22px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .mono { font-family: var(--font-mono); }
  .dim  { color: var(--fg-dim); }

  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 8px;
    border-radius: 999px;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.4;
    border: 1px solid var(--border);
    color: var(--fg-soft);
    background: var(--bg-card-2);
    white-space: nowrap;
  }
  .pill-ok      { color: var(--ok);     border-color: color-mix(in oklch, var(--ok)     35%, transparent); background: color-mix(in oklch, var(--ok)     10%, var(--bg-card-2)); }
  .pill-warn    { color: var(--warn);   border-color: color-mix(in oklch, var(--warn)   35%, transparent); background: color-mix(in oklch, var(--warn)   10%, var(--bg-card-2)); }
  .pill-alert   { color: var(--alert);  border-color: color-mix(in oklch, var(--alert)  35%, transparent); background: color-mix(in oklch, var(--alert)  10%, var(--bg-card-2)); }
  .pill-info    { color: var(--info);   border-color: color-mix(in oklch, var(--info)   35%, transparent); background: color-mix(in oklch, var(--info)   10%, var(--bg-card-2)); }
  .pill-accent  { color: var(--accent); border-color: color-mix(in oklch, var(--accent) 35%, transparent); background: color-mix(in oklch, var(--accent) 10%, var(--bg-card-2)); }
  .pill-muted   { color: var(--fg-muted); border-color: var(--border-hair); background: var(--bg-inset); }

  /* ============================ v2 narrative ============================ */
  .nv-head {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .nv-service {
    color: var(--fg);
    font-size: 15px;
    font-weight: 600;
    letter-spacing: 0.01em;
  }
  .nv-badge {
    display: inline-flex;
    align-items: center;
    padding: 1px 7px;
    border-radius: 4px;
    font-size: 10px;
    letter-spacing: 0.04em;
    color: var(--accent);
    border: 1px solid color-mix(in oklch, var(--accent) 35%, transparent);
    background: color-mix(in oklch, var(--accent) 8%, var(--bg-card-2));
  }

  .nv-tiles {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 10px;
  }
  .nv-tile {
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    padding: 14px 14px 12px;
    text-align: center;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }
  .nv-tile-num {
    font-family: var(--font-mono);
    font-size: 24px;
    line-height: 1;
    font-weight: 600;
    color: var(--fg);
    font-variant-numeric: tabular-nums;
  }
  .nv-tile-label {
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--fg-muted);
    letter-spacing: 0.02em;
  }

  .nv-summary {
    margin: 0;
    color: var(--fg-soft);
    font-size: 13.5px;
    line-height: 1.55;
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    padding: 12px 14px;
    max-width: none;
  }

  .nv-section {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .nv-section-head {
    margin: 4px 0 0;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .nv-card {
    display: flex;
    gap: 0;
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    background: var(--bg-card);
    overflow: hidden;
  }
  .nv-card-stripe {
    width: 4px;
    flex-shrink: 0;
  }
  .nv-card-stripe-ok      { background: var(--ok); }
  .nv-card-stripe-warn    { background: var(--warn); }
  .nv-card-stripe-alert   { background: var(--alert); }
  .nv-card-stripe-info    { background: var(--info); }
  .nv-card-stripe-accent  { background: var(--accent); }
  .nv-card-stripe-muted   { background: var(--fg-dim); }

  .nv-card-body {
    padding: 12px 16px 14px;
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .nv-card-head {
    display: flex;
    align-items: center;
    gap: 4px;
    font-family: var(--font-mono);
    font-size: 10.5px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .nv-card-kind { font-weight: 600; }
  .nv-card-kind-ok      { color: var(--ok); }
  .nv-card-kind-warn    { color: var(--warn); }
  .nv-card-kind-alert   { color: var(--alert); }
  .nv-card-kind-info    { color: var(--info); }
  .nv-card-kind-accent  { color: var(--accent); }
  .nv-card-kind-muted   { color: var(--fg-muted); }
  .nv-card-ticket {
    color: var(--fg-muted);
    font-weight: 500;
  }
  .nv-card-title {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    color: var(--fg);
    line-height: 1.4;
  }
  .nv-card-prose {
    color: var(--fg-soft);
    font-size: 13px;
    line-height: 1.55;
  }
  .nv-card-prose :global(p) { margin: 0 0 8px; }
  .nv-card-prose :global(p:last-child) { margin-bottom: 0; }
  .nv-card-prose :global(code) {
    font-family: var(--font-mono);
    font-size: 12px;
    padding: 1px 5px;
    border-radius: 3px;
    background: var(--bg-inset);
    color: var(--fg);
    border: 1px solid var(--border-hair);
  }
  .nv-card-prose :global(strong) { color: var(--fg); font-weight: 600; }
  .nv-card-prose :global(em) { color: var(--fg-soft); font-style: italic; }

  .nv-card-refs {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 4px;
  }
  .ref {
    font-family: var(--font-mono);
    font-size: 10.5px;
    padding: 2px 7px;
    border-radius: 4px;
    border: 1px solid var(--border-hair);
    color: var(--fg-soft);
    background: var(--bg-inset);
    line-height: 1.4;
  }
  .ref-file    { color: var(--fg-soft); }
  .ref-branch  { color: var(--info);  border-color: color-mix(in oklch, var(--info)   25%, transparent); background: color-mix(in oklch, var(--info)   6%, var(--bg-inset)); }
  .ref-pr      { color: var(--accent); border-color: color-mix(in oklch, var(--accent) 30%, transparent); background: color-mix(in oklch, var(--accent) 8%, var(--bg-inset)); font-weight: 600; }
  .ref-commit  { color: var(--fg-soft); font-variant-numeric: tabular-nums; }
  .ref-ticket  { color: var(--accent); }
  .ref-test    { color: var(--fg-muted); }
  .ref-session { color: var(--fg-dim); }

  .nv-followup {
    border: 1px solid color-mix(in oklch, var(--warn) 25%, transparent);
    background: color-mix(in oklch, var(--warn) 6%, var(--bg-inset));
    border-radius: 8px;
    padding: 10px 14px 12px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .nv-followup-label {
    font-family: var(--font-mono);
    font-size: 10px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--warn);
  }
  .nv-followup p {
    margin: 0;
    color: var(--fg-soft);
    font-size: 12.5px;
    line-height: 1.5;
  }

  /* ============================ v1 legacy ============================ */
  .wwd-head {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 11.5px;
    color: var(--fg-muted);
    flex-wrap: wrap;
  }
  .wwd-service {
    color: var(--fg);
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.01em;
  }
  .wwd-sonnet-badge {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 10px;
    letter-spacing: 0.04em;
    color: var(--accent);
    border: 1px solid color-mix(in oklch, var(--accent) 35%, transparent);
    background: color-mix(in oklch, var(--accent) 8%, var(--bg-card-2));
  }
  .wwd-pills { display: flex; gap: 6px; flex-wrap: wrap; }
  .wwd-pill { font-size: 10.5px; letter-spacing: 0.02em; padding: 1px 7px; }
  .grow { flex: 1; }
  .wwd-meta {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-variant-numeric: tabular-nums;
  }
  .wwd-sep { color: var(--fg-dim); }
  .wwd-tldr {
    margin: 0;
    color: var(--fg-soft);
    font-size: 13px;
    line-height: 1.5;
    max-width: 88ch;
  }
  .wwd-top-ev {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    font-size: 10.5px;
  }
  .wwd-ev-tok {
    color: var(--fg-soft);
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    padding: 1px 6px;
    border-radius: 4px;
    letter-spacing: 0.01em;
  }
  .wwd-disclose {
    align-self: flex-start;
    background: transparent;
    border: 1px solid var(--border-hair);
    border-radius: 6px;
    padding: 3px 8px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    transition: color var(--t-fast), border-color var(--t-fast), background var(--t-fast);
  }
  .wwd-disclose:hover {
    color: var(--fg);
    border-color: var(--border-soft);
    background: var(--bg-inset);
  }
  .wwd-caret { display: inline-block; transition: transform 120ms ease-out; }
  .wwd-caret.open { transform: rotate(90deg); }
  .wwd-details {
    margin: 4px 0 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 6px;
    border-top: 1px dashed var(--border-hair);
    padding-top: 10px;
  }
  .wwd-detail {
    display: grid;
    grid-template-columns: 96px 48px 1fr auto;
    gap: 10px;
    align-items: start;
    padding: 6px 8px;
    border-radius: 6px;
    transition: background var(--t-fast);
  }
  .wwd-detail.clickable { cursor: pointer; }
  .wwd-detail.clickable:hover,
  .wwd-detail.clickable:focus-visible {
    background: var(--bg-inset);
    outline: none;
  }
  .wwd-detail.clickable:focus-visible {
    box-shadow: 0 0 0 1px var(--border-soft) inset;
  }
  .wwd-detail-chip {
    align-self: start;
    margin-top: 2px;
    justify-content: center;
    min-width: 80px;
    text-align: center;
    font-size: 10px;
    letter-spacing: 0.04em;
  }
  .wwd-detail-when {
    color: var(--fg-soft);
    font-size: 11px;
    margin-top: 3px;
    font-variant-numeric: tabular-nums;
  }
  .wwd-detail-text {
    color: var(--fg);
    font-size: 13px;
    line-height: 1.45;
    min-width: 0;
    word-break: break-word;
  }
  .wwd-detail-ev {
    display: flex;
    flex-direction: column;
    gap: 3px;
    align-items: flex-end;
    font-size: 10.5px;
    color: var(--fg-dim);
    max-width: 28ch;
  }
  .wwd-detail-ev :global(.mono) { font-variant-numeric: tabular-nums; }
</style>
