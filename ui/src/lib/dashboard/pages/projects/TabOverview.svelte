<!--
  TabOverview — Overview tab for a single project.
  Shows 4 stat tiles, a daily activity chart (from ProjectInsight.daily),
  a recent-sessions list (top 3), and an AgentMixDonut from the project's
  own claude/codex token data (no extra API call needed).
-->
<script lang="ts">
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import type { ProjectInsight, Session } from '$lib/types.js';
  import { kfmt, costFmt, relAgo, dayLabel } from '$lib/format.js';
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
    { k: 'Cost', v: costFmt(project.cost, project.priced), s: project.priced ? 'all-time' : 'no API key' },
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

  // Daily sparkline bars from ProjectInsight.daily (16 days)
  const spark = $derived.by(() => {
    if (!insight?.daily?.length) return [];
    const sorted = [...insight.daily].sort((a, b) => (a.day < b.day ? -1 : 1));
    return sorted.slice(-16).map((d) => d.tokens);
  });

  const sparkMax = $derived(spark.length > 0 ? Math.max(...spark, 1) : 1);

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
      <div style="display: flex; align-items: flex-end; gap: 3px; height: 56px;">
        {#each spark as v, i}
          <div
            style="
              flex: 1;
              height: {Math.max((v / sparkMax) * 100, 3)}%;
              min-height: 2px;
              background: {i === spark.length - 1 ? 'var(--ad-accent)' : 'color-mix(in oklch, var(--ad-accent) 45%, var(--ad-bg-2))'};
              border-radius: 2px;
            "
          ></div>
        {/each}
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
