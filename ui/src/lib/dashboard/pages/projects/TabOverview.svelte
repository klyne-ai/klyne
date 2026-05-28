<!--
  TabOverview — Overview tab for a single project.
  Shows 4 stat tiles, a daily activity chart (from ProjectInsight.daily),
  a recent-sessions list (top 3), and an AgentMixDonut from the project's
  own claude/codex token data (no extra API call needed).
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import type { ProjectInsight, Session } from '$lib/types.js';
  import { kfmt, relAgo, dayLabel } from '$lib/format.js';
  import AgentMixDonut from '$lib/ui/AgentMixDonut.svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl, tabUrl } from '$lib/dashboard/url-state.js';
  import { hiddenSessionIds } from '$lib/hidden-sessions.svelte.js';

  interface Props {
    project: ProjectAggregate;
    insight: ProjectInsight | null;
    sessions: Session[];
  }
  const { project, insight, sessions }: Props = $props();

  const stats = $derived([
    { k: 'Sessions', v: project.sessions.toLocaleString(), s: 'all-time' },
    { k: 'Messages', v: project.msgs.toLocaleString(), s: `${kfmt(project.tokensIn)} in` },
    { k: 'Tokens out', v: kfmt(project.tokensOut), s: 'cumulative' },
  ]);

  // Agent mix from the project's own CLI breakdown — no extra API call.
  // claude/codex from ProjectAggregate sessionsByCli gives session counts,
  // but we need token counts. We get those from tokensIn split by sessions.
  // Better: use insight.claude / insight.codex if available (both are AgentSlice).
  const donutTotals = $derived.by(() => {
    if (insight) {
      return {
        tokens: insight.tokens_in + insight.tokens_out,
        claude_tokens: insight.claude.tokens,
        codex_tokens: insight.codex.tokens,
        claude_msgs: insight.claude.messages,
        codex_msgs: insight.codex.messages,
      };
    }
    // Fallback: estimate from ProjectAggregate session counts (not token-precise)
    const total = project.sessions;
    if (total === 0) return null;
    const claudeFrac = project.sessionsByCli.claude / total;
    const totalTok = project.tokensIn + project.tokensOut;
    return {
      tokens: totalTok,
      claude_tokens: Math.round(totalTok * claudeFrac),
      codex_tokens: Math.round(totalTok * (1 - claudeFrac)),
      claude_msgs: Math.round(project.msgs * claudeFrac),
      codex_msgs: Math.round(project.msgs * (1 - claudeFrac)),
    };
  });

  // Daily sparkline bars from ProjectInsight.daily (16 days).
  // We keep the full {day, tokens} per bar so the hover tooltip can
  // surface what each bar represents (date + token count + an
  // approximate claude/codex split derived from the project's
  // overall mix).
  const spark = $derived.by<{ day: string; tokens: number }[]>(() => {
    if (!insight?.daily?.length) return [];
    const sorted = [...insight.daily].sort((a, b) => (a.day < b.day ? -1 : 1));
    return sorted.slice(-16);
  });

  const sparkMax = $derived(spark.length > 0 ? Math.max(...spark.map(s => s.tokens), 1) : 1);

  // Project-wide claude/codex token ratio — used to apportion each
  // day's total token count into a likely claude/codex split. The
  // backend's DailyPoint only carries `{day, tokens}`, so this is an
  // ESTIMATE based on the project's all-time mix, not a measured
  // per-day breakdown. The tooltip labels it "~claude / ~codex" so
  // the user can see it's approximate.
  const claudeShare = $derived.by(() => {
    if (!insight) return 1;
    const total = insight.claude.tokens + insight.codex.tokens;
    if (total <= 0) return 1;
    return insight.claude.tokens / total;
  });

  // ── tokens-per-day hover tooltip state ─────────────────────────
  let sparkHoverIdx = $state<number | null>(null);
  let sparkHoverLeft = $state(0);
  let sparkHoverTop  = $state(0);

  function onSparkEnter(i: number, ev: MouseEvent): void {
    sparkHoverIdx = i;
    const el = ev.currentTarget as HTMLElement;
    const wrap = el.closest('.spark-bars') as HTMLElement | null;
    if (!wrap) return;
    const wr = wrap.getBoundingClientRect();
    const br = el.getBoundingClientRect();
    sparkHoverLeft = br.left - wr.left + br.width / 2;
    sparkHoverTop  = br.top - wr.top;
  }
  function onSparkLeave(): void { sparkHoverIdx = null; }

  // Format YYYY-MM-DD into "Mon May 26" for the tooltip header.
  function fmtDay(day: string): string {
    if (!day) return '';
    const d = new Date(day + 'T00:00:00');
    if (Number.isNaN(d.getTime())) return day;
    return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
  }

  // Recent sessions (up to 3), visible only
  const recentSessions = $derived(
    sessions
      .filter((s) => !hiddenSessionIds().has(s.id))
      .sort((a, b) => b.last_msg_at - a.last_msg_at)
      .slice(0, 3)
  );

  function openSession(id: string): void {
    void goto(sessionUrl($page.url.pathname + $page.url.search, id));
  }

  function goToSessions(): void {
    void goto(tabUrl($page.url.pathname, 'sessions'));
  }
</script>

<div style="display: flex; flex-direction: column; gap: 16px;">
  <!-- Stat tiles -->
  <div style="display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px;">
    {#each stats as s}
      <div
        style="
          background: var(--ad-bg-2);
          border: 1px solid var(--ad-border);
          border-radius: 8px;
          padding: 12px 14px;
          display: flex;
          flex-direction: column;
          gap: 2px;
        "
      >
        <span style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">{s.k}</span>
        <span class="ad-mono" style="font-size: 20px; font-weight: 600; color: var(--ad-fg); letter-spacing: -0.02em;">{s.v}</span>
        <span style="font-size: 11px; color: var(--ad-faint);">{s.s}</span>
      </div>
    {/each}
  </div>

  <!-- Daily activity sparkline -->
  {#if spark.length > 0}
    <div
      style="
        background: var(--ad-bg-2);
        border: 1px solid var(--ad-border);
        border-radius: 8px;
        padding: 14px 16px;
      "
    >
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
        <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">Daily activity · {spark.length}d</span>
        <span class="ad-mono" style="font-size: 10.5px; color: var(--ad-faint);">tokens / day</span>
      </div>
      <div class="spark-bars" style="position: relative; display: flex; align-items: flex-end; gap: 3px; height: 56px;">
        {#each spark as point, i (point.day)}
          <div
            role="img"
            aria-label="{point.day}: {kfmt(point.tokens)} tokens"
            onmouseenter={(e) => onSparkEnter(i, e)}
            onmouseleave={onSparkLeave}
            class="spark-bar"
            class:spark-bar-active={sparkHoverIdx === i}
            style="
              flex: 1;
              height: {Math.max((point.tokens / sparkMax) * 100, 3)}%;
              min-height: 2px;
              background: {i === spark.length - 1 ? 'var(--ad-accent)' : 'color-mix(in oklch, var(--ad-accent) 45%, var(--ad-bg-2))'};
              border-radius: 2px;
            "
          ></div>
        {/each}

        {#if sparkHoverIdx !== null && spark[sparkHoverIdx]}
          {@const p = spark[sparkHoverIdx]}
          {@const claudeApprox = Math.round(p.tokens * claudeShare)}
          {@const codexApprox  = Math.max(0, p.tokens - claudeApprox)}
          <div role="tooltip" class="spark-tt ad-mono" style="left: {sparkHoverLeft}px; top: {sparkHoverTop}px;">
            <div class="spark-tt-date">{fmtDay(p.day)} <span class="spark-tt-iso">· {p.day}</span></div>
            <div class="spark-tt-row">
              <span class="spark-tt-sw" style="background: var(--ad-claude);"></span>
              <span class="spark-tt-label">~claude</span>
              <span class="spark-tt-val">{kfmt(claudeApprox)}</span>
            </div>
            <div class="spark-tt-row">
              <span class="spark-tt-sw" style="background: var(--ad-codex);"></span>
              <span class="spark-tt-label">~codex</span>
              <span class="spark-tt-val">{kfmt(codexApprox)}</span>
            </div>
            <div class="spark-tt-row spark-tt-total">
              <span class="spark-tt-label">total</span>
              <span class="spark-tt-val">{kfmt(p.tokens)}</span>
            </div>
            <div class="spark-tt-note">split is project-ratio approximation</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Recent sessions -->
  {#if recentSessions.length > 0}
    <div
      style="
        background: var(--ad-bg-2);
        border: 1px solid var(--ad-border);
        border-radius: 8px;
        padding: 14px 16px;
      "
    >
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
        <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">Recent sessions · {recentSessions.length}</span>
        <button
          type="button"
          onclick={goToSessions}
          style="font-size: 11px; color: var(--ad-faint); background: none; border: none; cursor: pointer; padding: 0;"
        >see full →</button>
      </div>
      <div style="display: flex; flex-direction: column; gap: 4px;">
        {#each recentSessions as s}
          <button
            type="button"
            onclick={() => openSession(s.id)}
            style="
              display: grid;
              grid-template-columns: 60px minmax(0, 1fr) 80px 70px 70px;
              gap: 10px;
              align-items: center;
              padding: 8px 10px;
              border-radius: 6px;
              background: var(--ad-panel);
              border: 1px solid var(--ad-border);
              cursor: pointer;
              text-align: left;
            "
          >
            <span
              class="ad-pill {s.cli === 'claude' ? 'ad-pill--claude' : 'ad-pill--codex'}"
              style="width: fit-content; font-size: 10px;"
            >{s.cli}</span>
            <div style="min-width: 0;">
              <div style="font-size: 12px; color: var(--ad-fg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
                {s.id.slice(0, 8)}
              </div>
              <div class="ad-mono" style="font-size: 10px; color: var(--ad-faint);">
                {dayLabel(s.last_msg_at)}
              </div>
            </div>
            <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-align: right;">{s.msg_count} msgs</span>
            <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-align: right;">↓ {kfmt(s.tokens_out)}</span>
            <span
              class="ad-pill {s.status === 'active' ? 'ad-pill--ok' : ''}"
              style="justify-self: end; font-size: 10px;"
            >{s.status}</span>
          </button>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Agent mix donut -->
  {#if donutTotals && donutTotals.tokens > 0}
    <AgentMixDonut totals={donutTotals} />
  {/if}
</div>

<style>
  /* Daily-activity sparkline: subtle base opacity + a brighter hover
     state so the bar that drives the tooltip is visually highlighted. */
  .spark-bar {
    cursor: pointer;
    transition: opacity 120ms ease-out, filter 120ms ease-out;
  }
  .spark-bar:hover,
  .spark-bar-active {
    filter: brightness(1.18);
  }

  /* Floating tooltip — absolutely positioned over the chart, centered
     above the hovered bar. pointer-events: none so the next bar's
     hover isn't blocked. */
  .spark-tt {
    position: absolute;
    transform: translate(-50%, calc(-100% - 10px));
    pointer-events: none;
    background: var(--ad-panel);
    border: 1px solid var(--ad-border);
    border-radius: 8px;
    padding: 8px 10px;
    font-size: 11px;
    box-shadow: 0 12px 32px color-mix(in oklch, black 50%, transparent);
    z-index: 10;
    min-width: 184px;
    display: flex;
    flex-direction: column;
    gap: 4px;
    white-space: nowrap;
  }
  .spark-tt-date {
    font-size: 11px;
    color: var(--ad-fg);
    font-weight: 600;
    letter-spacing: -0.01em;
    padding-bottom: 4px;
    margin-bottom: 2px;
    border-bottom: 1px solid var(--ad-border-soft);
  }
  .spark-tt-iso { font-weight: 400; color: var(--ad-faint); }
  .spark-tt-row {
    display: grid;
    grid-template-columns: 9px auto 1fr;
    gap: 8px;
    align-items: center;
    font-variant-numeric: tabular-nums;
  }
  .spark-tt-sw {
    width: 9px;
    height: 9px;
    border-radius: 2px;
  }
  .spark-tt-label {
    color: var(--ad-faint);
    font-size: 11px;
  }
  .spark-tt-val {
    color: var(--ad-fg);
    text-align: right;
    font-size: 12px;
    font-weight: 600;
  }
  .spark-tt-total {
    grid-template-columns: auto 1fr;
    padding-top: 4px;
    margin-top: 2px;
    border-top: 1px solid var(--ad-border-soft);
  }
  .spark-tt-total .spark-tt-label {
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 10px;
  }
  .spark-tt-note {
    font-size: 9.5px;
    color: var(--ad-faint);
    font-style: italic;
    padding-top: 4px;
    margin-top: 2px;
    border-top: 1px dashed var(--ad-border-soft);
  }
</style>
