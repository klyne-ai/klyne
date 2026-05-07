<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { fetchSession, fetchMessages } from '$lib/api.js';
  import { subscribe } from '$lib/sse.js';
  import type { Message } from '$lib/types.js';
  import SessionView from '$lib/components/SessionView.svelte';

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------

  let sessionId = $derived($page.params.id ?? '');
  let highlightMessageId = $derived($page.url.searchParams.get('msg'));

  let sessionTitle = $state<string>('Session');
  let messages = $state<Message[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // ---------------------------------------------------------------------------
  // Data fetching
  // ---------------------------------------------------------------------------

  async function load(id: string): Promise<void> {
    if (!id) return;
    loading = true;
    error = null;
    messages = [];
    try {
      const [sessionRes, msgsRes] = await Promise.all([
        fetchSession(id),
        fetchMessages(id, { limit: 200 })
      ]);
      sessionTitle = sessionRes.session.project_path.split('/').filter(Boolean).pop() ?? id;
      messages = msgsRes.messages;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load session';
    } finally {
      loading = false;
    }
  }

  // ---------------------------------------------------------------------------
  // SSE – live updates
  // ---------------------------------------------------------------------------

  let unsubscribe: (() => void) | null = null;

  async function fetchNewMessages(id: string): Promise<void> {
    // Append any new messages not already in the list
    try {
      const res = await fetchMessages(id, { limit: 50 });
      const newMessages = res.messages.filter(
        (m) => !messages.some((existing) => existing.id === m.id)
      );
      if (newMessages.length > 0) {
        messages = [...messages, ...newMessages];
      }
    } catch {
      // Non-fatal — live update failure doesn't break the page
    }
  }

  // ---------------------------------------------------------------------------
  // Lifecycle
  // ---------------------------------------------------------------------------

  onMount(() => {
    void load(sessionId);

    unsubscribe = subscribe({
      onMsgNew: (payload) => {
        if (payload.session_id === sessionId) {
          void fetchNewMessages(sessionId);
        }
      }
    });
  });

  onDestroy(() => {
    unsubscribe?.();
    unsubscribe = null;
  });

  // Re-load when the session ID changes (navigation between sessions)
  $effect(() => {
    const id = sessionId;
    if (id) {
      void load(id);
    }
  });
</script>

<svelte:head>
  <title>{sessionTitle} — agentdeck</title>
</svelte:head>

<div class="flex flex-col flex-1 overflow-hidden w-full">
  <!-- Header bar -->
  <header class="flex items-center gap-3 px-4 py-2 border-b border-gray-800 shrink-0">
    <button
      type="button"
      aria-label="Back to dashboard"
      class="text-gray-500 hover:text-gray-300 transition-colors"
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
    <h1 class="text-sm font-semibold text-gray-200 truncate">{sessionTitle}</h1>
    {#if !loading && !error}
      <span class="ml-auto text-xs text-gray-500">{messages.length} messages</span>
    {/if}
  </header>

  <!-- Message area -->
  <SessionView
    {messages}
    {loading}
    {error}
    {highlightMessageId}
    onRetry={() => void load(sessionId)}
  />
</div>
