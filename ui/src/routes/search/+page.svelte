<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { search as apiSearch } from '$lib/api.js';
  import type { SearchHit } from '$lib/types.js';
  import SearchBar from '$lib/components/SearchBar.svelte';

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  // Keep a reactive ref to the URL query param
  let urlQuery = $derived($page.url.searchParams.get('q') ?? '');

  let results = $state<SearchHit[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let searchBarValue = $state(urlQuery);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  const DEBOUNCE_MS = 300;

  // ---------------------------------------------------------------------------
  // Search
  // ---------------------------------------------------------------------------

  async function doSearch(q: string): Promise<void> {
    if (!q.trim()) {
      results = [];
      return;
    }
    loading = true;
    error = null;
    try {
      const res = await apiSearch(q, 50);
      results = res.hits;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Search failed';
      results = [];
    } finally {
      loading = false;
    }
  }

  function handleSearch(query: string): void {
    searchBarValue = query;

    if (debounceTimer !== null) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      // Update URL param
      const url = new URL(window.location.href);
      if (query.trim()) {
        url.searchParams.set('q', query);
      } else {
        url.searchParams.delete('q');
      }
      void goto(url.pathname + url.search, { replaceState: true, keepFocus: true });
      void doSearch(query);
    }, DEBOUNCE_MS);
  }

  function openSession(hit: SearchHit): void {
    const url = `/sessions/${encodeURIComponent(hit.session_id)}?msg=${encodeURIComponent(hit.message_id)}`;
    void goto(url);
  }

  function formatScore(score: number): string {
    return score.toFixed(2);
  }

  function relativeTime(tsMs: number): string {
    const diff = Date.now() - tsMs;
    const s = Math.floor(diff / 1000);
    if (s < 60) return `${s}s ago`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    const d = Math.floor(h / 24);
    return `${d}d ago`;
  }

  function projectName(path: string): string {
    return path.split('/').filter(Boolean).pop() ?? path;
  }

  // ---------------------------------------------------------------------------
  // Lifecycle
  // ---------------------------------------------------------------------------

  onMount(() => {
    // Capture the initial URL query value at mount time (intentional — read once)
    const initialQuery = $page.url.searchParams.get('q') ?? '';
    if (initialQuery.trim()) {
      searchBarValue = initialQuery;
      void doSearch(initialQuery);
    }
  });
</script>

<svelte:head>
  <title>{searchBarValue ? `Search: ${searchBarValue}` : 'Search'} — agentdeck</title>
</svelte:head>

<div class="flex flex-col flex-1 overflow-hidden w-full">
  <!-- Header / search input -->
  <div class="flex items-center gap-3 px-4 py-3 border-b border-gray-800 shrink-0">
    <button
      type="button"
      aria-label="Back to dashboard"
      class="text-gray-500 hover:text-gray-300 transition-colors shrink-0"
      onclick={() => void goto('/')}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-4 w-4"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <polyline points="15 18 9 12 15 6" />
      </svg>
    </button>

    <div class="flex-1">
      <SearchBar
        bind:value={searchBarValue}
        placeholder="Search all messages…"
        debounceMs={300}
        onsearch={handleSearch}
      />
    </div>
  </div>

  <!-- Results area -->
  <div class="flex-1 overflow-y-auto p-4">
    {#if loading}
      <!-- Skeleton -->
      <ul data-testid="search-loading" class="flex flex-col gap-3 animate-pulse">
        {#each [1, 2, 3, 4] as _}
          <li class="rounded-lg bg-gray-800 p-4">
            <div class="h-3 bg-gray-700 rounded w-1/4 mb-2"></div>
            <div class="h-3 bg-gray-700 rounded w-full mb-1"></div>
            <div class="h-3 bg-gray-700 rounded w-3/4"></div>
          </li>
        {/each}
      </ul>

    {:else if error}
      <div data-testid="search-error" class="flex flex-col items-center gap-3 py-12 text-center">
        <p class="text-red-400">{error}</p>
        <button
          type="button"
          class="rounded-lg bg-gray-800 px-4 py-2 text-sm text-gray-200 hover:bg-gray-700 transition-colors"
          onclick={() => void doSearch(searchBarValue)}
        >
          Retry
        </button>
      </div>

    {:else if !searchBarValue.trim()}
      <!-- No query yet -->
      <div data-testid="search-prompt" class="flex flex-col items-center gap-2 py-12 text-center">
        <p class="text-gray-400">Type to search across all messages.</p>
      </div>

    {:else if results.length === 0}
      <!-- No results -->
      <div data-testid="search-empty" class="flex flex-col items-center gap-2 py-12 text-center">
        <p class="text-gray-400">No results for <strong class="text-gray-200">"{searchBarValue}"</strong></p>
        <p class="text-gray-600 text-sm">Try different keywords or check spelling.</p>
      </div>

    {:else}
      <!-- Results list -->
      <div class="mb-3 text-xs text-gray-500">
        {results.length} result{results.length !== 1 ? 's' : ''} for
        <strong class="text-gray-300">"{urlQuery}"</strong>
      </div>

      <ul data-testid="search-results" class="flex flex-col gap-2">
        {#each results as hit (hit.message_id)}
          <li>
            <button
              type="button"
              data-testid="search-result-item"
              class="w-full text-left rounded-lg border border-gray-700 bg-gray-800 p-4
                     hover:border-blue-600 transition-colors"
              onclick={() => openSession(hit)}
            >
              <!-- Meta row -->
              <div class="flex items-center gap-2 mb-2 text-xs text-gray-500">
                <span class="font-medium text-gray-300">{projectName(hit.project_path)}</span>
                <span>·</span>
                <span class="capitalize">{hit.role}</span>
                <span>·</span>
                <span>{relativeTime(hit.ts)}</span>
                <span class="ml-auto">score {formatScore(hit.score)}</span>
              </div>

              <!-- Snippet -->
              <p class="text-sm text-gray-200 line-clamp-3 whitespace-pre-wrap break-words">
                {hit.snippet}
              </p>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
