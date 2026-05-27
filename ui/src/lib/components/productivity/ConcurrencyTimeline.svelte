<!--
  ConcurrencyTimeline — V4 Synthesis layout.

  Renders one slim 7px bar per session active-interval, arranged
  across 7 lanes (modulo session index) so overlapping sessions
  visually stack.

  Per-repo colors (2026-05-27 rewrite): assigned by sorted-position
  through an 8-slot palette so distinct repos in the same render
  never collide. Hash-based fallback only kicks in past the 8th
  service (rare).

  Legend chips are CLICKABLE — clicking a chip filters the canvas
  to just that service's intervals. Click the same chip again to
  deselect; click a second/third chip to AND-multi-select. The
  filtered-out bars dim to a low-contrast trace so the silhouette
  of the day stays visible, but only the selected repos are
  fully painted.

  Hovering a bar opens a small styled floating tooltip (instant —
  not the native ~1s `title`) showing the repo, duration, and the
  clock time range. The tooltip is portaled to <body> so it isn't
  trapped by the dashboard `.page` element's fadeUp animation
  containing block.
-->
<script lang="ts">
  import type { ProductivitySessionStat } from '$lib/api.js';
  import { portal } from '$lib/dashboard/portal';

  interface Props {
    sessions: ProductivitySessionStat[];
    since: number;
    until: number;
  }
  let { sessions, since, until }: Props = $props();

  // Per-repo color palette (2026-05-27 v2 rewrite): hand-picked OKLCH
  // values with hues spaced ≥40° apart on the color wheel so adjacent
  // services are PERCEPTUALLY distinct, not just nominally different.
  // The previous palette routed three services through warm tones in a
  // 20° hue band (--warn:75 / --accent:80 / --ad-claude:60), which read
  // as the same color to most users. These slots stay stable across
  // reloads because assignment is by sorted-repo index.
  //
  // Hues chosen (in render order):
  //   150 green · 230 blue · 290 purple · 30 red ·
  //    75 yellow · 330 magenta · 190 cyan · 15 coral
  const REPO_PALETTE = [
    'oklch(0.74 0.13 150)',  // green   — slot 0
    'oklch(0.75 0.13 230)',  // blue    — slot 1
    'oklch(0.78 0.14 290)',  // purple  — slot 2
    'oklch(0.70 0.16  30)',  // red     — slot 3
    'oklch(0.82 0.15  90)',  // yellow  — slot 4 (slightly more saturated than --warn)
    'oklch(0.74 0.16 330)',  // magenta — slot 5
    'oklch(0.75 0.12 190)',  // cyan    — slot 6
    'oklch(0.72 0.14  15)',  // coral   — slot 7
  ] as const;

  const palette = $derived.by(() => {
    const repos = new Set<string>();
    for (const s of sessions ?? []) {
      const r = (s.repo || '').toLowerCase();
      if (r) repos.add(r);
    }
    const sorted = Array.from(repos).sort();
    const map = new Map<string, string>();
    sorted.forEach((r, i) => {
      const slot = i < REPO_PALETTE.length
        ? REPO_PALETTE[i]
        // Past the 8-slot palette, hash to distribute the overflow.
        : REPO_PALETTE[hashSlot(r)];
      map.set(r, slot);
    });
    return map;
  });

  function hashSlot(s: string): number {
    let h = 5381;
    for (let i = 0; i < s.length; i++) h = (((h << 5) + h) + s.charCodeAt(i)) | 0;
    return Math.abs(h) % REPO_PALETTE.length;
  }
  function repoColor(repo: string): string {
    const r = (repo || '').toLowerCase();
    if (!r) return 'var(--fg-dim)';
    return palette.get(r) ?? 'var(--fg-dim)';
  }

  // ── Selection state ──────────────────────────────────────────────
  // Empty set = "no filter, show all repos at full opacity."
  // Non-empty set = "only these repos are highlighted; everything else
  // drops to a low-contrast trace."
  let selected = $state<Set<string>>(new Set());

  function toggleSelected(repo: string): void {
    const next = new Set(selected);
    const key = repo.toLowerCase();
    if (next.has(key)) next.delete(key);
    else next.add(key);
    selected = next;
  }
  function clearSelected(): void {
    if (selected.size > 0) selected = new Set();
  }
  function isSelected(repo: string): boolean {
    return selected.has((repo || '').toLowerCase());
  }
  // True when the filter is active AND the repo is NOT in the
  // selection — controls bar dimming.
  function isFiltered(repo: string): boolean {
    return selected.size > 0 && !isSelected(repo);
  }

  // Lane layout: 7 lanes, each 11px tall (6 + 5 gap), session-index modulo.
  const LANE_HEIGHT = 11;
  const LANE_COUNT = 7;
  const BAR_HEIGHT = 7;
  const TOP_PAD = 6;
  const canvasHeight = TOP_PAD * 2 + LANE_HEIGHT * LANE_COUNT;

  interface Bar {
    lane: number;
    startPct: number;
    widthPct: number;
    color: string;
    faded: boolean;
    repo: string;
    startMs: number;
    endMs: number;
    minutes: number;
  }

  // Flatten sessions × active_intervals into renderable bars.
  const bars = $derived.by<Bar[]>(() => {
    if (until <= since) return [];
    const span = until - since;
    const out: Bar[] = [];
    (sessions ?? []).forEach((s, idx) => {
      const lane = idx % LANE_COUNT;
      const c = repoColor(s.repo);
      for (const iv of s.active_intervals ?? []) {
        const a = Date.parse(iv.start), b = Date.parse(iv.end);
        if (Number.isNaN(a) || Number.isNaN(b) || b <= a) continue;
        const startPct = Math.max(0, ((a - since) / span) * 100);
        const widthPct = Math.max(0.4, ((b - a) / span) * 100);
        const minutes = Math.round((b - a) / 60000);
        out.push({
          lane, startPct, widthPct, color: c,
          faded: minutes <= 5,
          repo: s.repo || '—',
          startMs: a, endMs: b, minutes,
        });
      }
    });
    return out;
  });

  // Visible bar count under the active filter — drives the header
  // "N active intervals" so the user can see the filter taking effect.
  const visibleCount = $derived.by(() => {
    if (selected.size === 0) return bars.length;
    let n = 0;
    for (const b of bars) if (!isFiltered(b.repo)) n++;
    return n;
  });

  // Hour ticks across the window.
  const ticks = $derived.by(() => {
    if (until <= since) return [] as { pct: number; label: string }[];
    const span = until - since;
    const hours = span / (60 * 60 * 1000);
    const step = hours <= 12 ? 2 : hours <= 36 ? 4 : 6;
    const out: { pct: number; label: string }[] = [];
    const start = new Date(since); start.setMinutes(0, 0, 0);
    let t = start.getTime(); if (t < since) t += 60 * 60 * 1000;
    while (t <= until) {
      const hr = new Date(t).getHours();
      out.push({ pct: ((t - since) / span) * 100, label: hr.toString().padStart(2,'0') + ':00' });
      t += step * 60 * 60 * 1000;
    }
    return out;
  });

  // Distinct repos in render order — for the legend.
  const legend = $derived.by(() => {
    const seen = new Map<string, string>();
    for (const s of sessions ?? []) if (!seen.has(s.repo) && s.repo) seen.set(s.repo, repoColor(s.repo));
    return Array.from(seen.entries()).map(([repo, color]) => ({ repo, color }));
  });

  const rangeLabel = $derived.by(() => {
    if (until <= since) return '';
    const fmt = (ms: number) => {
      const d = new Date(ms);
      return `${d.getHours().toString().padStart(2,'0')}:${d.getMinutes().toString().padStart(2,'0')}`;
    };
    return `${fmt(since)} → ${fmt(until)}`;
  });

  // ── Floating tooltip ──────────────────────────────────────────────
  interface Tip {
    bar: Bar;
    x: number;
    y: number;
    placement: 'above' | 'below';
  }
  let tip = $state<Tip | null>(null);

  const TIP_HALF = 110;
  const TIP_ABOVE_NEED = 100;

  function showTip(b: Bar, e: MouseEvent): void {
    const el = e.currentTarget as HTMLElement | null;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const cx = rect.left + rect.width / 2;
    const margin = 8;
    const clampedX = Math.min(
      Math.max(cx, TIP_HALF + margin),
      window.innerWidth - TIP_HALF - margin
    );
    const placement: 'above' | 'below' = rect.top < TIP_ABOVE_NEED ? 'below' : 'above';
    tip = {
      bar: b,
      x: clampedX,
      y: placement === 'above' ? rect.top : rect.bottom,
      placement,
    };
  }
  function hideTip(): void {
    tip = null;
  }

  function formatClock(ms: number): string {
    const d = new Date(ms);
    return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
  }
  function formatDuration(min: number): string {
    if (min < 60) return `${min}m`;
    const h = Math.floor(min / 60);
    const m = min - h * 60;
    return m > 0 ? `${h}h ${m}m` : `${h}h`;
  }
</script>

<div class="ctl">
  <header class="ctl-h">
    <span class="ctl-meta mono">
      {#if selected.size > 0}
        {visibleCount} of {bars.length} active interval{bars.length === 1 ? '' : 's'}
        <span class="ctl-filter-hint">· filtered by {selected.size} service{selected.size === 1 ? '' : 's'}</span>
      {:else}
        {bars.length} active interval{bars.length === 1 ? '' : 's'}
      {/if}
    </span>
    <span class="ctl-range mono">{rangeLabel}</span>
  </header>
  <div class="ctl-canvas" style="height: {canvasHeight}px;">
    {#each ticks as t (t.pct)}
      <div class="ctl-tick" style="left: {t.pct}%;"></div>
    {/each}
    {#each bars as b, i (i)}
      {@const filtered = isFiltered(b.repo)}
      <div class="ctl-bar"
        class:ctl-bar-filtered={filtered}
        style="top: {TOP_PAD + b.lane * LANE_HEIGHT}px; height: {BAR_HEIGHT}px; left: {b.startPct}%; width: {b.widthPct}%; background: {b.color}; opacity: {filtered ? 0.18 : (b.faded ? 0.55 : 1)};"
        onmouseenter={(e) => showTip(b, e)}
        onmouseleave={hideTip}
        role="presentation">
      </div>
    {/each}
    {#if bars.length === 0}
      <div class="ctl-empty mono">No active sessions in this window.</div>
    {/if}
  </div>
  <div class="ctl-axis">
    {#each ticks as t (t.pct)}
      <span style="left: {t.pct}%;" class="mono">{t.label}</span>
    {/each}
  </div>
  {#if legend.length > 0}
    <div class="ctl-legend">
      {#each legend as l (l.repo)}
        {@const sel = isSelected(l.repo)}
        {@const dim = selected.size > 0 && !sel}
        <button
          type="button"
          class="ctl-legend-item"
          class:ctl-legend-selected={sel}
          class:ctl-legend-dim={dim}
          onclick={() => toggleSelected(l.repo)}
          title={sel ? `Click to deselect ${l.repo}` : `Click to show only ${l.repo}` + (selected.size > 0 ? ` (and selected)` : '')}
        >
          <span class="ctl-swatch" style="background: {l.color};"></span>
          <span class="mono">{l.repo}</span>
        </button>
      {/each}
      {#if selected.size > 0}
        <button
          type="button"
          class="ctl-legend-clear mono"
          onclick={clearSelected}
          title="Clear filter — show all services"
        >
          show all
        </button>
      {/if}
    </div>
  {/if}
</div>

{#if tip}
  <div use:portal class="ctl-tip-host">
    <div
      class="ctl-tip ctl-tip--{tip.placement}"
      style:left="{tip.x}px"
      style:top="{tip.y}px"
      role="tooltip"
    >
      <div class="ctl-tip-hd">
        <span class="ctl-tip-swatch" style:background={tip.bar.color}></span>
        <span class="ctl-tip-repo mono">{tip.bar.repo}</span>
      </div>
      <dl class="ctl-tip-rows">
        <div class="ctl-tip-row">
          <dt>duration</dt>
          <dd class="mono">{formatDuration(tip.bar.minutes)}</dd>
        </div>
        <div class="ctl-tip-row">
          <dd class="mono ctl-tip-time">{formatClock(tip.bar.startMs)} → {formatClock(tip.bar.endMs)}</dd>
        </div>
      </dl>
    </div>
  </div>
{/if}

<style>
  .ctl { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  .ctl-h { display: flex; align-items: baseline; justify-content: space-between; }
  .ctl-h .mono, .ctl-meta, .ctl-range { font-family: var(--font-mono); font-variant-numeric: tabular-nums; }
  .ctl-meta  { font-size: 11.5px; color: var(--fg-soft); }
  .ctl-range { font-size: 11px; color: var(--fg-dim); }
  .ctl-filter-hint { color: var(--accent); margin-left: 4px; }
  .ctl-canvas {
    position: relative;
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 6px;
  }
  .ctl-tick { position: absolute; top: 0; bottom: 0; width: 1px; background: var(--border-hair); }
  .ctl-bar {
    position: absolute;
    border-radius: 2px;
    cursor: pointer;
    transition: filter 120ms ease, transform 120ms ease, opacity 160ms ease;
  }
  .ctl-bar:hover {
    filter: brightness(1.18);
    transform: scaleY(1.18);
  }
  .ctl-bar-filtered { cursor: default; }
  .ctl-bar-filtered:hover {
    /* When filtered out the bar shouldn't grow on hover — it's
       deliberately dimmed for context only. */
    transform: none;
    filter: none;
  }
  .ctl-empty { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--fg-muted); font-size: 12px; }
  .ctl-axis  { position: relative; height: 14px; }
  .ctl-axis span { position: absolute; top: 0; transform: translateX(-50%); font-size: 10px; color: var(--fg-dim); }

  /* ── Legend (clickable filter chips) ─────────────────────────── */
  .ctl-legend { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .ctl-legend-item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: transparent;
    border: 1px solid var(--border-hair);
    border-radius: 999px;
    padding: 3px 9px 3px 7px;
    cursor: pointer;
    transition: background var(--t-fast), border-color var(--t-fast), opacity var(--t-fast);
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 10.5px;
  }
  .ctl-legend-item:hover {
    background: var(--bg-card);
    border-color: var(--border-soft);
    color: var(--fg-soft);
  }
  .ctl-legend-item:focus-visible {
    outline: 1px solid var(--accent);
    outline-offset: 1px;
  }
  .ctl-legend-selected {
    background: var(--bg-card);
    border-color: var(--accent);
    color: var(--fg);
  }
  .ctl-legend-dim {
    opacity: 0.45;
  }
  .ctl-legend-item .mono { font-size: 10.5px; color: inherit; }
  .ctl-swatch {
    width: 10px;
    height: 4px;
    background: var(--fg-dim);
    border-radius: 1px;
    display: inline-block;
  }
  .ctl-legend-clear {
    background: transparent;
    border: 1px dashed var(--border-soft);
    color: var(--fg-muted);
    border-radius: 999px;
    padding: 3px 10px;
    font-size: 10px;
    cursor: pointer;
    letter-spacing: 0.04em;
    text-transform: lowercase;
  }
  .ctl-legend-clear:hover {
    color: var(--fg);
    border-color: var(--fg-muted);
  }

  /* ── Tooltip ──────────────────────────────────────────────────── */
  .ctl-tip-host { display: contents; }
  .ctl-tip {
    position: fixed;
    transform: translate(-50%, calc(-100% - 10px));
    background: var(--bg-card);
    border: 1px solid var(--border-soft);
    border-radius: 8px;
    padding: 10px 12px;
    min-width: 180px;
    max-width: 240px;
    box-shadow: 0 14px 36px -12px rgba(0, 0, 0, 0.55);
    z-index: 80;
    pointer-events: none;
    animation: ctl-tip-in 90ms ease-out both;
  }
  .ctl-tip--below { transform: translate(-50%, 10px); }
  @keyframes ctl-tip-in {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  .ctl-tip-hd { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
  .ctl-tip-swatch { width: 10px; height: 10px; border-radius: 3px; flex-shrink: 0; }
  .ctl-tip-repo { font-size: 13px; color: var(--fg); }
  .ctl-tip-rows { margin: 0; display: flex; flex-direction: column; gap: 2px; }
  .ctl-tip-row { display: flex; align-items: baseline; gap: 6px; font-size: 11.5px; color: var(--fg-soft); }
  .ctl-tip-row dt { color: var(--fg-muted); text-transform: uppercase; letter-spacing: 0.04em; font-size: 10px; }
  .ctl-tip-row dd { margin: 0; color: var(--fg); }
  .ctl-tip-time { color: var(--fg-muted); font-size: 11px; }
</style>
