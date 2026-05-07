<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchSessions, fetchCostSummary } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import {
    sessions,
    setSessions,
    onMsgNew,
    patchSession,
    costSummary,
    setCostSummary
  } from '$lib/stores.svelte.js';
  import type { Session } from '$lib/types.js';
  import SessionList from '$lib/components/SessionList.svelte';
  import SearchBar from '$lib/components/SearchBar.svelte';
  import CostPanel from '$lib/components/CostPanel.svelte';

  // ---------------------------------------------------------------------------
  // Local state
  // ---------------------------------------------------------------------------

  let loadError = $state<string | null>(null);
  let costError = $state<string | null>(null);
  let searchQuery = $state('');
  let highlightIndex = $state(-1);

  // ---------------------------------------------------------------------------
  // Data loading
  // ---------------------------------------------------------------------------

  async function loadSessions(): Promise<void> {
    sessions.loading = true;
    loadError = null;
    try {
      const res = await fetchSessions({ limit: 50 });
      setSessions(res.sessions, res.next_before);
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load sessions';
    } finally {
      sessions.loading = false;
    }
  }

  async function loadCostSummary(): Promise<void> {
    costSummary.loading = true;
    costError = null;
    try {
      const res = await fetchCostSummary({ group: 'session' });
      setCostSummary(res);
    } catch (e) {
      costError = e instanceof Error ? e.message : 'Failed to load cost data';
    } finally {
      costSummary.loading = false;
    }
  }

  // ---------------------------------------------------------------------------
  // SSE
  // ---------------------------------------------------------------------------

  let unsubscribe: (() => void) | null = null;

  // ---------------------------------------------------------------------------
  // Filtered sessions (local search filter)
  // ---------------------------------------------------------------------------

  const filteredSessions = $derived(
    searchQuery.trim()
      ? sessions.items.filter(
          (s) =>
            s.project_path.toLowerCase().includes(searchQuery.toLowerCase()) ||
            s.cli.includes(searchQuery.toLowerCase()) ||
            s.model.toLowerCase().includes(searchQuery.toLowerCase())
        )
      : sessions.items
  );

  // ---------------------------------------------------------------------------
  // Keyboard shortcuts
  // ---------------------------------------------------------------------------

  function handleWindowKeydown(e: KeyboardEvent): void {
    const tag = (e.target as HTMLElement).tagName.toLowerCase();
    const isInput = tag === 'input' || tag === 'textarea';

    if (!isInput) {
      if (e.key === 'j') {
        e.preventDefault();
        highlightIndex = Math.min(highlightIndex + 1, filteredSessions.length - 1);
        return;
      }
      if (e.key === 'k') {
        e.preventDefault();
        highlightIndex = Math.max(highlightIndex - 1, 0);
        return;
      }
      if (e.key === 'Enter' && highlightIndex >= 0) {
        e.preventDefault();
        const session = filteredSessions[highlightIndex];
        if (session) {
          void goto(`/sessions/${encodeURIComponent(session.id)}`);
        }
        return;
      }
      if (e.key === 'Escape') {
        highlightIndex = -1;
        searchQuery = '';
        return;
      }
    } else {
      if (e.key === 'Escape') {
        highlightIndex = -1;
        searchQuery = '';
      }
    }
  }

  function handleSearch(query: string): void {
    searchQuery = query;
    highlightIndex = -1;
  }

  function handleSessionSelect(session: Session): void {
    void goto(`/sessions/${encodeURIComponent(session.id)}`);
  }

  // ---------------------------------------------------------------------------
  // Lifecycle
  // ---------------------------------------------------------------------------

  onMount(() => {
    void loadSessions();
    void loadCostSummary();

    unsubscribe = subscribe({
      onMsgNew: (payload) => {
        onMsgNew(payload);
      },
      onSessionUpdate: (payload) => {
        patchSession({
          id: payload.session_id,
          last_msg_at: payload.last_msg_at,
          msg_count: payload.msg_count,
          cost_usd: payload.cost_usd,
          status: payload.status as 'active' | 'idle' | 'compacted'
        });
      }
    });

    window.addEventListener('keydown', handleWindowKeydown);
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
    window.removeEventListener('keydown', handleWindowKeydown);
  });
</script>

<svelte:head>
  <title>agentdeck — Sessions</title>
</svelte:head>

<div class="flex flex-1 overflow-hidden w-full">
  <!-- Left sidebar: session list -->
  <aside class="flex flex-col w-72 shrink-0 border-r border-gray-800 overflow-hidden">
    <!-- Search bar -->
    <div class="p-2 border-b border-gray-800">
      <SearchBar
        bind:value={searchQuery}
        onsearch={handleSearch}
      />
    </div>

    <!-- Session list -->
    <div class="flex-1 overflow-hidden">
      <SessionList
        sessions={filteredSessions}
        loading={sessions.loading}
        error={loadError}
        {highlightIndex}
        onselect={handleSessionSelect}
      />
    </div>

    {#if loadError}
      <div class="p-2">
        <button
          type="button"
          class="w-full rounded-lg bg-gray-800 px-4 py-2 text-sm text-gray-200 hover:bg-gray-700 transition-colors"
          onclick={() => { void loadSessions(); }}
        >
          Retry
        </button>
      </div>
    {/if}
  </aside>

  <!-- Main content: welcome / cost panel -->
  <main class="flex flex-1 flex-col overflow-auto p-6 gap-6">
    <!-- Welcome hero when no active session -->
    <div class="flex flex-col gap-4">
      <h1 class="text-2xl font-bold text-gray-100">Dashboard</h1>
      <p class="text-gray-400 text-sm max-w-prose">
        Select a session from the sidebar to view messages, or search across all sessions.
        New sessions from <code class="bg-gray-800 px-1 rounded text-gray-300">claude</code> and
        <code class="bg-gray-800 px-1 rounded text-gray-300">codex</code> appear automatically.
      </p>

      <!-- Keyboard shortcut hints -->
      <div class="flex flex-wrap gap-3 text-xs text-gray-600">
        <kbd class="rounded border border-gray-700 px-2 py-1">/</kbd>
        <span class="self-center">focus search</span>
        <kbd class="rounded border border-gray-700 px-2 py-1">j / k</kbd>
        <span class="self-center">navigate list</span>
        <kbd class="rounded border border-gray-700 px-2 py-1">Enter</kbd>
        <span class="self-center">open session</span>
        <kbd class="rounded border border-gray-700 px-2 py-1">Esc</kbd>
        <span class="self-center">clear</span>
      </div>
    </div>

    <!-- Cost panel -->
    <div class="max-w-sm">
      <CostPanel
        data={costSummary.data}
        loading={costSummary.loading}
        error={costError}
      />
    </div>
  </main>
</div>
