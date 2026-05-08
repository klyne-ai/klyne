<!--
  UsageBadge — TopNav badge with hover popover that mirrors what the
  Claude Code CLI / SwiftBar plugin shows.

  Data path:
    - Claude: backend reads the OAuth token from macOS Keychain and calls
      Anthropic's first-party /api/oauth/usage. Percentages and reset
      timestamps come back already bound to the user's plan tier — no
      guesswork. We display these verbatim under `usage.claude.oauth`.
      If the OAuth call fails (no token, expired, network), we fall back
      to a token-based estimate computed from the local message log.
    - Codex: no equivalent OAuth endpoint exists, so we use a
      community-estimated tier table the user can pick from.

  Refreshes: 60s poll + on every SSE MsgNew (debounced 600ms) + a 30s
  local-clock tick that keeps the "resets in" countdown live without
  hitting the daemon.
-->
<script lang="ts">
  import { fetchUsage } from '$lib/api';
  import type { OAuthUsage, OAuthWindow, UsageCLI, UsageResponse, UsageWindow } from '$lib/types';
  import { subscribe } from '$lib/sse';

  // ---------------------------------------------------------------------------
  // Codex plan tier — no OAuth endpoint, so percentage is derived from
  // local token sums against a community-estimated cap. User picks tier in
  // the popover; choice is persisted in localStorage.
  // ---------------------------------------------------------------------------

  type CodexTier = 'plus' | 'pro' | 'business';

  interface CodexLimits {
    label: string;
    fiveHourOut: number;
    sevenDayOut: number;
  }

  const CODEX_LIMITS: Record<CodexTier, CodexLimits> = {
    plus:     { label: 'Plus',     fiveHourOut:    500_000, sevenDayOut:  6_000_000 },
    pro:      { label: 'Pro',      fiveHourOut:  2_500_000, sevenDayOut: 30_000_000 },
    business: { label: 'Business', fiveHourOut:  5_000_000, sevenDayOut: 60_000_000 }
  };

  const LS_CODEX_KEY = 'klyne.usage.plan.codex';

  function readCodexTier(): CodexTier {
    if (typeof localStorage === 'undefined') return 'plus';
    const v = localStorage.getItem(LS_CODEX_KEY);
    if (v === 'plus' || v === 'pro' || v === 'business') return v;
    return 'plus';
  }

  function writeCodexTier(t: CodexTier): void {
    if (typeof localStorage !== 'undefined') localStorage.setItem(LS_CODEX_KEY, t);
  }

  // ---------------------------------------------------------------------------
  // Reactive state
  // ---------------------------------------------------------------------------

  let usage = $state<UsageResponse | null>(null);
  let loadError = $state<string | null>(null);
  let open = $state(false);
  let codexTier = $state<CodexTier>(readCodexTier());
  let nowMs = $state(Date.now());

  const codexLimits = $derived(CODEX_LIMITS[codexTier]);

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------

  function pct(used: number, cap: number): number {
    if (cap <= 0) return 0;
    return Math.max(0, Math.min(100, Math.round((used / cap) * 100)));
  }

  function clampPct(p: number): number {
    return Math.max(0, Math.min(100, Math.round(p)));
  }

  function resetsInMsLocal(window: UsageWindow, now: number): number {
    if (window.first_msg_ts <= 0) return window.window_seconds * 1000;
    return Math.max(0, window.first_msg_ts + window.window_seconds * 1000 - now);
  }

  function resetsInMsOAuth(w: OAuthWindow, now: number): number {
    if (w.resets_at <= 0) return 0;
    return Math.max(0, w.resets_at - now);
  }

  function formatDuration(ms: number): string {
    if (ms <= 0) return '0m';
    const totalMin = Math.floor(ms / 60_000);
    const hrs = Math.floor(totalMin / 60);
    const min = totalMin % 60;
    if (hrs <= 0) return `${min}m`;
    if (hrs >= 24) {
      const days = Math.floor(hrs / 24);
      const remHrs = hrs % 24;
      return remHrs > 0 ? `${days}d${remHrs}h` : `${days}d`;
    }
    return min > 0 ? `${hrs}h${min}m` : `${hrs}h`;
  }

  function compactTokens(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return n.toString();
  }

  // ---------------------------------------------------------------------------
  // Derived view-models
  // ---------------------------------------------------------------------------

  interface Row {
    label: string;
    pct: number;
    resetsIn: string;
    /** Raw "12.5M out" string when we have local token data; empty otherwise. */
    detail: string;
  }

  function buildClaudeRowsOAuth(o: OAuthUsage, cli: UsageCLI, now: number): Row[] {
    const rows: Row[] = [];
    const five = o.five_hour;
    if (five) {
      rows.push({
        label: '5-hour window',
        pct: clampPct(five.utilization_pct),
        resetsIn: formatDuration(resetsInMsOAuth(five, now)),
        detail: `${compactTokens(cli.window_5h.tokens_out)} out · ${cli.window_5h.messages} msgs`
      });
    }
    const seven = o.seven_day;
    if (seven) {
      rows.push({
        label: '7-day window',
        pct: clampPct(seven.utilization_pct),
        resetsIn: formatDuration(resetsInMsOAuth(seven, now)),
        detail: `${compactTokens(cli.window_7d.tokens_out)} out · ${cli.window_7d.messages} msgs`
      });
    }
    const son = o.seven_day_sonnet;
    if (son) {
      rows.push({
        label: '7-day Sonnet',
        pct: clampPct(son.utilization_pct),
        resetsIn: formatDuration(resetsInMsOAuth(son, now)),
        detail: `${compactTokens(cli.window_7d_sonnet.tokens_out)} out · ${cli.window_7d_sonnet.messages} msgs`
      });
    }
    return rows;
  }

  function buildCodexRowsOAuth(o: OAuthUsage, cli: UsageCLI, now: number): Row[] {
    const rows: Row[] = [];
    const five = o.five_hour;
    if (five) {
      rows.push({
        label: '5-hour window',
        pct: clampPct(five.utilization_pct),
        resetsIn: five.resets_at > 0
          ? formatDuration(resetsInMsOAuth(five, now))
          : 'rolled over',
        detail: `${compactTokens(cli.window_5h.tokens_out)} out · ${cli.window_5h.messages} msgs`
      });
    }
    const seven = o.seven_day;
    if (seven) {
      rows.push({
        label: '7-day window',
        pct: clampPct(seven.utilization_pct),
        resetsIn: seven.resets_at > 0
          ? formatDuration(resetsInMsOAuth(seven, now))
          : 'rolled over',
        detail: `${compactTokens(cli.window_7d.tokens_out)} out · ${cli.window_7d.messages} msgs`
      });
    }
    return rows;
  }

  function buildCodexRowsLocal(cli: UsageCLI, now: number): Row[] {
    return [
      {
        label: '5-hour window',
        pct: pct(cli.window_5h.tokens_out, codexLimits.fiveHourOut),
        resetsIn: formatDuration(resetsInMsLocal(cli.window_5h, now)),
        detail: `${compactTokens(cli.window_5h.tokens_out)} / ${compactTokens(codexLimits.fiveHourOut)} out`
      },
      {
        label: '7-day window',
        pct: pct(cli.window_7d.tokens_out, codexLimits.sevenDayOut),
        resetsIn: formatDuration(resetsInMsLocal(cli.window_7d, now)),
        detail: `${compactTokens(cli.window_7d.tokens_out)} / ${compactTokens(codexLimits.sevenDayOut)} out`
      }
    ];
  }

  const claudeOAuth = $derived(usage?.claude.oauth ?? null);
  const codexOAuth = $derived(usage?.codex.oauth ?? null);
  const claudeRows = $derived(usage && claudeOAuth ? buildClaudeRowsOAuth(claudeOAuth, usage.claude, nowMs) : []);
  const codexRows = $derived(
    usage
      ? codexOAuth
        ? buildCodexRowsOAuth(codexOAuth, usage.codex, nowMs)
        : buildCodexRowsLocal(usage.codex, nowMs)
      : []
  );

  function planLabel(t?: string): string {
    if (!t) return '';
    return t.charAt(0).toUpperCase() + t.slice(1);
  }
  const claudePlanLabel = $derived(planLabel(claudeOAuth?.subscription_type));
  const codexPlanLabel = $derived(planLabel(codexOAuth?.subscription_type));

  // Badge label = the highest of (Claude 5h OAuth, Codex 5h estimate). 5h
  // is the most actionable number — the window most likely to trip you up.
  const claudeTopPct = $derived(claudeRows.length > 0 ? claudeRows[0].pct : 0);
  const codexTopPct = $derived(codexRows.length > 0 ? codexRows[0].pct : 0);
  const topPct = $derived(Math.max(claudeTopPct, codexTopPct));
  const topCli = $derived(claudeTopPct >= codexTopPct ? 'C' : 'X');

  function pctColor(p: number): string {
    if (p >= 90) return 'var(--ad-danger, #f87171)';
    if (p >= 70) return 'var(--ad-warn, #fbbf24)';
    return 'var(--ad-good, #4ade80)';
  }

  // ---------------------------------------------------------------------------
  // Lifecycle
  // ---------------------------------------------------------------------------

  async function refresh(): Promise<void> {
    try {
      usage = await fetchUsage();
      loadError = null;
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err);
    }
  }

  let refreshTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleRefresh(): void {
    if (refreshTimer !== null) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      void refresh();
    }, 600);
  }

  $effect(() => {
    void refresh();
    // Poll every 5 min — matches the server-side cache TTL so we never
    // make a redundant request. Page reloads, SSE-driven refreshes, and
    // this poll all coalesce through the cache into one upstream call
    // per window. The 30s clock tick below keeps the countdown live
    // without touching the network.
    const pollId = setInterval(() => void refresh(), 5 * 60_000);
    const tickId = setInterval(() => { nowMs = Date.now(); }, 30_000);
    const unsubSSE = subscribe({
      onMsgNew: () => scheduleRefresh()
    });
    return () => {
      clearInterval(pollId);
      clearInterval(tickId);
      unsubSSE();
      if (refreshTimer !== null) clearTimeout(refreshTimer);
    };
  });

  // Close popover on click-outside.
  let popoverEl: HTMLDivElement | null = $state(null);
  let buttonEl: HTMLButtonElement | null = $state(null);

  function onDocClick(e: MouseEvent): void {
    if (!open) return;
    const t = e.target as Node | null;
    if (popoverEl && t && popoverEl.contains(t)) return;
    if (buttonEl && t && buttonEl.contains(t)) return;
    open = false;
  }

  $effect(() => {
    document.addEventListener('click', onDocClick);
    return () => document.removeEventListener('click', onDocClick);
  });

  function onCodexTierChange(e: Event): void {
    const v = (e.target as HTMLSelectElement).value as CodexTier;
    codexTier = v;
    writeCodexTier(v);
  }
</script>

<div class="usage-wrap">
  <button
    bind:this={buttonEl}
    class="ad-btn ad-btn--ghost usage-btn"
    onclick={() => (open = !open)}
    title="Plan-tier usage. Click for details."
    style:color={pctColor(topPct)}
  >
    <span class="usage-cli-letter">{topCli}:</span>
    <span class="usage-pct">{topPct}%</span>
  </button>

  {#if open}
    <div bind:this={popoverEl} class="usage-pop" role="dialog" aria-label="Plan-tier usage">
      {#if loadError}
        <div class="usage-err">Failed to load: {loadError}</div>
      {:else if !usage}
        <div class="usage-loading">Loading…</div>
      {:else}
        <!-- Claude block -->
        <div class="usage-section">
          <div class="usage-section-head">
            <span class="ad-badge ad-badge--claude">claude</span>
            {#if claudeOAuth}
              <span class="usage-source" title="Live data from Anthropic /api/oauth/usage">
                {#if claudePlanLabel}{claudePlanLabel} · {/if}live
              </span>
            {:else}
              <span class="usage-source usage-source--degraded" title="OAuth call failed; showing local estimate only">
                local estimate
              </span>
            {/if}
          </div>
          {#if claudeOAuth && claudeRows.length > 0}
            {#each claudeRows as row}
              <div class="usage-row">
                <div class="usage-row-head">
                  <span class="usage-row-label">{row.label}</span>
                  <span class="usage-row-pct" style:color={pctColor(row.pct)}>{row.pct}%</span>
                </div>
                <div class="usage-bar">
                  <div class="usage-bar-fill" style:width="{row.pct}%" style:background={pctColor(row.pct)}></div>
                </div>
                <div class="usage-row-meta">
                  <span>{row.detail}</span>
                  <span>resets in {row.resetsIn}</span>
                </div>
              </div>
            {/each}
          {:else}
            <div class="usage-empty">
              No Claude OAuth credentials available. Sign in with the Claude
              Code CLI to see live plan utilization here.
            </div>
          {/if}
        </div>

        <!-- Codex block -->
        <div class="usage-section">
          <div class="usage-section-head">
            <span class="ad-badge ad-badge--codex">codex</span>
            {#if codexOAuth}
              <span class="usage-source" title="Live snapshot from Codex CLI session log">
                {#if codexPlanLabel}{codexPlanLabel} · {/if}live
              </span>
            {:else}
              <select class="usage-select" value={codexTier} onchange={onCodexTierChange} aria-label="Codex plan tier">
                {#each Object.entries(CODEX_LIMITS) as [key, info]}
                  <option value={key}>{info.label}</option>
                {/each}
              </select>
            {/if}
          </div>
          {#each codexRows as row}
            <div class="usage-row">
              <div class="usage-row-head">
                <span class="usage-row-label">{row.label}</span>
                <span class="usage-row-pct" style:color={pctColor(row.pct)}>{row.pct}%</span>
              </div>
              <div class="usage-bar">
                <div class="usage-bar-fill" style:width="{row.pct}%" style:background={pctColor(row.pct)}></div>
              </div>
              <div class="usage-row-meta">
                <span>{row.detail}</span>
                <span>resets in {row.resetsIn}</span>
              </div>
            </div>
          {/each}
        </div>

        <div class="usage-note">
          {#if claudeOAuth && codexOAuth}
            All percentages are vendor-canonical: Claude from Anthropic's
            OAuth endpoint, Codex from the CLI's own rate-limit snapshot.
          {:else if claudeOAuth}
            Claude numbers are pulled live from Anthropic.
            Codex caps are community estimates — pick your plan to recalibrate.
          {:else if codexOAuth}
            Codex numbers are pulled live from the CLI rate-limit snapshot.
            Claude OAuth is unavailable — falling back to local estimate.
          {:else}
            Both CLIs are showing local token estimates against community
            plan caps. Sign in with each CLI to surface live data.
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .usage-wrap {
    position: relative;
  }
  .usage-btn {
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
    font-family: var(--ad-font-mono);
    font-size: 12px;
    font-weight: 600;
  }
  .usage-cli-letter {
    opacity: 0.6;
    font-size: 11px;
  }
  .usage-pop {
    position: absolute;
    top: calc(100% + 8px);
    right: 0;
    width: 320px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border);
    border-radius: 8px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
    padding: 12px;
    z-index: 50;
    font-size: 12px;
  }
  .usage-section + .usage-section {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px solid var(--ad-border);
  }
  .usage-section-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }
  .usage-source {
    color: var(--ad-faint);
    font-family: var(--ad-font-mono);
    font-size: 10px;
    letter-spacing: 0.04em;
    text-transform: lowercase;
  }
  .usage-source--degraded {
    color: var(--ad-warn, #fbbf24);
  }
  .usage-select {
    background: var(--ad-bg);
    color: var(--ad-fg);
    border: 1px solid var(--ad-border);
    border-radius: 4px;
    padding: 2px 6px;
    font-size: 11px;
    font-family: var(--ad-font-mono);
  }
  .usage-row + .usage-row {
    margin-top: 10px;
  }
  .usage-row-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }
  .usage-row-label {
    color: var(--ad-muted);
    font-size: 11px;
  }
  .usage-row-pct {
    font-family: var(--ad-font-mono);
    font-weight: 700;
    font-size: 13px;
  }
  .usage-bar {
    margin-top: 4px;
    height: 4px;
    background: var(--ad-panel);
    border-radius: 2px;
    overflow: hidden;
  }
  .usage-bar-fill {
    height: 100%;
    transition: width 240ms ease-out, background 240ms ease-out;
  }
  .usage-row-meta {
    margin-top: 4px;
    display: flex;
    justify-content: space-between;
    color: var(--ad-faint);
    font-family: var(--ad-font-mono);
    font-size: 10px;
  }
  .usage-note {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid var(--ad-border);
    color: var(--ad-faint);
    font-size: 10px;
    line-height: 1.4;
  }
  .usage-empty {
    color: var(--ad-faint);
    font-size: 11px;
    padding: 4px 0;
    line-height: 1.5;
  }
  .usage-loading,
  .usage-err {
    color: var(--ad-muted);
    font-size: 12px;
    text-align: center;
    padding: 8px 0;
  }
  .usage-err {
    color: var(--ad-danger, #f87171);
  }
</style>
