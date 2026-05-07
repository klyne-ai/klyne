<script lang="ts">
  import type { CostSummaryResponse } from '$lib/types.js';

  interface Props {
    data?: CostSummaryResponse | null;
    loading?: boolean;
    error?: string | null;
  }

  const { data = null, loading = false, error = null }: Props = $props();

  function formatUsd(val: number): string {
    if (val < 0.01) return `$${val.toFixed(4)}`;
    return `$${val.toFixed(2)}`;
  }

  function formatNumber(val: number): string {
    return val.toLocaleString();
  }
</script>

<aside
  data-testid="cost-panel"
  class="flex flex-col gap-2 rounded-lg border border-gray-700 bg-gray-900 p-4 text-sm"
>
  <h2 class="font-semibold text-gray-200 text-xs uppercase tracking-wide">Cost Summary</h2>

  {#if loading}
    <!-- Loading skeleton -->
    <div data-testid="cost-loading" class="flex flex-col gap-2 animate-pulse">
      {#each [1, 2, 3] as _}
        <div class="h-4 bg-gray-700 rounded w-full"></div>
      {/each}
    </div>

  {:else if error}
    <!-- Error state -->
    <div data-testid="cost-error" class="text-red-400 text-xs">{error}</div>

  {:else if !data || data.buckets.length === 0}
    <!-- Empty state -->
    <p data-testid="cost-empty" class="text-gray-500 text-xs">
      No cost data available yet.
    </p>

  {:else}
    <!-- Summary totals -->
    <div class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
      <span class="text-gray-500">Total cost</span>
      <span class="text-right font-mono text-green-400" data-testid="cost-total">
        {formatUsd(data.total.cost_usd)}
      </span>

      <span class="text-gray-500">Tokens in</span>
      <span class="text-right font-mono text-gray-300">
        {formatNumber(data.total.tokens_in)}
      </span>

      <span class="text-gray-500">Tokens out</span>
      <span class="text-right font-mono text-gray-300">
        {formatNumber(data.total.tokens_out)}
      </span>

      <span class="text-gray-500">Messages</span>
      <span class="text-right font-mono text-gray-300">
        {formatNumber(data.total.count)}
      </span>
    </div>

    {#if data.buckets.length > 0}
      <div class="mt-2 border-t border-gray-700/50 pt-2">
        <p class="text-gray-500 text-xs mb-1 uppercase tracking-wide">By {data.group}</p>
        <ul class="flex flex-col gap-1 max-h-48 overflow-y-auto">
          {#each data.buckets as bucket (bucket.key)}
            <li class="flex items-center justify-between text-xs">
              <span class="text-gray-400 truncate flex-1 mr-2" title={bucket.key}>
                {bucket.key}
              </span>
              <span class="font-mono text-gray-300 shrink-0">{formatUsd(bucket.cost_usd)}</span>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {/if}
</aside>
