<!--
  Insights — project-centric, subscription-aware metrics.
  Subscription users don't pay per-token; surfacing movement, efficiency,
  and pain signals (cache hit %, tokens/message, compact rate, trend
  vs prior window) is more useful than inferred dollar costs.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchProjectInsights } from '$lib/api.js';
  import type { ProjectInsight, ProjectInsightsResponse } from '$lib/types.js';
  import { kfmt, relTime } from '$lib/format.js';
  import DailyActivityChart from '$lib/ui/DailyActivityChart.svelte';
  import AgentMixDonut from '$lib/ui/AgentMixDonut.svelte';
  import SmoothSparkline from '$lib/ui/SmoothSparkline.svelte';

  type Unit = 'tokens' | 'messages' | 'sessions';
  type SortKey = Unit | 'trend' | 'cache' | 'compact';
  type Window = '1d' | '7d' | '30d' | '90d';

  let unit = $state<Unit>('tokens');
  let sortBy = $state<SortKey>('tokens');
  let win = $state<Window>('30d');
  let expandedId = $state<string | null>(null);

  let resp = $state<ProjectInsightsResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // 1d is a rolling 24-hour window from now — not midnight-to-now. Keeps
  // the math consistent with the other windows so the trend sparkline
  // and totals don't snap at the calendar boundary.
  const WINDOW_MS: Record<Window, number> = {
    '1d':  1 * 86_400_000,
    '7d':  7 * 86_400_000,
    '30d': 30 * 86_400_000,
    '90d': 90 * 86_400_000
  };

  async function load(): Promise<void> {
    loading = true;
    try {
      const now = Date.now();
      resp = await fetchProjectInsights({
        since: now - WINDOW_MS[win],
        until: now,
        top: 3
      });
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load insights';
    } finally {
      loading = false;
    }
  }

  onMount(() => { void load(); });
  $effect(() => { void win; void load(); });

  function projectValue(p: ProjectInsight, u: Unit): number {
    return u === 'tokens' ? p.tokens : u === 'messages' ? p.messages : p.sessions;
  }
  function projectClaudePart(p: ProjectInsight, u: Unit): number {
    return u === 'tokens'
      ? p.claude.tokens
      : u === 'messages'
        ? p.claude.messages
        : p.claude.sessions;
  }
  function projectCodexPart(p: ProjectInsight, u: Unit): number {
    return u === 'tokens'
      ? p.codex.tokens
      : u === 'messages'
        ? p.codex.messages
        : p.codex.sessions;
  }

  const sorted: ProjectInsight[] = $derived.by(() => {
    if (!resp) return [];
    const items = [...resp.projects];
    items.sort((a, b) => {
      if (sortBy === 'cache') return a.cache_hit_pct - b.cache_hit_pct;
      if (sortBy === 'trend') return Math.abs(b.trend_pct) - Math.abs(a.trend_pct);
      if (sortBy === 'compact') return b.compact_count - a.compact_count;
      return projectValue(b, sortBy as Unit) - projectValue(a, sortBy as Unit);
    });
    return items;
  });

  interface Totals {
    tokens: number;
    claude_tokens: number;
    codex_tokens: number;
    claude_msgs: number;
    codex_msgs: number;
    messages: number;
    sessions: number;
    compact: number;
  }

  const totals: Totals = $derived.by(() => {
    if (!resp) {
      return { tokens: 0, claude_tokens: 0, codex_tokens: 0, claude_msgs: 0, codex_msgs: 0, messages: 0, sessions: 0, compact: 0 };
    }
    return resp.projects.reduce<Totals>(
      (acc, p) => ({
        tokens: acc.tokens + p.tokens,
        claude_tokens: acc.claude_tokens + p.claude.tokens,
        codex_tokens: acc.codex_tokens + p.codex.tokens,
        claude_msgs: acc.claude_msgs + p.claude.messages,
        codex_msgs: acc.codex_msgs + p.codex.messages,
        messages: acc.messages + p.messages,
        sessions: acc.sessions + p.sessions,
        compact: acc.compact + p.compact_count
      }),
      { tokens: 0, claude_tokens: 0, codex_tokens: 0, claude_msgs: 0, codex_msgs: 0, messages: 0, sessions: 0, compact: 0 }
    );
  });

  const weightedCache: number = $derived.by(() => {
    if (totals.tokens <= 0 || !resp) return 0;
    return Math.round(
      resp.projects.reduce((s, p) => s + p.cache_hit_pct * p.tokens, 0) / totals.tokens
    );
  });
  const weightedTrend: number = $derived.by(() => {
    if (totals.tokens <= 0 || !resp) return 0;
    return Math.round(
      resp.projects.reduce((s, p) => s + p.trend_pct * p.tokens, 0) / totals.tokens
    );
  });

  const maxValue: number = $derived.by(() =>
    Math.max(1, ...sorted.map((p) => projectValue(p, unit)))
  );

  function fmt(n: number, u: Unit): string {
    return u === 'tokens' ? kfmt(n) : String(n);
  }
  function trendColor(pct: number): string {
    if (pct > 0) return 'var(--ad-warn)';
    if (pct < 0) return 'var(--ad-live)';
    return 'var(--ad-faint)';
  }
  function cacheColor(pct: number): string {
    if (pct >= 90) return 'var(--ad-live)';
    if (pct >= 75) return 'var(--ad-warn)';
    return 'var(--ad-danger)';
  }
  function kpiCacheColor(pct: number): string { return cacheColor(pct); }

  function sparkData(p: ProjectInsight): number[] {
    return p.daily.map((d) => d.tokens);
  }
</script>

<svelte:head><title>klyne — Insights</title></svelte:head>

<div class="page">
  <div class="page-hd">
    <div><h1>Insights</h1></div>
    <div class="row">
      <div class="seg">
        <button class:active={unit === 'tokens'} onclick={() => { unit = 'tokens'; sortBy = 'tokens'; }}>Tokens</button>
        <button class:active={unit === 'messages'} onclick={() => { unit = 'messages'; sortBy = 'messages'; }}>Messages</button>
        <button class:active={unit === 'sessions'} onclick={() => { unit = 'sessions'; sortBy = 'sessions'; }}>Sessions</button>
      </div>
      <div class="seg">
        {#each (['1d', '7d', '30d', '90d'] as const) as w}
          <button class:active={win === w} onclick={() => (win = w)} title={w === '1d' ? 'Past 24 hours' : `Past ${w.replace('d', ' days')}`}>{w}</button>
        {/each}
      </div>
    </div>
  </div>
  <p class="page-sub">
    Subscription-aware metrics. Movement, efficiency, and pain signals — the things dollars never tell you on a fixed plan.
  </p>

  {#if loading && !resp}
    <div style="padding: 40px 20px; text-align: center; color: var(--ad-faint);">Loading…</div>
  {:else if error}
    <div style="padding: 16px; border: 1px solid var(--ad-danger); border-radius: 8px; color: var(--ad-danger); margin-bottom: 16px;">{error}</div>
  {:else if resp}
    <div class="kpis">
      <div class="kpi enter enter-1">
        <div class="k">Trend vs prior {win}</div>
        <div class="v" style:color={weightedTrend > 0 ? 'var(--ad-warn)' : 'var(--ad-live)'}>
          {weightedTrend > 0 ? '↑' : weightedTrend < 0 ? '↓' : '·'}{Math.abs(weightedTrend)}<span style="font-size: 22px; color: var(--ad-faint); margin-left: 2px;">%</span>
        </div>
        <div class="d">{kfmt(totals.tokens)} tokens this {win}</div>
      </div>
      <div class="kpi enter enter-2">
        <div class="k">Cache hit (weighted)</div>
        <div class="v" style:color={kpiCacheColor(weightedCache)}>{weightedCache}<span style="font-size: 22px; color: var(--ad-faint); margin-left: 2px;">%</span></div>
        <div class="d">low cache = re-sending context</div>
      </div>
      <div class="kpi enter enter-3">
        <div class="k">Tokens / message</div>
        <div class="v mono-v">{totals.messages > 0 ? kfmt(Math.round(totals.tokens / totals.messages)) : '—'}</div>
        <div class="d">efficiency — lower is leaner</div>
      </div>
      <div class="kpi enter enter-4">
        <div class="k">Sessions hit /compact</div>
        <div class="v" style:color={totals.compact > 10 ? 'var(--ad-danger)' : 'var(--ad-warn)'}>{totals.compact}</div>
        <div class="d">{totals.sessions > 0 ? Math.round(totals.compact / totals.sessions * 100) : 0}% of {totals.sessions} sessions ran out of context</div>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1.6fr 1fr; gap: 12px; margin-bottom: 14px;">
      <DailyActivityChart projects={resp.projects} />
      <AgentMixDonut totals={totals} />
    </div>

    <div class="chart-card enter enter-4" style="margin-bottom: 14px;">
      <div class="row" style="margin-bottom: 14px; align-items: baseline; flex-wrap: wrap; gap: 10px;">
        <h3 style="margin: 0;">Projects ranked</h3>
        <span class="sub" style="margin: 0;">· {win} · {sorted.length} projects</span>
        <span class="spacer"></span>
        <span class="faint mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.10em; margin-right: 4px;">sort</span>
        <div class="seg">
          <button class:active={sortBy === unit} onclick={() => (sortBy = unit)}>volume</button>
          <button class:active={sortBy === 'trend'} onclick={() => (sortBy = 'trend')}>trend</button>
          <button class:active={sortBy === 'cache'} onclick={() => (sortBy = 'cache')}>cache↓</button>
          <button class:active={sortBy === 'compact'} onclick={() => (sortBy = 'compact')}>/compact</button>
        </div>
      </div>

      <div style="display: grid; grid-template-columns: 14px 170px 1fr 80px 70px 90px 90px 60px 70px; gap: 10px; padding: 0 0 8px; border-bottom: 1px solid var(--ad-border-soft); font-size: 10px; text-transform: uppercase; letter-spacing: 0.10em; color: var(--ad-faint); font-family: var(--ad-font-mono); font-weight: 600;">
        <span></span>
        <span>Project</span>
        <span>{unit} (claude / codex)</span>
        <span style="text-align: right;">{unit}</span>
        <span style="text-align: right;">trend</span>
        <span style="text-align: right;">cache hit</span>
        <span style="text-align: right;">tok / msg</span>
        <span style="text-align: right;">/compact</span>
        <span></span>
      </div>

      <div style="display: flex; flex-direction: column;">
        {#each sorted as p (p.project_path)}
          {@const total = projectValue(p, unit)}
          {@const cPart = projectClaudePart(p, unit)}
          {@const xPart = projectCodexPart(p, unit)}
          {@const widthPct = total > 0 ? (total / maxValue) * 100 : 0}
          {@const cPct = total > 0 ? (cPart / total) * 100 : 0}
          {@const xPct = total > 0 ? (xPart / total) * 100 : 0}
          {@const isOpen = expandedId === p.project_path}
          <div class="proj-rank-row">
            <div
              style="display: grid; grid-template-columns: 14px 170px 1fr 80px 70px 90px 90px 60px 70px; gap: 10px; padding: 11px 0; cursor: pointer; align-items: center; border-bottom: 1px solid var(--ad-border-soft);"
              onclick={() => (expandedId = isOpen ? null : p.project_path)}
              role="button"
              tabindex="0"
              onkeydown={(e) => { if (e.key === 'Enter') expandedId = isOpen ? null : p.project_path; }}
            >
              <span class="mono faint" style="font-size: 10.5px; text-align: right; transition: transform 180ms cubic-bezier(.2,.8,.2,1); display: inline-block; transform: rotate({isOpen ? 90 : 0}deg);">▸</span>
              <div style="display: flex; align-items: center; gap: 7px; min-width: 0;">
                <span class="dot {p.last_msg_at > Date.now() - 60_000 ? 'dot--live' : 'dot--idle'}"></span>
                <span style="font-size: 12.75px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; letter-spacing: -0.005em;">{p.name}</span>
              </div>
              <div style="height: 16px; position: relative; background: var(--ad-bg-2); border-radius: 5px; overflow: hidden; border: 1px solid var(--ad-border-soft);">
                <div style="position: absolute; inset: 0; width: {widthPct}%; display: flex; transition: width 700ms cubic-bezier(.2,.8,.2,1);">
                  {#if cPart > 0}
                    <div style="width: {cPct}%; background: var(--ad-claude);" title="claude · {fmt(cPart, unit)}"></div>
                  {/if}
                  {#if xPart > 0}
                    <div style="width: {xPct}%; background: var(--ad-codex);" title="codex · {fmt(xPart, unit)}"></div>
                  {/if}
                </div>
              </div>
              <span class="mono tnum" style="font-size: 12.5px; text-align: right; font-weight: 600;">{fmt(total, unit)}</span>
              <span class="mono tnum" style="font-size: 11.5px; text-align: right; color: {trendColor(p.trend_pct)}; font-weight: 600;">
                {p.trend_pct > 0 ? '↑' : p.trend_pct < 0 ? '↓' : '·'}{Math.abs(Math.round(p.trend_pct))}%
              </span>
              <span class="mono tnum" style="font-size: 11.5px; text-align: right; color: {cacheColor(p.cache_hit_pct)}; font-weight: 600;">{Math.round(p.cache_hit_pct)}%</span>
              <span class="mono tnum faint" style="font-size: 11.5px; text-align: right;">{kfmt(Math.round(p.tokens_per_message))}</span>
              <span class="mono tnum" style="font-size: 11.5px; text-align: right; color: {p.compact_count > 0 ? 'var(--ad-danger)' : 'var(--ad-faint)'}; font-weight: {p.compact_count > 0 ? 600 : 400};">
                {p.compact_count > 0 ? `${p.compact_count}/${p.sessions}` : '—'}
              </span>
              <button
                class="btn btn--ghost btn--sm"
                style="justify-self: end; font-size: 11px; padding: 3px 8px;"
                title="Open {p.name} — all sessions for this project"
                aria-label="Open project {p.name}"
                onclick={(e) => { e.stopPropagation(); goto(`/insights/projects/${encodeURIComponent(p.name)}`); }}
              >open →</button>
            </div>

            {#if isOpen}
              <div style="padding: 14px 14px 18px 26px; background: var(--ad-bg-2); border-bottom: 1px solid var(--ad-border-soft); animation: fadeUp 260ms cubic-bezier(.2,.8,.2,1) both;">
                <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 22px;">
                  <div>
                    <div class="faint mono" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.10em; margin-bottom: 10px; font-weight: 600;">Agent breakdown</div>
                    <div style="display: flex; flex-direction: column; gap: 8px;">
                      <div class="row" style="gap: 8px;">
                        <span class="badge badge--claude" style="width: 56px; justify-content: center;">claude</span>
                        <span class="mono tnum" style="font-size: 12.5px;">{kfmt(p.claude.tokens)} tok</span>
                        <span class="faint mono" style="font-size: 11px;">· {kfmt(p.claude.messages)} msgs</span>
                      </div>
                      <div class="row" style="gap: 8px;">
                        <span class="badge badge--codex" style="width: 56px; justify-content: center;">codex</span>
                        <span class="mono tnum" style="font-size: 12.5px;">{kfmt(p.codex.tokens)} tok</span>
                        <span class="faint mono" style="font-size: 11px;">· {kfmt(p.codex.messages)} msgs</span>
                      </div>
                    </div>
                    <div class="hr" style="margin: 14px 0 10px;"></div>
                    <div class="faint mono" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.10em; margin-bottom: 8px; font-weight: 600;">Daily · last {win}</div>
                    {#if p.daily.length > 0}
                      <SmoothSparkline data={sparkData(p)} />
                    {:else}
                      <div class="faint mono" style="font-size: 11px;">no daily activity in window</div>
                    {/if}
                  </div>
                  <div>
                    <div class="faint mono" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.10em; margin-bottom: 10px; font-weight: 600;">Top sessions</div>
                    {#if p.top_sessions.length > 0}
                      <div style="display: flex; flex-direction: column; gap: 5px;">
                        {#each p.top_sessions as s (s.session_id)}
                          <div class="row" style="padding: 7px 10px; background: var(--ad-panel); border-radius: 7px; border: 1px solid var(--ad-border-soft); gap: 10px;">
                            <span class="mono" style="font-size: 10.5px; color: var(--ad-fg-2); min-width: 64px;">{s.session_id.slice(0, 8)}…</span>
                            <span class="mono faint" style="font-size: 11px;">{kfmt(s.tokens)} tok</span>
                            <span class="mono faint" style="font-size: 11px;">{kfmt(s.messages)} msg</span>
                            <span class="spacer"></span>
                            <span class="mono faint" style="font-size: 10.5px;">{relTime(s.last_msg_at)}</span>
                            <button class="btn btn--ghost btn--sm" onclick={(e) => { e.stopPropagation(); goto(`/insights/sessions/${encodeURIComponent(s.session_id)}`); }}>open →</button>
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <div class="faint mono" style="font-size: 11px;">no sessions in window</div>
                    {/if}
                    {#if p.compact_count > 0}
                      <div class="faint mono" style="font-size: 11px; margin-top: 10px;">
                        {p.compact_count} of {p.sessions} session(s) ran out of context.
                      </div>
                    {/if}
                    <div style="margin-top: 14px;">
                      <button
                        class="btn btn--primary btn--sm"
                        onclick={() => goto(`/insights/projects/${encodeURIComponent(p.name)}`)}
                      >Open full project →</button>
                    </div>
                  </div>
                </div>
              </div>
            {/if}
          </div>
        {/each}

        {#if sorted.length === 0}
          <div style="padding: 26px 12px; color: var(--ad-faint); text-align: center; font-size: 12.5px;">
            No projects with activity in this window yet.
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>
