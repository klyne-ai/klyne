<!--
  Productivity dashboard — composes the four productivity components
  (SummaryBar, RiskPanel, TimeBarChart, ServiceCard) over the
  deterministic /productivity report. Real UI; the prior "dirt"
  prototype is retired.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchProductivity, type ProductivityReport } from '$lib/api.js';
  import SummaryBar from '$lib/components/productivity/SummaryBar.svelte';
  import RiskPanel from '$lib/components/productivity/RiskPanel.svelte';
  import TimeBarChart from '$lib/components/productivity/TimeBarChart.svelte';
  import ServiceCard from '$lib/components/productivity/ServiceCard.svelte';

  let rep = $state<ProductivityReport | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load(): Promise<void> {
    loading = true;
    try {
      // Optional ?since=&until= epoch-ms window; absent → server
      // defaults to today local 00:00 → now.
      const q = new URLSearchParams(window.location.search);
      const since = q.get('since') ? Number(q.get('since')) : undefined;
      const until = q.get('until') ? Number(q.get('until')) : undefined;
      rep = await fetchProductivity(since, until);
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load productivity';
    } finally {
      loading = false;
    }
  }

  onMount(() => { void load(); });
</script>

<div class="prod-page">
  {#if loading}
    <p class="prod-state">Loading…</p>
  {:else if error}
    <p class="prod-state prod-state--err">Error: {error}</p>
  {:else if rep}
    <SummaryBar report={rep} />

    <div class="prod-row">
      <RiskPanel report={rep} />
      <TimeBarChart report={rep} />
    </div>

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
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(420px, 1fr));
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
