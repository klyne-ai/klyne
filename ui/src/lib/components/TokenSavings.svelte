<!--
  TokenSavings — per-session "save tokens" indicator on /sessions/[id].

  Three layered UI elements driven off a single SessionUsageResponse:

    1. Passive context-fill bar (always visible). Shows
       "Session N% full · Next turn ≈ M% of 5h" with a colour shift
       green → amber → red as fill grows.

    2. Active "Compact now" CTA (>50% fill, calibrated only). Shows the
       concrete next-turn savings delta plus a one-click /compact copy
       button and a "Start fresh" alternative.

    3. AI break advisor (>60% fill). One-click button that hits
       /sessions/{id}/break-advice and renders the verdict
       (start_fresh / compact / continue / unavailable) with a reason.

  Refresh policy mirrors UsageBadge:
    - on mount + on sessionId change: fetchSessionUsage()
    - poll every 60s
    - re-fetch when SSE msg.new arrives for this session (debounced)
  Break-advice is fetched only on user click and cached locally for the
  component's lifetime (server already caches for 10 min).

  Uncalibrated state: when next_turn_pct_5h === -1 we have no recent
  /usage telemetry to project against the 5h window; the bar still
  renders, the "next turn" text shows "—", and the savings CTA is
  hidden so we never show a misleading number.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { fetchSessionUsage, fetchBreakAdvice, fetchSession } from '$lib/api';
  import { subscribe } from '$lib/sse';
  import { kfmt } from '$lib/format';
  import type {
    BreakAdviceResponse,
    BreakAdviceVerdict,
    CLI,
    SessionUsageResponse
  } from '$lib/types';

  // ---------------------------------------------------------------------------
  // Props
  // ---------------------------------------------------------------------------

  interface Props {
    sessionId: string;
    cli: CLI;
    /** Optional: parent can pass project_path to avoid an extra fetch
     *  for the "start fresh" command. When omitted, the component will
     *  call fetchSession() once to learn the path. */
    projectPath?: string;
  }

  let { sessionId, cli, projectPath }: Props = $props();

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  let usage = $state<SessionUsageResponse | null>(null);
  let advice = $state<BreakAdviceResponse | null>(null);
  let adviceError = $state<string | null>(null);
  let loadingAdvice = $state(false);
  let copyFeedback = $state(false);
  let copyFreshFeedback = $state(false);
  /** Project path discovered via fetchSession() when the parent
   *  doesn't pass one in via the prop. The component prefers the
   *  prop when available — see derivedProjectPath below. */
  let fetchedProjectPath = $state<string | null>(null);

  /** Effective project path used to build the "start fresh" command. */
  const derivedProjectPath = $derived(projectPath ?? fetchedProjectPath ?? '');

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------

  /** Format a percentage value, rendering -1 sentinel as em dash and
   *  collapsing values below 0.1% to "<0.1%" so we never display a
   *  misleading "0.0%" for a non-zero projection. */
  function fmtPct(v: number): string {
    if (v < 0) return '—';
    if (v < 0.1) return '<0.1%';
    return `${v.toFixed(1)}%`;
  }

  function clampPct(p: number): number {
    if (!Number.isFinite(p) || p < 0) return 0;
    if (p > 100) return 100;
    return p;
  }

  /** Pick the bar fill color from the same green/amber/red palette
   *  UsageBadge uses, but driven off context_fill_pct (not utilization). */
  function fillColor(p: number): string {
    if (p >= 75) return 'var(--ad-error, #f85149)';
    if (p >= 50) return 'var(--ad-compact, #d29922)';
    return 'var(--ad-active, #3fb950)';
  }

  function verdictBadgeClass(v: BreakAdviceVerdict): string {
    switch (v) {
      case 'start_fresh':
        return 'ad-badge ad-badge--active';
      case 'compact':
        return 'ad-badge ad-badge--compact';
      case 'continue':
      case 'unavailable':
      default:
        return 'ad-badge ad-badge--idle';
    }
  }

  function verdictLabel(v: BreakAdviceVerdict): string {
    switch (v) {
      case 'start_fresh':
        return 'Start a fresh session';
      case 'compact':
        return 'Compact this session';
      case 'continue':
        return 'Keep going';
      case 'unavailable':
      default:
        return 'Advice unavailable';
    }
  }

  /** Build the equivalent of resumeCommand() but WITHOUT --resume so the
   *  user lands in a fresh session in the same project directory. */
  function startFreshCommand(): string {
    const bin = cli === 'codex' ? 'codex' : 'claude';
    const path = derivedProjectPath;
    if (!path) return bin;
    const safePath = path.replace(/'/g, `'\\''`);
    return `cd '${safePath}' && ${bin}`;
  }

  // ---------------------------------------------------------------------------
  // Data loading
  // ---------------------------------------------------------------------------

  async function refreshUsage(id: string): Promise<void> {
    if (!id) return;
    try {
      usage = await fetchSessionUsage(id);
    } catch {
      // Silent degrade — like UsageBadge does on OAuth failures.
      usage = null;
    }
  }

  async function ensureProjectPath(id: string): Promise<void> {
    if (projectPath || fetchedProjectPath || !id) return;
    try {
      const r = await fetchSession(id);
      fetchedProjectPath = r.session.project_path;
    } catch {
      // Non-fatal; the start-fresh button just won't have a cd.
    }
  }

  async function loadAdvice(id: string): Promise<void> {
    if (!id) return;
    loadingAdvice = true;
    adviceError = null;
    try {
      advice = await fetchBreakAdvice(id);
    } catch (e) {
      adviceError = e instanceof Error ? e.message : 'failed';
      advice = null;
    } finally {
      loadingAdvice = false;
    }
  }

  function refreshAdvice(): void {
    advice = null;
    adviceError = null;
    void loadAdvice(sessionId);
  }

  // ---------------------------------------------------------------------------
  // Copy interactions
  // ---------------------------------------------------------------------------

  async function copyCompact(): Promise<void> {
    try {
      await navigator.clipboard.writeText('/compact');
    } catch {
      // ignore — clipboard API may be unavailable in some sandboxed envs
    }
    copyFeedback = true;
    setTimeout(() => {
      copyFeedback = false;
    }, 1200);
  }

  async function copyStartFresh(): Promise<void> {
    try {
      await navigator.clipboard.writeText(startFreshCommand());
    } catch {
      // ignore
    }
    copyFreshFeedback = true;
    setTimeout(() => {
      copyFreshFeedback = false;
    }, 1200);
  }

  // ---------------------------------------------------------------------------
  // Lifecycle: poll + SSE
  // ---------------------------------------------------------------------------

  let pollId: ReturnType<typeof setInterval> | null = null;
  let unsubSSE: (() => void) | null = null;
  let refreshTimer: ReturnType<typeof setTimeout> | null = null;

  /** Debounced re-fetch on SSE msg.new — bursts of events shouldn't
   *  produce a fetch storm. Mirrors UsageBadge's scheduleRefresh(). */
  function scheduleUsageRefresh(): void {
    if (refreshTimer !== null) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      void refreshUsage(sessionId);
    }, 600);
  }

  onMount(() => {
    pollId = setInterval(() => void refreshUsage(sessionId), 60_000);

    unsubSSE = subscribe({
      onMsgNew: (payload) => {
        if (payload.session_id === sessionId) scheduleUsageRefresh();
      }
    });
  });

  onDestroy(() => {
    if (pollId !== null) clearInterval(pollId);
    if (refreshTimer !== null) clearTimeout(refreshTimer);
    unsubSSE?.();
    unsubSSE = null;
  });

  // Re-fetch on mount and whenever the sessionId prop changes (route
  // navigation between sessions while the component is mounted).
  $effect(() => {
    const id = sessionId;
    if (id) {
      void refreshUsage(id);
      void ensureProjectPath(id);
      // Drop stale advice when the session changes.
      advice = null;
      adviceError = null;
    }
  });

  // ---------------------------------------------------------------------------
  // Derived view-model
  // ---------------------------------------------------------------------------

  const fillPct = $derived(usage ? clampPct(usage.context_fill_pct) : 0);
  const calibrated = $derived(!!usage && (usage.next_turn_pct_5h ?? -1) >= 0);
  const showCompactCta = $derived(
    !!usage && (usage.context_fill_pct ?? 0) >= 50 && (usage.next_turn_pct_5h ?? -1) >= 0
  );
  const showBreakAdvisor = $derived(!!usage && (usage.context_fill_pct ?? 0) >= 60);
</script>

{#if usage === null}
  <div class="ad-card" style="padding: 10px 14px; margin-bottom: 12px;">
    <span class="ad-faint" style="font-size: 12px;">Computing context fill…</span>
  </div>
{:else}
  <div class="ad-card token-savings" style="padding: 12px 14px; margin-bottom: 12px;">
    <!-- (a) Fill bar — always rendered when usage is non-null -->
    <div class="ts-bar-head">
      <span class="ts-bar-head-l">
        <strong class="ad-mono ad-tnum" style="color: {fillColor(fillPct)};">
          Session {fillPct.toFixed(0)}% full
        </strong>
      </span>
      <span class="ts-bar-head-r ad-mono ad-tnum">
        {#if calibrated}
          Next turn ≈ {fmtPct(usage.next_turn_pct_5h)} of your 5h limit
        {:else}
          <span title="We'll show this after some recent CLI activity (no calibration data yet).">
            Next turn ≈ —
          </span>
        {/if}
      </span>
    </div>

    <div class="ts-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={fillPct}>
      <div
        class="ts-bar-fill"
        style:width="{fillPct}%"
        style:background={fillColor(fillPct)}
      ></div>
    </div>

    <div class="ts-bar-meta ad-mono">
      {#if usage.context_window > 0}
        {kfmt(usage.context_used)} of {kfmt(usage.context_window)}
      {:else}
        {kfmt(usage.context_used)} tokens
      {/if}
      {#if usage.model}
        · <span class="ad-faint">{usage.model}</span>
      {:else}
        · <span class="ad-faint">unknown model</span>
      {/if}
    </div>

    <!-- (b) Compact CTA — only when fill >= 50 AND we have calibrated numbers -->
    {#if showCompactCta && usage}
      <div class="ts-cta">
        <div class="ts-cta-head">
          <span class="ts-cta-icon" aria-hidden="true">⚡</span>
          <span class="ts-cta-title">
            Compact now → next turn ≈
            <strong class="ad-mono ad-tnum">{fmtPct(usage.compacted_next_turn_pct_5h)}</strong>
            of your 5h limit
          </span>
        </div>
        <div class="ts-cta-body">
          Saves ~<strong class="ad-mono ad-tnum">{fmtPct(usage.compact_savings_pct_5h)}</strong>
          per future turn. Keep working — no cache rehydration cost.
        </div>
        <div class="ts-cta-actions">
          <button
            class="ad-btn ad-btn--primary ad-btn--sm"
            onclick={copyCompact}
            title="Copy /compact to clipboard, then paste into your CLI"
          >
            {copyFeedback ? 'copied!' : '⎘ copy /compact'}
          </button>
          <button
            class="ad-btn ad-btn--ghost ad-btn--sm"
            onclick={copyStartFresh}
            title={`Copy: ${startFreshCommand()}`}
          >
            {copyFreshFeedback ? 'copied!' : 'Start fresh instead'}
          </button>
        </div>
      </div>
    {/if}

    <!-- (c) Break advisor — only when fill >= 60 -->
    {#if showBreakAdvisor}
      <div class="ts-advisor">
        {#if advice === null && !loadingAdvice && !adviceError}
          <button
            class="ad-btn ad-btn--ghost ad-btn--sm"
            onclick={() => void loadAdvice(sessionId)}
          >
            🤔 Should I start a fresh session?
          </button>
        {:else if loadingAdvice}
          <span class="ad-faint" style="font-size: 12px;">Asking…</span>
        {:else if adviceError}
          <span class="ad-faint" style="font-size: 12px;">
            Couldn't fetch advice — keep going
          </span>
          <button
            class="ad-btn ad-btn--ghost ad-btn--sm ts-advisor-refresh"
            onclick={refreshAdvice}
          >
            retry
          </button>
        {:else if advice}
          <div class="ts-advice-card">
            <div class="ts-advice-head">
              <span class={verdictBadgeClass(advice.verdict)}>
                {verdictLabel(advice.verdict)}
              </span>
              <button
                class="ad-btn ad-btn--ghost ad-btn--sm ts-advisor-refresh"
                onclick={refreshAdvice}
                title="Re-run the advisor"
              >
                refresh
              </button>
            </div>
            <div class="ts-advice-reason">{advice.reason}</div>
            {#if advice.verdict === 'start_fresh' && advice.suggested_topic}
              <div class="ts-advice-topic">
                Topic: <span class="ad-mono">"{advice.suggested_topic}"</span>
              </div>
            {/if}
            {#if advice.verdict !== 'unavailable' && (advice.provider || advice.model)}
              <div class="ts-advice-foot ad-faint">
                powered by {advice.provider ?? '?'}{advice.model ? `/${advice.model}` : ''}
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .token-savings {
    /* card already supplied by .ad-card */
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .ts-bar-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 12px;
    font-size: 12px;
    flex-wrap: wrap;
  }
  .ts-bar-head-l strong {
    font-weight: 600;
  }
  .ts-bar-head-r {
    color: var(--ad-fg-2);
    font-size: 11px;
  }
  .ts-bar {
    height: 6px;
    background: var(--ad-bg-2);
    border-radius: 3px;
    overflow: hidden;
  }
  .ts-bar-fill {
    height: 100%;
    transition: width 240ms ease-out, background 240ms ease-out;
  }
  .ts-bar-meta {
    font-size: 10px;
    color: var(--ad-muted);
  }

  /* Compact CTA */
  .ts-cta {
    margin-top: 8px;
    background: var(--ad-compact-bg);
    border: 1px solid color-mix(in oklch, var(--ad-compact) 40%, transparent);
    border-radius: var(--ad-r-sm);
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .ts-cta-head {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--ad-fg);
  }
  .ts-cta-icon {
    font-size: 14px;
  }
  .ts-cta-title strong {
    color: var(--ad-compact);
  }
  .ts-cta-body {
    font-size: 12px;
    color: var(--ad-fg-2);
    line-height: 1.4;
  }
  .ts-cta-body strong {
    color: var(--ad-compact);
  }
  .ts-cta-actions {
    display: flex;
    gap: 8px;
    margin-top: 2px;
  }

  /* Break advisor */
  .ts-advisor {
    margin-top: 8px;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .ts-advice-card {
    flex: 1;
    min-width: 0;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
    padding: 8px 10px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .ts-advice-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .ts-advice-reason {
    font-size: 12px;
    color: var(--ad-fg);
    line-height: 1.45;
  }
  .ts-advice-topic {
    font-size: 11px;
    color: var(--ad-fg-2);
  }
  .ts-advice-foot {
    font-size: 10px;
    margin-top: 2px;
  }
  .ts-advisor-refresh {
    font-size: 10px;
  }
</style>
