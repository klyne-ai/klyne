<!--
  Productivity dashboard — composes the productivity components over the
  deterministic /productivity report.

  The selected date range follows a clear precedence:
    1. URL query (?since=&until=)        — shareable links win
    2. localStorage saved range          — persists across reloads
    3. Default: yesterday's full day     — sensible end-of-day view

  Whenever the user changes the range we mirror it to BOTH localStorage
  (for next reload) and the URL via replaceState (so the current URL
  stays shareable). The explicit Refresh button bypasses the backend
  caches (?refresh=1) without changing the range.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchProductivity, type ProductivityReport } from '$lib/api.js';
  import RangeBar from '$lib/components/productivity/RangeBar.svelte';
  import SummaryBar from '$lib/components/productivity/SummaryBar.svelte';
  import DaySummary from '$lib/components/productivity/DaySummary.svelte';
  import RiskPanel from '$lib/components/productivity/RiskPanel.svelte';
  import TimeBarChart from '$lib/components/productivity/TimeBarChart.svelte';
  import ProofOfWork from '$lib/components/productivity/ProofOfWork.svelte';
  import ServiceCard from '$lib/components/productivity/ServiceCard.svelte';

  const STORAGE_KEY = 'klyne.productivity.range';

  let rep = $state<ProductivityReport | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let loadedAt = $state<number | null>(null);

  // Selected window in epoch-ms. Initialised in onMount via resolveRange.
  let since = $state<number>(0);
  let until = $state<number>(0);

  // ---- range resolution + persistence ------------------------------------

  function yesterdayRange(): { since: number; until: number } {
    const now = new Date();
    const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    return { since: midnight - 24 * 60 * 60 * 1000, until: midnight - 1 };
  }

  function loadSavedRange(): { since: number; until: number } | null {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (typeof parsed?.since === 'number' && typeof parsed?.until === 'number') {
        return { since: parsed.since, until: parsed.until };
      }
    } catch {
      /* ignore corrupt entries */
    }
    return null;
  }

  function saveRange(s: number, u: number): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ since: s, until: u }));
    } catch {
      /* private mode / quota full → silently degrade */
    }
  }

  /** URL ⊃ localStorage ⊃ yesterday */
  function resolveInitialRange(): { since: number; until: number } {
    const q = new URLSearchParams(window.location.search);
    const qSince = Number(q.get('since'));
    const qUntil = Number(q.get('until'));
    if (Number.isFinite(qSince) && qSince > 0 && Number.isFinite(qUntil) && qUntil > 0) {
      return { since: qSince, until: qUntil };
    }
    const saved = loadSavedRange();
    if (saved) return saved;
    return yesterdayRange();
  }

  /** Mirror the current range into the URL (shareable + reload-safe). */
  function syncUrl(s: number, u: number): void {
    const q = new URLSearchParams(window.location.search);
    q.set('since', String(s));
    q.set('until', String(u));
    const qs = q.toString();
    const next = `${window.location.pathname}${qs ? '?' + qs : ''}${window.location.hash}`;
    window.history.replaceState(null, '', next);
  }

  // ---- data fetch --------------------------------------------------------

  async function load(opts?: { refresh?: boolean }): Promise<void> {
    loading = true;
    try {
      rep = await fetchProductivity(since, until, opts?.refresh);
      loadedAt = Date.now();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load productivity';
    } finally {
      loading = false;
    }
  }

  // ---- range-change + refresh handlers (passed to RangeBar) -------------

  function handleRangeChange(newSince: number, newUntil: number): void {
    if (newSince === since && newUntil === until) return;
    since = newSince;
    until = newUntil;
    saveRange(since, until);
    syncUrl(since, until);
    void load();
  }

  function handleRefresh(): void {
    void load({ refresh: true });
  }

  onMount(() => {
    const r = resolveInitialRange();
    since = r.since;
    until = r.until;
    saveRange(since, until);
    syncUrl(since, until);
    void load();
  });
</script>

<div class="prod-page">
  {#if since > 0}
    <RangeBar
      {since}
      {until}
      {loading}
      {loadedAt}
      onChange={handleRangeChange}
      onRefresh={handleRefresh}
    />
  {/if}

  {#if loading && !rep}
    <p class="prod-state">Loading…</p>
  {:else if error}
    <p class="prod-state prod-state--err">Error: {error}</p>
  {:else if rep}
    <SummaryBar report={rep} />

    <DaySummary report={rep} />

    <div class="prod-row">
      <RiskPanel report={rep} />
      <TimeBarChart report={rep} />
    </div>

    <ProofOfWork report={rep} />

    {#if rep.services.length === 0}
      <p class="prod-state">No services in this window.</p>
    {:else}
      <div class="prod-grid">
        {#each rep.services as svc, si (svc.repo + '|' + si)}
          <ServiceCard service={svc} />
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .prod-page {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s4);
    padding: var(--ad-s5);
    max-width: 1180px;
    margin: 0 auto;
    width: 100%;
  }

  .prod-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--ad-s4);
    align-items: start;
  }

  .prod-grid {
    display: flex;
    flex-direction: column;
    gap: var(--ad-s4);
  }

  .prod-state {
    color: var(--ad-muted);
    font-size: var(--ad-fs-sm);
    padding: var(--ad-s5);
  }
  .prod-state--err {
    color: var(--ad-danger);
  }

  @media (max-width: 860px) {
    .prod-row {
      grid-template-columns: 1fr;
    }
  }
</style>
