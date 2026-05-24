<!--
  ConcurrencyTimeline — V4 Synthesis layout.

  Renders one slim 7px bar per session active-interval, arranged
  across 7 lanes (modulo session index) so overlapping sessions
  visually stack. Repo color follows the V4 mapping:
    operations-app → ok (green)
    klyne          → info (blue)
    oms-service    → warn (amber)
    doc            → accent (gold)
    other          → fg-dim
  Hour ticks render as faint vertical dividers + axis labels.

  Hovering a bar opens a small styled floating tooltip (instant —
  not the native ~1s `title`) showing the repo, duration, and the
  clock time range. The tooltip is portaled to <body> so it isn't
  trapped by the dashboard `.page` element's fadeUp animation
  containing block (same root cause as the modal portal fix).
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

  // Stable known-name colors — matches the V4 mock's legend.
  function repoColor(repo: string): string {
    const r = (repo || '').toLowerCase();
    if (r.includes('operations') || r.includes('app'))     return 'var(--ok)';
    if (r === 'klyne')                                      return 'var(--info)';
    if (r.includes('oms')   || r.includes('order'))         return 'var(--warn)';
    if (r.includes('doc')   || r.includes('docs'))          return 'var(--accent)';
    return 'var(--fg-dim)';
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
  // Anchored to the hovered bar via fixed positioning + boundingClientRect.
  // Portaled to <body> so position:fixed resolves to the viewport rather
  // than .page (which has `animation: fadeUp … both` whose end-frame
  // transform creates a containing block — same root cause as the
  // ConfirmDeleteProjectModal / ThreadPeekModal anchor bug).
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
    <span class="ctl-meta mono">{bars.length} active interval{bars.length === 1 ? '' : 's'}</span>
    <span class="ctl-range mono">{rangeLabel}</span>
  </header>
  <div class="ctl-canvas" style="height: {canvasHeight}px;">
    {#each ticks as t (t.pct)}
      <div class="ctl-tick" style="left: {t.pct}%;"></div>
    {/each}
    {#each bars as b, i (i)}
      <div class="ctl-bar"
        style="top: {TOP_PAD + b.lane * LANE_HEIGHT}px; height: {BAR_HEIGHT}px; left: {b.startPct}%; width: {b.widthPct}%; background: {b.color}; opacity: {b.faded ? 0.55 : 1};"
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
        <span class="ctl-legend-item"><span class="ctl-swatch" style="background: {l.color};"></span><span class="mono">{l.repo}</span></span>
      {/each}
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
    transition: filter 120ms ease, transform 120ms ease;
  }
  .ctl-bar:hover {
    filter: brightness(1.18);
    transform: scaleY(1.18);
  }
  .ctl-empty { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--fg-muted); font-size: 12px; }
  .ctl-axis  { position: relative; height: 14px; }
  .ctl-axis span { position: absolute; top: 0; transform: translateX(-50%); font-size: 10px; color: var(--fg-dim); }
  .ctl-legend { display: flex; flex-wrap: wrap; gap: 12px; }
  .ctl-legend-item { display: inline-flex; align-items: center; gap: 6px; }
  .ctl-legend-item .mono { font-size: 10.5px; color: var(--fg-muted); }
  .ctl-swatch { width: 10px; height: 4px; background: var(--fg-dim); border-radius: 1px; display: inline-block; }

  /* ── Tooltip ──────────────────────────────────────────────────── */
  .ctl-tip-host {
    /* display: contents keeps this wrapper transparent to layout so the
       portaled tooltip renders at body level with position: fixed. */
    display: contents;
  }
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
  .ctl-tip--below {
    transform: translate(-50%, 10px);
  }
  @keyframes ctl-tip-in {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  .ctl-tip-hd {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
  }
  .ctl-tip-swatch {
    width: 10px;
    height: 10px;
    border-radius: 3px;
    flex-shrink: 0;
  }
  .ctl-tip-repo {
    font-size: 13px;
    color: var(--fg);
  }
  .ctl-tip-rows {
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .ctl-tip-row {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 11.5px;
    color: var(--fg-soft);
  }
  .ctl-tip-row dt {
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-size: 10px;
  }
  .ctl-tip-row dd {
    margin: 0;
    color: var(--fg);
  }
  .ctl-tip-time {
    color: var(--fg-muted);
    font-size: 11px;
  }
</style>
