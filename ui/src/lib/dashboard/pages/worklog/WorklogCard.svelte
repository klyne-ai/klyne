<!--
  WorklogCard — per-project reflection rollup card.
  Matches page-worklog.jsx WorklogCard design:
    - 3px left rail in state colour
    - Header: name + state pill + ago + path
    - Latest reflection panel (if any): date, evidence, bullets, shipped, open loops
    - Stale/cold only: RunStrip (copy + run/stop + live elapsed counter)
    - All cards: "See full →" drill-in link
-->
<script lang="ts">
  import type { WorklogProjectRollup, Reflection } from '$lib/types.js';
  import { relTime } from '$lib/format.js';
  import RunStrip from './RunStrip.svelte';
  import KebabMenu from '$lib/dashboard/KebabMenu.svelte';
  import ConfirmDeleteProjectModal from './ConfirmDeleteProjectModal.svelte';

  interface Props {
    w: WorklogProjectRollup;
    // Called after a successful reflect or project-data delete so the parent
    // can refresh the rollup list.
    onChanged?: () => void;
  }

  const { w, onChanged }: Props = $props();

  let confirmDeleteOpen = $state(false);

  // --- State colour ---
  const tone = $derived(
    w.stale && w.latest_reflection ? 'var(--warn)' :
    !w.latest_reflection            ? 'var(--fg-muted)' :
                                      'var(--ok)'
  );

  // cardState derives a simpler label from the rollup shape
  const cardState = $derived<'stale' | 'cold' | 'fresh'>(
    w.stale && w.latest_reflection ? 'stale' :
    !w.latest_reflection            ? 'cold' :
                                      'fresh'
  );

  // --- State badge text ---
  const stateLabel = $derived.by(() => {
    if (cardState === 'stale') return `${w.pending_entries ?? 0} new entries since reflection`;
    if (cardState === 'cold')  return `${w.pending_entries ?? 0} entr${(w.pending_entries ?? 0) === 1 ? 'y' : 'ies'} · no reflection yet`;
    return 'fresh';
  });

  // --- Reflect command ---
  function reflectCmd(path: string): string {
    return `cd "${path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'`;
  }
  const cmd = $derived(reflectCmd(w.project_path));

  // --- Reflection helpers ---
  function reflectionAge(r: Reflection | null): string {
    if (!r) return 'never reflected';
    return `reflected ${relTime(r.ts)}`;
  }

  // Parse bullets from body_md: split on newline, filter lines starting with "- " or "* "
  function parseBullets(r: Reflection): string[] {
    return r.body_md
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l.startsWith('- ') || l.startsWith('* '))
      .map((l) => l.slice(2).trim());
  }

  // Parse shipped/open-loops from body_md: look for headings then following bullets
  function parseSection(r: Reflection, heading: string): string[] {
    const lines = r.body_md.split('\n').map((l) => l.trim());
    const idx = lines.findIndex((l) =>
      l.toLowerCase().includes(heading.toLowerCase()) && (l.startsWith('#') || l.startsWith('**'))
    );
    if (idx === -1) return [];
    const items: string[] = [];
    for (let i = idx + 1; i < lines.length; i++) {
      const l = lines[i];
      if (!l) continue;
      if (l.startsWith('#') || (l.startsWith('**') && !l.startsWith('- '))) break;
      if (l.startsWith('- ') || l.startsWith('* ')) items.push(l.slice(2).trim());
    }
    return items;
  }

  function reflectionDate(r: Reflection): string {
    return new Date(r.ts).toISOString().slice(0, 10);
  }

  function evidenceLabel(r: Reflection): string {
    const n = r.evidence_entry_ids.length;
    return `${n} evidence · tier ${r.tier} · ${r.summary_source}`;
  }
</script>

<div class="wcard" style="border-left-color: {tone}">
  <!-- Header -->
  <div class="wcard-head" class:has-body={!!w.latest_reflection}>
    <div class="wcard-head-left">
      <div class="name-row">
        <h3 class="proj-name">{w.name}</h3>
        {#if cardState === 'stale'}
          <span class="pill pill-warn">{stateLabel}</span>
        {:else if cardState === 'cold'}
          <span class="pill">{stateLabel}</span>
        {:else}
          <span class="pill pill-ok">fresh</span>
        {/if}
      </div>
      <div class="meta mono dim">
        {reflectionAge(w.latest_reflection)}
        {#if w.latest_entry_ts > 0} · last activity {relTime(w.latest_entry_ts)}{/if}
      </div>
      <div class="path mono">{w.project_path}</div>
    </div>
    <div class="wcard-head-right">
      <KebabMenu label={`Actions for ${w.name}`}>
        {#snippet children({ close })}
          <button
            type="button"
            role="menuitem"
            class="km-item km-item--danger"
            onclick={() => { confirmDeleteOpen = true; close(); }}
          >
            Delete project data…
          </button>
        {/snippet}
      </KebabMenu>
    </div>
  </div>

  <!-- Latest reflection body -->
  {#if w.latest_reflection}
    {@const r = w.latest_reflection}
    {@const bullets = parseBullets(r)}
    {@const shipped = parseSection(r, 'shipped')}
    {@const openLoops = parseSection(r, 'open loop')}
    <div class="reflection-body">
      <div class="ref-title-row">
        <h4 class="ref-title">Daily reflection · <span class="mono">{reflectionDate(r)}</span></h4>
        <span class="mono dim evidence">{evidenceLabel(r)}</span>
      </div>
      <div class="ref-card">
        {#if bullets.length > 0}
          <ul class="bullets">
            {#each bullets as b}
              <li>{b}</li>
            {/each}
          </ul>
        {:else}
          <!-- Fallback: show raw body_md lines as plain text when no bullets parsed -->
          <p class="body-raw">{r.body_md}</p>
        {/if}

        {#if shipped.length > 0 || openLoops.length > 0}
          <div class="sections">
            {#if shipped.length > 0}
              <div class="section">
                <div class="kicker ok-kicker">Shipped</div>
                {#each shipped as s}
                  <div class="mono section-line">· {s}</div>
                {/each}
              </div>
            {/if}
            {#if openLoops.length > 0}
              <div class="section">
                <div class="kicker warn-kicker">Open loops</div>
                {#each openLoops as s}
                  <div class="mono section-line">· {s}</div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Refresh strip (stale/cold only) -->
  {#if cardState !== 'fresh'}
    <div class="refresh-strip" class:has-top={!!w.latest_reflection}>
      <div class="kicker strip-kicker">
        {cardState === 'cold' ? 'Generate the first reflection:' : 'Refresh with the latest entries:'}
      </div>
      <RunStrip name={w.name} path={w.project_path} refreshCmd={cmd} {onChanged} />
    </div>
  {/if}

  <!-- See full link — always visible -->
  <div class="card-foot">
    <a class="see-full" href="/worklog/project?path={encodeURIComponent(w.project_path)}">
      See full →
    </a>
  </div>
</div>

{#if confirmDeleteOpen}
  <ConfirmDeleteProjectModal
    projectName={w.name}
    projectPath={w.project_path}
    onClose={() => { confirmDeleteOpen = false; }}
    onDeleted={() => { onChanged?.(); }}
  />
{/if}

<style>
  .wcard {
    background: var(--bg-card-2, var(--bg-card));
    border: 1px solid var(--border-hair);
    border-left-width: 3px;
    border-radius: 10px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  /* ── Header ── */
  .wcard-head {
    padding: 14px 18px;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }
  .wcard-head.has-body {
    border-bottom: 1px solid var(--border-hair);
  }
  .wcard-head-left { min-width: 0; flex: 1; }
  .wcard-head-right {
    flex-shrink: 0;
    margin-top: -4px;
    margin-right: -4px;
  }

  .name-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 4px;
    flex-wrap: wrap;
  }
  .proj-name {
    margin: 0;
    font-size: 16px;
    font-weight: 500;
    color: var(--fg);
    letter-spacing: -0.01em;
  }

  /* Pills */
  .pill {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    background: var(--bg-inset);
    color: var(--fg-muted);
    border: 1px solid var(--border-hair);
  }
  .pill-warn {
    background: color-mix(in oklch, var(--warn) 12%, var(--bg-card-2));
    color: var(--warn);
    border-color: color-mix(in oklch, var(--warn) 30%, transparent);
  }
  .pill-ok {
    background: color-mix(in oklch, var(--ok) 12%, var(--bg-card-2));
    color: var(--ok);
    border-color: color-mix(in oklch, var(--ok) 30%, transparent);
  }

  .meta {
    font-size: 11px;
    margin-bottom: 2px;
  }
  .path {
    font-size: 11px;
    color: var(--fg-muted);
    margin-top: 4px;
    word-break: break-all;
  }

  /* ── Reflection body ── */
  .reflection-body {
    padding: 16px 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .ref-title-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
  }
  .ref-title {
    margin: 0;
    font-size: 13.5px;
    font-weight: 500;
    color: var(--fg);
  }
  .evidence {
    font-size: 10.5px;
  }

  .ref-card {
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    padding: 14px 16px;
    font-size: 12.5px;
    color: var(--fg-soft, var(--fg-muted));
    line-height: 1.6;
  }

  .bullets {
    margin: 0;
    padding-left: 18px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .bullets li { margin-bottom: 2px; }

  .body-raw {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
  }

  .sections {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px dashed var(--border-soft, var(--border-hair));
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .section { display: flex; flex-direction: column; gap: 4px; }
  .kicker {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    margin-bottom: 2px;
  }
  .ok-kicker   { color: var(--ok); }
  .warn-kicker { color: var(--warn); }
  .section-line {
    font-size: 11.5px;
    color: var(--fg-soft, var(--fg-muted));
    line-height: 1.55;
  }

  /* ── Refresh strip ── */
  .refresh-strip {
    padding: 12px 18px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .refresh-strip.has-top {
    border-top: 1px dashed var(--border-soft, var(--border-hair));
  }
  .strip-kicker {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  /* ── Card foot ── */
  .card-foot {
    padding: 8px 18px 12px;
    display: flex;
    justify-content: flex-end;
  }
  .see-full {
    font-size: 12px;
    color: var(--fg-muted);
    text-decoration: none;
    opacity: 0.75;
    transition: opacity 0.15s;
  }
  .see-full:hover { opacity: 1; text-decoration: underline; }

  /* ── Shared ── */
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .dim  { color: var(--fg-dim, var(--fg-muted)); opacity: 0.75; }
</style>
