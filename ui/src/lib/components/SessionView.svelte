<script lang="ts">
  import type { Message } from '$lib/types.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';
  import MessageBubble from './MessageBubble.svelte';

  interface Props {
    messages: Message[];
    loading?: boolean;
    error?: string | null;
    highlightMessageId?: string | null;
    onRetry?: () => void;
  }

  const {
    messages,
    loading = false,
    error = null,
    highlightMessageId = null,
    onRetry
  }: Props = $props();

  // Global rule (see $lib/messageFilters.ts): only user + assistant
  // prose ever renders. Tool / system / pure-tool-call assistant
  // rows are dropped before the bubble loop, and ToolCallBlock is
  // no longer used anywhere here.
  const visibleMessages = $derived(messages.filter(isConversationalMessage));

  let containerEl: HTMLElement | undefined = $state(undefined);

  // Scroll to bottom when new messages arrive
  $effect(() => {
    // Depend on visibleMessages.length to re-run on append
    void visibleMessages.length;
    if (containerEl) {
      containerEl.scrollTop = containerEl.scrollHeight;
    }
  });
</script>

<div
  data-testid="session-view"
  bind:this={containerEl}
  class="flex flex-col gap-4 p-4 overflow-y-auto flex-1"
>
  {#if loading}
    <!-- Skeleton loading state -->
    <div data-testid="session-view-loading" class="flex flex-col gap-4 animate-pulse">
      {#each [1, 2, 3] as _}
        <div class="rounded-lg bg-gray-800 p-4 h-16 w-3/4"></div>
        <div class="rounded-lg bg-gray-700 p-4 h-20 w-2/3 self-end"></div>
      {/each}
    </div>

  {:else if error}
    <!-- Error state -->
    <div
      data-testid="session-view-error"
      class="flex flex-col items-center justify-center flex-1 gap-3 py-12 text-center"
    >
      <p class="text-red-400">{error}</p>
      {#if onRetry}
        <button
          type="button"
          class="rounded-lg bg-gray-800 px-4 py-2 text-sm text-gray-200 hover:bg-gray-700 transition-colors"
          onclick={onRetry}
        >
          Retry
        </button>
      {/if}
    </div>

  {:else if visibleMessages.length === 0}
    <!-- Empty state — either no messages at all, or only tool / system
         / pure-tool-call rows that the global filter drops. -->
    <div
      data-testid="session-view-empty"
      class="flex flex-col items-center justify-center flex-1 gap-2 py-12 text-center"
    >
      <p class="text-gray-400">No messages in this session yet.</p>
    </div>

  {:else}
    {#each visibleMessages as msg (msg.id)}
      <MessageBubble message={msg} highlight={highlightMessageId === msg.id} />
    {/each}
  {/if}
</div>
