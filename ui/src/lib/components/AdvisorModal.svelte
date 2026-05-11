<script lang="ts">
  /**
   * AdvisorModal — per-session advisory detail with PROOFS.
   *
   * Opens from the cockpit tile's "ⓘ" button. Renders every advisory
   * klyne has fired for the given session alongside the live proof
   * data that justifies each trigger — so the user can see not just
   * "klyne says ~66% stale" but exactly which files contribute, what
   * the relevance scores are, and how the acceleration window
   * compares against the prior window.
   *
   * Fetches on mount. Closes via Esc or backdrop click.
   */
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchAdvisorDetail } from '$lib/api.js';
  import { relTime, kfmt } from '$lib/format.js';
  import type {
    AccelerationProof,
    AdvisorDetailResponse,
    AdvisoryKind,
    AdvisoryRow,
    ContextWindowProof,
    StaleProof
  } from '$lib/types.js';

  interface Props {
    sessionId: string;
    onClose: () => void;
  }
  const { sessionId, onClose }: Props = $props();

  let detail = $state<AdvisorDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  /** kind → label + colour. Mirrors /advisors page so the cockpit
   *  vocabulary stays consistent across surfaces. */
  const kindMeta: Record<AdvisoryKind, { label: string; color: string }> = {
    stale: { label: 'stale context', color: '#f59e0b' },
    acceleration: { label: 'acceleration', color: '#fb923c' },
    hard_ceiling: { label: 'hard ceiling', color: '#ef4444' },
    window_50: { label: '5-h window 50%', color: '#eab308' },
    window_75: { label: '5-h window 75%', color: '#ef4444' },
    unknown: { label: 'other', color: '#6b7280' }
  };

  async function load(): Promise<void> {
    loading = true;
    try {
      detail = await fetchAdvisorDetail(sessionId);
      error = null;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : 'failed to load advisor detail';
      detail = null;
    } finally {
      loading = false;
    }
  }

  function onWindowKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    }
  }

  onMount(() => {
    void load();
    window.addEventListener('keydown', onWindowKey);
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onWindowKey);
  });

  /** Group advisories by kind so the modal shows one section per
   *  trigger — easier to scan than a flat chronological list when
   *  the same trigger has fired multiple times. */
  const advisoriesByKind = $derived.by(() => {
    const groups: Record<string, AdvisoryRow[]> = {};
    for (const a of detail?.advisories ?? []) {
      (groups[a.kind] ??= []).push(a);
    }
    return groups;
  });

  /** Sort the kind sections so most-active triggers float to the top. */
  const orderedKinds = $derived.by(() => {
    const known = Object.keys(advisoriesByKind);
    return known.sort((a, b) => advisoriesByKind[b].length - advisoriesByKind[a].length);
  });

  function shortSession(id: string): string {
    return id.length > 8 ? id.slice(0, 8) : id;
  }

  /** filesExpanded — when false the file table caps at FILES_COLLAPSED_LIMIT
   *  rows and renders an "...and N more (show all)" link below; when true
   *  every file is rendered. Toggling is purely local UI state; the server
   *  already returns the full file list. */
  let filesExpanded = $state(false);
  const FILES_COLLAPSED_LIMIT = 12;

  // ---- Plain-English summaries for each proof section ----
  //
  // Each helper takes the live proof DTO and returns a single sentence
  // that a non-technical user can read without consulting the stats
  // grid below it. The goal is "read the sentence, understand what's
  // happening" — the grid stays for power users who want raw numbers.

  /** staleSummary — one-line plain-English summary for the loaded-file
   *  relevance panel. */
  function staleSummary(d: StaleProof): string {
    if (d.files.length === 0) {
      return 'No files have been loaded into this session yet.';
    }
    const total = kfmt(d.total_bytes);
    const stale = kfmt(d.stale_bytes);
    const pct = Math.round(d.stale_share * 100);
    if (pct === 0) {
      return `All ${total}B of files Claude has loaded match what you're working on. Healthy.`;
    }
    if (d.stale_share > d.threshold) {
      return `${pct}% of loaded files (${stale}B of ${total}B) aren't in your recent rotation — Claude is carrying weight it isn't using. Above the alert threshold.`;
    }
    return `${pct}% of loaded files (${stale}B of ${total}B) aren't in your recent rotation, but that's below the alert threshold.`;
  }

  /** accelSummary — one-line plain-English summary for the per-turn
   *  cost trend panel. Compares recent vs prior in human terms
   *  ("3× cheaper", "stable", "trending up"). */
  function accelSummary(d: AccelerationProof): string {
    if (d.sampled_turns < 8) {
      return `Not enough data yet — Claude has only replied ${d.sampled_turns} times in this session.`;
    }
    const recent = kfmt(d.recent_mean);
    const prior = kfmt(d.prior_mean);
    const r = d.ratio;
    if (!isFinite(r) || r === 0) {
      return `Recent replies look very small compared to earlier ones — not enough signal to draw a conclusion.`;
    }
    if (r < 0.5) {
      const factor = (1 / r).toFixed(1);
      return `Your recent replies cost about ${factor}× less than earlier ones (avg ${recent} new tokens vs ${prior}). Healthy — context isn't bloating.`;
    }
    if (r < 1.0) {
      return `Recent replies are slightly cheaper than earlier ones (avg ${recent} new tokens vs ${prior}). Healthy.`;
    }
    if (r < 1.5) {
      return `Per-reply cost is roughly stable (recent avg ${recent}, earlier avg ${prior}). Healthy.`;
    }
    if (r < 2.0) {
      return `Per-reply cost is trending up — recent ${recent} vs earlier ${prior} (${r.toFixed(2)}× more). Watch this. If it crosses 2× the advisor will recommend a fresh session.`;
    }
    return `Recent replies cost ${r.toFixed(2)}× more than earlier ones (avg ${recent} new tokens vs ${prior}). Context is bloating — consider a handoff.`;
  }

  /** ctxSummary — one-line plain-English summary for the context-window
   *  fill panel. */
  function ctxSummary(d: ContextWindowProof): string {
    const pct = Math.round(d.fill_pct);
    const used = kfmt(d.latest_input);
    const max = kfmt(d.context_window);
    if (pct >= 75) {
      return `Claude's memory is ${pct}% full (${used} of ${max} tokens). Above ${d.threshold}% the next turn's costs balloon, so the advisor recommends a fresh session.`;
    }
    if (pct >= 50) {
      return `Claude's memory is ${pct}% full (${used} of ${max} tokens). Still healthy, but watch the trend.`;
    }
    return `Claude's memory is ${pct}% full (${used} of ${max} tokens). Plenty of headroom.`;
  }

  /** wouldFireNow — true when the LIVE proof for the given trigger
   *  would currently trip it. Returns false when conditions have
   *  changed since the historical fire (e.g. the user's recent
   *  prompts now overlap the loaded files again, or the per-turn
   *  cost has settled back down). Decoupling the historical fact
   *  ("this fired 1× an hour ago") from the live verdict ("would
   *  it fire now?") prevents the modal from looking like an active
   *  alert when the underlying condition has resolved. */
  function wouldFireNow(kind: AdvisoryKind, d: AdvisorDetailResponse): boolean {
    switch (kind) {
      case 'stale':
        return d.stale.stale_share > d.stale.threshold;
      case 'acceleration':
        return d.acceleration.would_fire;
      case 'hard_ceiling':
        return d.context_window.would_fire;
      case 'window_50':
        return (d.five_hour?.pct_used ?? 0) >= 50;
      case 'window_75':
        return (d.five_hour?.pct_used ?? 0) >= 75;
      default:
        return false;
    }
  }
</script>

<!-- Backdrop. Click closes. -->
<div
  role="presentation"
  onclick={onClose}
  style="
    position: fixed; inset: 0;
    background: color-mix(in oklch, var(--ad-bg) 70%, transparent);
    backdrop-filter: blur(2px);
    z-index: 100;
    display: flex; align-items: center; justify-content: center;
    padding: 24px;
  "
>
  <!-- Modal body: don't bubble click to the backdrop. -->
  <div
    role="dialog"
    aria-modal="true"
    aria-label="Advisor detail"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    onkeydown={(e) => e.stopPropagation()}
    style="
      width: min(960px, 100%);
      max-height: 90vh; overflow-y: auto;
      background: var(--ad-bg-2);
      border: 1px solid var(--ad-border);
      border-radius: 10px;
      padding: 20px 24px;
      box-shadow: 0 24px 60px rgba(0,0,0,0.45);
    "
  >
    <!-- Header -->
    <div style="display: flex; justify-content: space-between; align-items: start; gap: 12px; margin-bottom: 12px;">
      <div>
        <h2 style="margin: 0 0 4px 0; font-size: 18px; letter-spacing: -0.01em;">
          Advisor detail — <span class="ad-mono">{shortSession(sessionId)}</span>
        </h2>
        <p style="margin: 0; color: var(--ad-muted); font-size: 13px;">
          klyne's advisories for this session, with the underlying proof for each trigger.
        </p>
      </div>
      <button
        class="ad-btn ad-btn--ghost"
        onclick={onClose}
        aria-label="Close advisor detail"
        style="font-size: 14px;"
      >✕</button>
    </div>

    {#if loading}
      <div style="color: var(--ad-muted); padding: 24px 0;">Loading detail…</div>
    {:else if error}
      <div style="color: var(--ad-red, #ef4444); padding: 12px; background: rgba(239,68,68,0.08); border-radius: 6px;">
        {error}
      </div>
    {:else if detail}
      <!-- Advisories list, grouped by kind. -->
      {#if (detail.advisories?.length ?? 0) === 0}
        <div style="border: 1px dashed var(--ad-border); padding: 16px; border-radius: 6px; color: var(--ad-muted); margin-bottom: 16px;">
          No advisories have fired yet for this session. Klyne's live signals are shown below — you can use them to judge whether the advisor's thresholds would soon trigger.
        </div>
      {:else}
        <div style="display: flex; flex-direction: column; gap: 16px; margin-bottom: 24px;">
          {#each orderedKinds as kind (kind)}
            {@const meta = kindMeta[kind as AdvisoryKind] ?? kindMeta.unknown}
            {@const rows = advisoriesByKind[kind]}
            {@const stillFiring = wouldFireNow(kind as AdvisoryKind, detail)}
            <section style="border: 1px solid var(--ad-border); border-left: 3px solid {stillFiring ? meta.color : 'var(--ad-border)'}; border-radius: 6px; padding: 12px 14px; background: var(--ad-panel); opacity: {stillFiring ? 1 : 0.72};">
              <header style="display: flex; align-items: baseline; gap: 8px; margin-bottom: 8px;">
                <span style="text-transform: uppercase; font-size: 11px; letter-spacing: 0.04em; color: {stillFiring ? meta.color : 'var(--ad-muted)'}; font-weight: 600;">
                  {meta.label}
                </span>
                <span style="color: var(--ad-muted); font-size: 12px;">
                  fired {rows.length}× in this session
                </span>
                {#if !stillFiring}
                  <span style="color: var(--ad-active, #10b981); font-size: 11px; font-weight: 600; margin-left: auto;">
                    ✓ resolved — would not fire now
                  </span>
                {:else}
                  <span style="color: {meta.color}; font-size: 11px; font-weight: 600; margin-left: auto;">
                    currently active
                  </span>
                {/if}
              </header>
              <ul style="margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 8px;">
                {#each rows as adv (adv.message_id)}
                  <li style="font-size: 13px; line-height: 1.5;">
                    <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;">
                      {relTime(adv.ts)}
                    </div>
                    <div style="color: var(--ad-fg);">{adv.content}</div>
                  </li>
                {/each}
              </ul>
            </section>
          {/each}
        </div>
      {/if}

      <!-- Proof section: always shown, regardless of whether the
           advisor has actually fired yet. This lets users see the
           live signals klyne is evaluating against. -->
      <h3 style="margin: 0 0 10px 0; font-size: 14px; font-weight: 600; letter-spacing: -0.01em; color: var(--ad-muted); text-transform: uppercase;">
        Proof — what klyne is seeing right now
      </h3>

      <!-- Stale-context proof: per-file relevance table. -->
      <section style="border: 1px solid var(--ad-border); border-radius: 6px; padding: 12px 14px; margin-bottom: 12px;">
        <header style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 6px;">
          <div>
            <strong style="font-size: 13px;">Files in Claude's context</strong>
            <div style="color: var(--ad-muted); font-size: 11px; margin-top: 1px;">
              What Claude has loaded vs what's still in your active rotation
            </div>
          </div>
          <span style="color: var(--ad-muted); font-size: 12px;">
            {Math.round(detail.stale.stale_share * 100)}% of {kfmt(detail.stale.total_bytes)}B stale
            {#if detail.stale.stale_share > detail.stale.threshold}
              <span style="color: {kindMeta.stale.color}; margin-left: 6px;">(triggers alert)</span>
            {/if}
          </span>
        </header>
        <p style="margin: 4px 0 10px 0; font-size: 12.5px; line-height: 1.5; color: var(--ad-fg);">
          {staleSummary(detail.stale)}
        </p>
        {#if detail.stale.files.length === 0}
          <p style="margin: 0; color: var(--ad-muted); font-size: 12px;">No files loaded into this session yet.</p>
        {:else}
          <table style="width: 100%; border-collapse: collapse; font-size: 12px;">
            <thead style="color: var(--ad-muted); text-align: left;">
              <tr>
                <th style="padding: 4px 8px 4px 0;">file</th>
                <th style="padding: 4px 8px; text-align: right;">bytes</th>
                <th style="padding: 4px 8px; text-align: right;">relevance</th>
                <th style="padding: 4px 0; text-align: right;">status</th>
              </tr>
            </thead>
            <tbody>
              {#each (filesExpanded ? detail.stale.files : detail.stale.files.slice(0, FILES_COLLAPSED_LIMIT)) as f (f.path)}
                <tr style="border-top: 1px solid var(--ad-border-soft);">
                  <td style="padding: 4px 8px 4px 0; font-family: var(--ad-font-mono);" title={f.path}>{f.basename}</td>
                  <td style="padding: 4px 8px; text-align: right;">{kfmt(f.bytes)}</td>
                  <td style="padding: 4px 8px; text-align: right;">{f.score.toFixed(2)}</td>
                  <td style="padding: 4px 0; text-align: right; color: {f.stale ? kindMeta.stale.color : 'var(--ad-active, #10b981)'};">
                    {f.stale ? 'stale' : 'relevant'}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
          {#if detail.stale.files.length > FILES_COLLAPSED_LIMIT}
            <button
              type="button"
              onclick={() => (filesExpanded = !filesExpanded)}
              style="
                margin: 6px 0 0;
                padding: 0;
                background: none;
                border: none;
                color: var(--ad-muted);
                font-size: 11px;
                cursor: pointer;
                text-decoration: underline;
                text-underline-offset: 2px;
              "
            >
              {filesExpanded
                ? 'show fewer files'
                : `…and ${detail.stale.files.length - FILES_COLLAPSED_LIMIT} more files (show all)`}
            </button>
          {/if}
        {/if}
      </section>

      <!-- Acceleration proof: recent-vs-prior window math. -->
      <section style="border: 1px solid var(--ad-border); border-radius: 6px; padding: 12px 14px; margin-bottom: 12px;">
        <header style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 6px;">
          <div>
            <strong style="font-size: 13px;">Per-reply cost trend</strong>
            <div style="color: var(--ad-muted); font-size: 11px; margin-top: 1px;">
              Is each of Claude's replies getting more expensive than the one before?
            </div>
          </div>
          <span style="color: var(--ad-muted); font-size: 12px;">
            based on {detail.acceleration.sampled_turns} of Claude's replies
            {#if detail.acceleration.would_fire}
              <span style="color: {kindMeta.acceleration.color}; margin-left: 6px;">(triggers alert)</span>
            {/if}
          </span>
        </header>
        <p style="margin: 4px 0 10px 0; font-size: 12.5px; line-height: 1.5; color: var(--ad-fg);">
          {accelSummary(detail.acceleration)}
        </p>
        <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 10px;">
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="Average new (uncached) tokens across the 5 Claude replies before the most recent 3.">earlier 5 replies (avg)</div>
            <div style="font-family: var(--ad-font-mono); font-size: 16px;">{kfmt(detail.acceleration.prior_mean)}</div>
          </div>
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="Average new (uncached) tokens across Claude's most recent 3 replies.">last 3 replies (avg)</div>
            <div style="font-family: var(--ad-font-mono); font-size: 16px;">{kfmt(detail.acceleration.recent_mean)}</div>
          </div>
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="Last 3 average ÷ earlier 5 average. Below 1.0 means cheaper than before; ≥ 2.0 triggers the alert.">trend (last 3 vs earlier 5)</div>
            <div style="font-family: var(--ad-font-mono); font-size: 16px; color: {detail.acceleration.ratio >= 2 ? kindMeta.acceleration.color : 'var(--ad-fg)'};">
              {detail.acceleration.ratio.toFixed(2)}×
            </div>
          </div>
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="New (uncached) tokens in Claude's most recent reply.">last reply (new tokens)</div>
            <div style="font-family: var(--ad-font-mono); font-size: 16px;">{kfmt(detail.acceleration.latest_effective)}</div>
          </div>
        </div>
        <p style="margin: 10px 0 0 0; color: var(--ad-muted); font-size: 11px; line-height: 1.5;">
          A “reply” is one of Claude's turns in the session (reading a file, making an edit, or sending text). “New tokens” means tokens Claude had to read fresh — anything served from the prompt cache is excluded.
          The alert triggers when the last 3 replies average ≥ 2× the earlier 5 AND the most recent reply alone costs ≥ 5K new tokens.
        </p>
      </section>

      <!-- Hard-ceiling proof: current fill state. -->
      <section style="border: 1px solid var(--ad-border); border-radius: 6px; padding: 12px 14px; margin-bottom: 12px;">
        <header style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 6px;">
          <div>
            <strong style="font-size: 13px;">How full is Claude's memory</strong>
            <div style="color: var(--ad-muted); font-size: 11px; margin-top: 1px;">
              Claude can only hold so much before each new turn balloons in cost
            </div>
          </div>
          <span style="color: var(--ad-muted); font-size: 12px;">
            alert at {detail.context_window.threshold}%
            {#if detail.context_window.would_fire}
              <span style="color: {kindMeta.hard_ceiling.color}; margin-left: 6px;">(triggers alert)</span>
            {/if}
          </span>
        </header>
        <p style="margin: 4px 0 10px 0; font-size: 12.5px; line-height: 1.5; color: var(--ad-fg);">
          {ctxSummary(detail.context_window)}
        </p>
        <div style="display: flex; gap: 24px; align-items: baseline;">
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="Percent of Claude's context window currently in use by the last reply's prefix.">% used</div>
            <div style="font-family: var(--ad-font-mono); font-size: 22px; color: {detail.context_window.would_fire ? kindMeta.hard_ceiling.color : 'var(--ad-fg)'};">
              {detail.context_window.fill_pct.toFixed(0)}%
            </div>
          </div>
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;" title="Tokens currently loaded into the prefix vs the model's maximum context window.">tokens used / max</div>
            <div style="font-family: var(--ad-font-mono); font-size: 16px;">{kfmt(detail.context_window.latest_input)} / {kfmt(detail.context_window.context_window)}</div>
          </div>
          <div>
            <div style="color: var(--ad-muted); font-size: 11px; margin-bottom: 2px;">model</div>
            <div style="font-family: var(--ad-font-mono); font-size: 13px;">{detail.context_window.model || '—'}</div>
          </div>
        </div>
        <!-- Fill bar -->
        <div style="margin-top: 10px; height: 6px; background: var(--ad-bg); border-radius: 3px; overflow: hidden;">
          <div style="height: 100%; width: {Math.min(100, detail.context_window.fill_pct)}%; background: {detail.context_window.would_fire ? kindMeta.hard_ceiling.color : 'var(--ad-active, #10b981)'};"></div>
        </div>
      </section>

      <!-- Footer actions -->
      <div style="display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px;">
        <button class="ad-btn ad-btn--ghost" onclick={() => goto(`/sessions/${encodeURIComponent(sessionId)}`)}>
          Open session →
        </button>
        <button class="ad-btn" onclick={onClose}>Close</button>
      </div>
    {/if}
  </div>
</div>
