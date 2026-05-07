<script lang="ts">
  import type { ToolCall, ToolResult } from '$lib/types.js';

  interface Props {
    toolCall: ToolCall;
    toolResult?: ToolResult;
  }

  const { toolCall, toolResult }: Props = $props();

  let expanded = $state(false);

  function toggleExpanded(): void {
    expanded = !expanded;
  }

  function prettyJson(raw: string): string {
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }

  const formattedInput = $derived(prettyJson(toolCall.input));
  const formattedOutput = $derived(toolResult ? prettyJson(toolResult.output) : '');
</script>

<div
  data-testid="tool-call-block"
  class="rounded-lg border border-gray-700 bg-gray-900 overflow-hidden text-xs font-mono"
>
  <!-- Header / trigger -->
  <button
    type="button"
    class="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-gray-800 transition-colors"
    onclick={toggleExpanded}
    aria-expanded={expanded}
    aria-controls="tool-body-{toolCall.id}"
  >
    <span class="text-orange-400 font-semibold" data-testid="tool-name">{toolCall.name}</span>
    {#if toolResult}
      {#if toolResult.is_error}
        <span class="ml-auto text-red-400">✗ error</span>
      {:else}
        <span class="ml-auto text-green-400">✓ ok</span>
      {/if}
    {:else}
      <span class="ml-auto text-gray-500">pending</span>
    {/if}
    <span class="text-gray-500 ml-1">{expanded ? '▲' : '▼'}</span>
  </button>

  {#if expanded}
    <div id="tool-body-{toolCall.id}" class="border-t border-gray-700">
      <!-- Input -->
      <div class="px-3 py-2">
        <p class="text-gray-500 mb-1 font-sans text-xs uppercase tracking-wide">Input</p>
        <pre
          data-testid="tool-input"
          class="text-gray-300 whitespace-pre-wrap break-all overflow-x-auto"
        >{formattedInput}</pre>
      </div>

      {#if toolResult}
        <!-- Output -->
        <div class="border-t border-gray-700/50 px-3 py-2">
          <p class="text-gray-500 mb-1 font-sans text-xs uppercase tracking-wide">
            {toolResult.is_error ? 'Error' : 'Output'}
          </p>
          <pre
            data-testid="tool-output"
            class="whitespace-pre-wrap break-all overflow-x-auto {toolResult.is_error ? 'text-red-300' : 'text-gray-300'}"
          >{formattedOutput}</pre>
        </div>
      {/if}
    </div>
  {/if}
</div>
