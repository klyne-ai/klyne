<script lang="ts">
  import type { Message } from '$lib/types.js';

  interface Props {
    message: Message;
    highlight?: boolean;
  }

  const { message, highlight = false }: Props = $props();

  const roleLabel: Record<string, string> = {
    user: 'You',
    assistant: 'Assistant',
    tool: 'Tool',
    system: 'System'
  };

  const roleBubbleClass: Record<string, string> = {
    user: 'bg-blue-900 border-blue-700 text-blue-50 ml-auto',
    assistant: 'bg-gray-800 border-gray-700 text-gray-100',
    tool: 'bg-gray-900 border-gray-700 text-gray-300 font-mono text-xs',
    system: 'bg-yellow-900/40 border-yellow-700/50 text-yellow-200 italic text-xs'
  };

  const roleHeaderClass: Record<string, string> = {
    user: 'text-blue-300',
    assistant: 'text-green-400',
    tool: 'text-orange-400',
    system: 'text-yellow-500'
  };

  const bubbleClass = $derived(
    roleBubbleClass[message.role] ?? 'bg-gray-800 border-gray-700 text-gray-100'
  );
  const headerClass = $derived(
    roleHeaderClass[message.role] ?? 'text-gray-400'
  );
  const label = $derived(roleLabel[message.role] ?? message.role);

  function formatTime(tsMs: number): string {
    return new Date(tsMs).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
</script>

<article
  data-testid="message-bubble"
  data-role={message.role}
  class="flex flex-col gap-1 max-w-3xl w-full {message.role === 'user' ? 'self-end items-end' : 'self-start items-start'}"
  class:ring-2={highlight}
  class:ring-blue-400={highlight}
>
  <header class="flex items-center gap-2 px-1 text-xs {headerClass}">
    <span class="font-semibold">{label}</span>
    {#if message.model}
      <span class="text-gray-500">{message.model}</span>
    {/if}
    <time class="text-gray-600" datetime={new Date(message.ts).toISOString()}>
      {formatTime(message.ts)}
    </time>
  </header>

  <div
    class="rounded-lg border px-4 py-3 whitespace-pre-wrap break-words leading-relaxed {bubbleClass}"
  >
    {#if message.content}
      {message.content}
    {:else if (message.tool_calls?.length ?? 0) > 0}
      <span class="text-gray-500 italic">Tool call</span>
    {:else}
      <span class="text-gray-600 italic">(empty)</span>
    {/if}
  </div>

  {#if message.cost_usd > 0}
    <footer class="px-1 text-xs text-gray-600">
      {message.tokens_in}↑ {message.tokens_out}↓ · ${message.cost_usd.toFixed(4)}
    </footer>
  {/if}
</article>
