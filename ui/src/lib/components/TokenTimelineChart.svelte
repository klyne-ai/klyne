<!--
  TokenTimelineChart — per-session token-usage line chart for the
  session-detail page.

  Plots the per-assistant-turn TotalInput series (the prefix size at
  each turn) over the session's lifetime, with a secondary curve for
  EffectiveInput (the uncached portion that bills against the 5h
  rate-limit at full rate). Same data shape the `klyne tokens` CLI
  and the `get_token_timeline` MCP tool already emit — fetched from
  the matching REST endpoint so all three surfaces stay aligned.

  Why hand-rolled SVG instead of a chart library:
    - A typical session emits ~50–500 points; a small SVG renderer
      is more than enough and adds zero dependency weight.
    - Matches the inline-style convention already used across the
      sibling components (TokenSavings, AdvisorModal, UsageBadge).
    - Keeps the bundle small for a daemon UI that ships alongside the
      Go binary.

  States rendered:
    - loading                — neutral placeholder while the fetch runs.
    - empty (zero points)    — "no assistant turns yet" hint.
    - normal                 — chart + axis labels + headline numbers.
    - error                  — one-line error message; no chart.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchTokenTimeline } from '$lib/api';
  import { kfmt } from '$lib/format';
  import type { TokenTimelinePoint, TokenTimelineResponse } from '$lib/types';

  interface Props {
    /** Session whose timeline to plot. The component refetches when
     *  this prop changes — driven by an $effect on the value. */
    sessionId: string;
  }

  const { sessionId }: Props = $props();

  let data = $state<TokenTimelineResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  /** Chart geometry — fixed to a single viewBox so the SVG scales
   *  cleanly into whatever width its parent gives it. The 4:1 aspect
   *  ratio feels right for a "session over time" curve without
   *  dominating the page. */
  const VIEW_W = 800;
  const VIEW_H = 220;
  const PAD_LEFT = 56;
  const PAD_RIGHT = 16;
  const PAD_TOP = 18;
  const PAD_BOTTOM = 28;

  async function load(id: string): Promise<void> {
    if (!id) return;
    loading = true;
    error = null;
    try {
      data = await fetchTokenTimeline(id);
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : 'failed to load token timeline';
      data = null;
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load(sessionId);
  });

  $effect(() => {
    const id = sessionId;
    if (id) void load(id);
  });

  // ---------------------------------------------------------------------------
  // Derived chart-rendering state
  // ---------------------------------------------------------------------------

  /** True when there's at least one point to plot. */
  const hasPoints = $derived((data?.points?.length ?? 0) > 0);

  /** maxY chooses the y-axis ceiling. We prefer the model's context
   *  window when known so the chart visualises "how full the prefix
   *  is" in absolute terms; falling back to the peak prefix size lets
   *  short sessions still produce a readable curve. Both branches
   *  multiply by 1.05 to leave headroom above the highest point. */
  const maxY = $derived.by(() => {
    if (!data || !hasPoints) return 1;
    const peak = data.peak_input;
    const ctx = data.context_window;
    if (ctx > 0 && ctx >= peak) {
      return ctx;
    }
    return Math.max(1, Math.round(peak * 1.05));
  });

  /** axisTicks places 4 evenly-spaced gridlines on the y-axis so the
   *  reader can eyeball "how many K tokens" each height corresponds
   *  to without hovering for a tooltip. */
  const axisTicks = $derived.by(() => {
    const max = maxY;
    return [0, max * 0.25, max * 0.5, max * 0.75, max].map((v) => ({
      value: Math.round(v),
      y: scaleY(v)
    }));
  });

  function scaleX(idx: number, count: number): number {
    if (count <= 1) {
      return PAD_LEFT;
    }
    const usable = VIEW_W - PAD_LEFT - PAD_RIGHT;
    return PAD_LEFT + (idx / (count - 1)) * usable;
  }

  function scaleY(value: number): number {
    const usable = VIEW_H - PAD_TOP - PAD_BOTTOM;
    const clamped = Math.max(0, Math.min(value, maxY));
    return PAD_TOP + (1 - clamped / maxY) * usable;
  }

  /** Build an SVG path d-string for the given point accessor. Skipping
   *  zero-token rows would create spurious gaps — the timeline already
   *  filters out non-qualifying turns server-side, so every point in
   *  the array is real. */
  function buildPath(points: TokenTimelinePoint[], pick: (p: TokenTimelinePoint) => number): string {
    if (points.length === 0) return '';
    const segments: string[] = [];
    points.forEach((p, i) => {
      const x = scaleX(i, points.length).toFixed(2);
      const y = scaleY(pick(p)).toFixed(2);
      segments.push(`${i === 0 ? 'M' : 'L'} ${x} ${y}`);
    });
    return segments.join(' ');
  }

  /** buildAreaPath produces the same curve as buildPath but closed
   *  back to y=baseY along the x extent — used to paint the soft
   *  fill under the primary TotalInput line. */
  function buildAreaPath(points: TokenTimelinePoint[], pick: (p: TokenTimelinePoint) => number): string {
    if (points.length === 0) return '';
    const top = buildPath(points, pick);
    const baseY = scaleY(0).toFixed(2);
    const lastX = scaleX(points.length - 1, points.length).toFixed(2);
    const firstX = scaleX(0, points.length).toFixed(2);
    return `${top} L ${lastX} ${baseY} L ${firstX} ${baseY} Z`;
  }

  const totalPath = $derived(buildPath(data?.points ?? [], (p) => p.total_input));
  const totalArea = $derived(buildAreaPath(data?.points ?? [], (p) => p.total_input));
  const effPath = $derived(buildPath(data?.points ?? [], (p) => p.effective_input));

  /** formatTime renders an epoch-ms as "HH:MM" in the user's locale.
   *  Picking the locale's local time keeps the axis labels readable
   *  ("13:24" instead of "2026-05-11T07:54:00Z"). */
  function formatTime(ms: number): string {
    if (!ms) return '';
    try {
      return new Date(ms).toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit'
      });
    } catch {
      return '';
    }
  }

  /** spanLabel describes the chart's time extent in human terms —
   *  "5h", "47m", "12:04 → 18:31" — so the user knows whether they
   *  are looking at a focused recent window or the full session. */
  const spanLabel = $derived.by(() => {
    if (!data || !hasPoints) return '';
    const start = formatTime(data.window_start_ms);
    const end = formatTime(data.window_end_ms);
    if (!start || !end) return '';
    return `${start} → ${end}`;
  });

  /** Highlighted point — driven by mouse hover over the chart. Holds
   *  the array index so the chart can render a marker + a labelled
   *  tooltip-style readout for the closest turn. */
  let hoverIdx = $state<number | null>(null);

  function onMove(ev: MouseEvent): void {
    if (!data || !hasPoints) return;
    const target = ev.currentTarget as SVGSVGElement | null;
    if (!target) return;
    const rect = target.getBoundingClientRect();
    // Map the cursor's x position into SVG-space using the viewBox
    // width. This stays accurate regardless of how the SVG has been
    // scaled by CSS — we never assume rect.width === VIEW_W.
    const ratio = (ev.clientX - rect.left) / rect.width;
    const xInView = ratio * VIEW_W;
    const usable = VIEW_W - PAD_LEFT - PAD_RIGHT;
    const localX = Math.max(0, Math.min(usable, xInView - PAD_LEFT));
    const count = data.points.length;
    if (count <= 1) {
      hoverIdx = 0;
      return;
    }
    const idx = Math.round((localX / usable) * (count - 1));
    hoverIdx = Math.max(0, Math.min(count - 1, idx));
  }

  function onLeave(): void {
    hoverIdx = null;
  }

  /** hoveredPoint is the TimelinePoint currently under the cursor,
   *  or the latest point as a sensible default so the readout is
   *  populated even before the user moves the mouse. */
  const hoveredPoint = $derived.by(() => {
    if (!data || !hasPoints) return null;
    const idx = hoverIdx ?? data.points.length - 1;
    return data.points[idx] ?? null;
  });

  /** hoveredX is the x-coordinate of the marker dot in SVG space. */
  const hoveredX = $derived.by(() => {
    if (!data || !hasPoints) return 0;
    const idx = hoverIdx ?? data.points.length - 1;
    return scaleX(idx, data.points.length);
  });

  const hoveredY = $derived.by(() => {
    if (!hoveredPoint) return 0;
    return scaleY(hoveredPoint.total_input);
  });
</script>

<section
  class="ad-card"
  aria-label="Token usage timeline"
  style="padding: 16px 18px; margin-bottom: 16px;"
>
  <header style="display: flex; align-items: baseline; justify-content: space-between; gap: 12px; margin-bottom: 10px; flex-wrap: wrap;">
    <div>
      <div style="font-size: 13px; font-weight: 600; letter-spacing: -0.01em;">
        Token usage over time
      </div>
      <div class="ad-muted" style="font-size: 11px; margin-top: 2px;">
        Per-assistant-turn prefix size · uncached portion shown as a thinner overlay
      </div>
    </div>
    {#if data && hasPoints}
      <div class="ad-mono ad-tnum" style="font-size: 11px; color: var(--ad-muted);">
        {spanLabel} · {data.points.length} turns
      </div>
    {/if}
  </header>

  {#if loading}
    <div class="ad-muted" style="padding: 24px 0; font-size: 12px;">Loading token timeline…</div>
  {:else if error}
    <div style="color: var(--ad-error, #ef4444); font-size: 12px; padding: 8px 10px; background: rgba(239,68,68,0.08); border-radius: 6px;">
      {error}
    </div>
  {:else if !data || !hasPoints}
    <div
      class="ad-muted"
      style="padding: 24px 0; font-size: 12px; text-align: center;"
    >
      No assistant turns recorded yet — once Claude or Codex replies, the curve will appear here.
    </div>
  {:else}
    <!-- Headline numbers above the chart so the reader gets the answer
         even before they look at the curve. Mirrors the trajectory
         sentence the CLI surface prints. -->
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px 16px; margin-bottom: 10px;">
      <div>
        <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">First</div>
        <div class="ad-mono ad-tnum" style="font-size: 14px;">{kfmt(data.first_input)}</div>
      </div>
      <div>
        <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">Peak</div>
        <div class="ad-mono ad-tnum" style="font-size: 14px;">{kfmt(data.peak_input)}</div>
      </div>
      <div>
        <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">Latest</div>
        <div class="ad-mono ad-tnum" style="font-size: 14px;">{kfmt(data.latest_input)}</div>
      </div>
      {#if data.context_window > 0}
        <div>
          <div style="font-size: 10px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">% of context</div>
          <div class="ad-mono ad-tnum" style="font-size: 14px;">
            {data.pct_of_context < 1 && data.pct_of_context > 0 ? '<1%' : `${data.pct_of_context.toFixed(0)}%`}
          </div>
        </div>
      {/if}
    </div>

    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <svg
      role="img"
      aria-label="Line chart of input token count per assistant turn"
      viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
      preserveAspectRatio="none"
      style="width: 100%; height: auto; display: block; cursor: crosshair;"
      onmousemove={onMove}
      onmouseleave={onLeave}
    >
      <!-- Gridlines -->
      {#each axisTicks as tick (tick.value)}
        <line
          x1={PAD_LEFT}
          x2={VIEW_W - PAD_RIGHT}
          y1={tick.y}
          y2={tick.y}
          stroke="var(--ad-border-soft, #2a2a2a)"
          stroke-width="1"
          stroke-dasharray="2 4"
        />
        <text
          x={PAD_LEFT - 6}
          y={tick.y}
          text-anchor="end"
          dominant-baseline="middle"
          font-size="10"
          font-family="var(--ad-font-mono)"
          fill="var(--ad-faint, #888)"
        >
          {kfmt(tick.value)}
        </text>
      {/each}

      <!-- Area fill under the primary curve. Renders behind both
           line paths so the line strokes stay crisp. -->
      <path d={totalArea} fill="var(--ad-claude, #c084fc)" fill-opacity="0.15" />

      <!-- Effective (uncached) input — secondary curve. Drawn first
           so the primary curve sits on top. -->
      <path
        d={effPath}
        fill="none"
        stroke="var(--ad-muted, #888)"
        stroke-width="1.25"
        stroke-dasharray="3 3"
      />

      <!-- Total input — primary curve. -->
      <path
        d={totalPath}
        fill="none"
        stroke="var(--ad-claude, #c084fc)"
        stroke-width="2"
        stroke-linejoin="round"
        stroke-linecap="round"
      />

      <!-- Hover marker + crosshair. Only rendered when there are
           points; positioned at the latest point by default. -->
      {#if hoveredPoint}
        <line
          x1={hoveredX}
          x2={hoveredX}
          y1={PAD_TOP}
          y2={VIEW_H - PAD_BOTTOM}
          stroke="var(--ad-fg-2, #aaa)"
          stroke-width="1"
          stroke-dasharray="1 3"
          opacity="0.6"
        />
        <circle
          cx={hoveredX}
          cy={hoveredY}
          r="3.5"
          fill="var(--ad-claude, #c084fc)"
          stroke="var(--ad-bg, #111)"
          stroke-width="1.5"
        />
      {/if}

      <!-- X-axis time labels. Render the first and last point's
           local time so the reader can anchor the curve in clock
           time without a full tick array. -->
      <text
        x={PAD_LEFT}
        y={VIEW_H - 8}
        font-size="10"
        font-family="var(--ad-font-mono)"
        fill="var(--ad-faint, #888)"
      >
        {formatTime(data.window_start_ms)}
      </text>
      <text
        x={VIEW_W - PAD_RIGHT}
        y={VIEW_H - 8}
        text-anchor="end"
        font-size="10"
        font-family="var(--ad-font-mono)"
        fill="var(--ad-faint, #888)"
      >
        {formatTime(data.window_end_ms)}
      </text>
    </svg>

    <!-- Hover readout. Always-on (defaults to the latest point) so
         the reader sees the exact numbers without needing to mouse
         over the SVG. -->
    {#if hoveredPoint}
      <div
        style="display: flex; flex-wrap: wrap; gap: 12px; font-size: 11px; color: var(--ad-fg-2); margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--ad-border-soft, #2a2a2a);"
      >
        <span class="ad-mono">{formatTime(hoveredPoint.ts_ms)}</span>
        <span>
          input <span class="ad-mono ad-tnum" style="color: var(--ad-claude, #c084fc); font-weight: 600;">{kfmt(hoveredPoint.total_input)}</span>
        </span>
        <span>
          uncached <span class="ad-mono ad-tnum">{kfmt(hoveredPoint.effective_input)}</span>
        </span>
        <span>
          cached <span class="ad-mono ad-tnum">{kfmt(hoveredPoint.cached_read_tokens)}</span>
        </span>
        <span>
          ↓ <span class="ad-mono ad-tnum">{kfmt(hoveredPoint.output_tokens)}</span>
        </span>
      </div>
    {/if}

    <!-- Legend. Tiny, low-contrast — the chart is self-explanatory
         once the colours have been labelled once. -->
    <div style="display: flex; gap: 14px; font-size: 11px; color: var(--ad-muted); margin-top: 6px;">
      <span style="display: inline-flex; align-items: center; gap: 6px;">
        <span style="display: inline-block; width: 14px; height: 2px; background: var(--ad-claude, #c084fc);"></span>
        input tokens (prefix size)
      </span>
      <span style="display: inline-flex; align-items: center; gap: 6px;">
        <span style="display: inline-block; width: 14px; height: 1px; border-top: 1.25px dashed var(--ad-muted, #888);"></span>
        uncached portion
      </span>
    </div>
  {/if}
</section>
