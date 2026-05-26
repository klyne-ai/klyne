<!--
  WhatWasDoneCard — typed per-service "What was done" card.

  Spec: docs/plan/2026-05-26-wwd-typed-cards.md §1.2 + §2 Agent U.

  Tier 1 (always visible): service name, deterministic tldr, pill_counts
  as small kind-colored chips, top_evidence as monospace short shas,
  turn/commit counts on the right.

  Tier 2 (behind a `▸ N entries` disclosure): every detail rendered as a
  clickable row → onOpenEntry(session_id) opens the underlying worklog
  entry. Each detail row carries a kind chip, the HH:MM `when`, the
  verb-led `text`, and the literal evidence tokens (citation invariant).

  Visual language matches the existing `.refl-card` block in
  routes/productivity/+page.svelte; styles use the shared
  `var(--fg)`, `var(--bg-card)`, etc. tokens — no raw hex.
-->
<script lang="ts">
  import type { WhatWasDoneCard, WWDDetail, WWDKind } from '$lib/api.js';

  interface Props {
    card: WhatWasDoneCard;
    /** Optional click handler — opens the originating worklog entry. */
    onOpenEntry?: (session_id: string) => void;
  }
  let { card, onOpenEntry }: Props = $props();

  let expanded = $state(false);

  const details: WWDDetail[] = $derived(card.tier2?.details ?? []);
  const entryCount: number = $derived(details.length);

  // pill_counts entries → ordered chips. The backend may use either
  // `decision`/`decisions` for the DECISION bucket; accept both so the
  // contract doesn't depend on which spelling Go ends up emitting (the
  // §1.2 example shows `decisions` — plural — but the §1.2 derivation
  // text describes "lowercased key" of the kind enum which would be
  // singular `decision`). Render whichever the backend sends.
  type PillRow = { key: string; label: string; count: number; tone: Tone };
  type Tone = 'ok' | 'warn' | 'alert' | 'info' | 'muted' | 'accent';

  function kindTone(kind: WWDKind | string): Tone {
    switch (kind) {
      case 'SHIPPED':      return 'ok';
      case 'MAJOR':        return 'info';
      case 'FIXED':        return 'info';
      case 'DECISION':     return 'accent';
      case 'INVESTIGATED': return 'muted';
      case 'IN_PROGRESS':  return 'warn';
      default:             return 'muted';
    }
  }

  // pill_counts may key as "decisions" (plural example in spec) or
  // "decision" (lowercased enum). Normalize for the chip row.
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
</article>

<style>
  /*
    Visual language mirrors the legacy `.refl-card` rules in
    routes/productivity/+page.svelte (~lines 1477-1536). Token reuse
    only — no raw hex. .pill / .pill-* / .mono / .dim live as
    scoped classes in the parent page; redeclare local copies here
    so the component is self-contained.
  */
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

  .mono { font-family: var(--font-mono); }
  .dim  { color: var(--fg-dim); }

  .wwd-card {
    border: 1px solid var(--border-soft);
    border-radius: 8px;
    background: var(--bg-card-2);
    padding: 12px 16px 14px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
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
  .wwd-pills {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .wwd-pill {
    font-size: 10.5px;
    letter-spacing: 0.02em;
    padding: 1px 7px;
  }
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
