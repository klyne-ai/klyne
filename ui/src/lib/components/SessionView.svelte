<script lang="ts">
  import type { Message } from '$lib/types.js';
  import MessageBubble from './MessageBubble.svelte';
  import ToolCallBlock from './ToolCallBlock.svelte';

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

  /**
   * Build a lookup map from tool_call id → ToolResult from the tool messages
   * so ToolCallBlock can receive the matched result.
   */
  const toolResultMap = $derived(
    (() => {
      const map = new Map<string, { output: string; is_error: boolean; id: string }>();
      for (const msg of messages) {
        for (const tr of msg.tool_results ?? []) {
          map.set(tr.id, tr);
        }
      }
      return map;
    })()
  );

  let containerEl: HTMLElement | undefined = $state(undefined);

  // Scroll to bottom when new messages arrive
  $effect(() => {
    // Depend on messages.length to re-run on append
    void messages.length;
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

  {:else if messages.length === 0}
    <!-- Empty state -->
    <div
      data-testid="session-view-empty"
      class="flex flex-col items-center justify-center flex-1 gap-2 py-12 text-center"
    >
      <p class="text-gray-400">No messages in this session yet.</p>
    </div>

  {:else}
    {#each messages as msg (msg.id)}
      {#if msg.role === 'tool'}
        <!-- Tool result messages: rendered inside ToolCallBlock, skip standalone bubble -->
        <!-- They show up via the toolResultMap lookup from assistant messages -->
      {:else}
        <!-- Render the message bubble -->
        <MessageBubble message={msg} highlight={highlightMessageId === msg.id} />

        <!-- If the message has tool calls, render each one with its matched result -->
        {#each msg.tool_calls ?? [] as tc (tc.id)}
          <div class="max-w-3xl w-full {msg.role === 'user' ? 'self-end' : 'self-start'}">
            <ToolCallBlock
              toolCall={tc}
              toolResult={toolResultMap.get(tc.id)}
            />
          </div>
        {/each}
      {/if}
    {/each}
  {/if}
</div>
