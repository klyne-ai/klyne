<script lang="ts">
  import { fetchRestore } from '$lib/api.js';
  import type { RestoreResponse } from '$lib/types.js';

  interface Props {
    sessionId: string;
    onclose?: () => void;
  }

  const { sessionId, onclose }: Props = $props();

  // State
  let loading = $state(true);
  let error = $state<string | null>(null);
  let data = $state<RestoreResponse | null>(null);
  let copied = $state(false);

  // Load restore context on mount.
  $effect(() => {
    loading = true;
    error = null;
    fetchRestore(sessionId)
      .then((res) => {
        data = res;
      })
      .catch((e: unknown) => {
        error = e instanceof Error ? e.message : 'Failed to load restore context';
      })
      .finally(() => {
        loading = false;
      });
  });

  async function copyToClipboard(): Promise<void> {
    if (!data) return;
    try {
      await navigator.clipboard.writeText(data.markdown);
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 2000);
    } catch {
      error = 'Failed to copy to clipboard';
    }
  }

  function handleClose(): void {
    onclose?.();
  }
</script>

<!-- Backdrop -->
<div
  data-testid="restore-context-backdrop"
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
  role="dialog"
  aria-modal="true"
  aria-label="Restore context"
>
  <!-- Modal -->
  <div
    class="relative flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-gray-700 bg-gray-900 shadow-2xl"
    data-testid="restore-context-modal"
  >
    <!-- Header -->
    <div class="flex shrink-0 items-center justify-between border-b border-gray-700 px-6 py-4">
      <div>
        <h2 class="text-base font-semibold text-gray-100">Restore context</h2>
        <p class="text-xs text-gray-500 mt-0.5">Session {sessionId}</p>
      </div>
      <button
        type="button"
        class="rounded-lg p-1 text-gray-500 hover:bg-gray-800 hover:text-gray-300 transition-colors"
        onclick={handleClose}
        aria-label="Close"
        data-testid="restore-close-button"
      >
        ✕
      </button>
    </div>

    <!-- Body -->
    <div class="flex-1 overflow-auto p-6">
      {#if loading}
        <div data-testid="restore-loading" class="flex items-center gap-2 text-gray-400 text-sm">
          <span class="animate-spin">⟳</span>
          <span>Loading restore context…</span>
        </div>
      {:else if error}
        <div data-testid="restore-error" class="rounded-lg border border-red-800 bg-red-900/20 p-4 text-sm text-red-300">
          {error}
        </div>
      {:else if data}
        <!-- Resume command -->
        {#if data.resume_cmd}
          <div class="mb-4">
            <p class="mb-1 text-xs font-medium uppercase tracking-wide text-gray-500">Resume command</p>
            <code
              data-testid="restore-resume-cmd"
              class="block rounded-lg border border-gray-700 bg-gray-800 px-3 py-2 text-sm font-mono text-green-300"
            >
              {data.resume_cmd}
            </code>
          </div>
        {/if}

        <!-- Markdown bundle -->
        <div>
          <p class="mb-1 text-xs font-medium uppercase tracking-wide text-gray-500">Context bundle</p>
          <pre
            data-testid="restore-markdown"
            class="overflow-auto rounded-lg border border-gray-700 bg-gray-800 p-4 text-xs text-gray-300 whitespace-pre-wrap break-words"
          >{data.markdown}</pre>
        </div>
      {/if}
    </div>

    <!-- Footer -->
    <div class="shrink-0 border-t border-gray-700 px-6 py-4 flex items-center justify-between">
      <span class="text-xs text-gray-600">
        {#if data}
          {data.tail.length} messages · generated {new Date(data.generated_at).toLocaleTimeString()}
        {/if}
      </span>
      <button
        type="button"
        class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 transition-colors disabled:opacity-50"
        onclick={copyToClipboard}
        disabled={!data || loading}
        data-testid="restore-copy-button"
      >
        {copied ? 'Copied!' : 'Copy as resume prompt'}
      </button>
    </div>
  </div>
</div>
