<script lang="ts">
  interface Props {
    saving?: boolean;
    error?: string | null;
    ondone?: () => void;
  }

  const { saving = false, error = null, ondone }: Props = $props();
</script>

<div data-testid="wizard-done" class="flex flex-col items-center gap-8 py-12 px-6 max-w-lg mx-auto text-center">
  {#if saving}
    <div data-testid="wizard-saving" class="flex flex-col items-center gap-4">
      <span class="text-4xl animate-spin">⟳</span>
      <p class="text-gray-400 text-sm">Saving your settings…</p>
    </div>
  {:else if error}
    <div data-testid="wizard-error" class="flex flex-col items-center gap-4 w-full">
      <div class="rounded-lg border border-red-800 bg-red-900/20 p-4 text-sm text-red-300 w-full text-left">
        {error}
      </div>
      <button
        type="button"
        class="rounded-lg bg-blue-600 px-6 py-3 text-sm font-semibold text-white hover:bg-blue-500 transition-colors"
        onclick={() => ondone?.()}
        data-testid="wizard-done-button"
      >
        Try again
      </button>
    </div>
  {:else}
    <div class="flex flex-col items-center gap-3">
      <div class="text-5xl" aria-hidden="true">🎉</div>
      <h2 class="text-2xl font-bold text-gray-100">You're all set!</h2>
      <p class="text-gray-400 leading-relaxed">
        klyne is now configured and watching your sessions.
        Open Claude Code or Codex CLI and start working — your sessions will appear here automatically.
      </p>
    </div>

    <div class="flex flex-col gap-3 w-full text-left">
      <div class="flex items-center gap-3 text-sm text-gray-400">
        <span class="text-green-400 font-bold">✓</span>
        <span>Settings saved to <code class="text-gray-300">~/.klyne/config.toml</code></span>
      </div>
      <div class="flex items-center gap-3 text-sm text-gray-400">
        <span class="text-green-400 font-bold">✓</span>
        <span>Session monitoring active</span>
      </div>
      <div class="flex items-center gap-3 text-sm text-gray-400">
        <span class="text-green-400 font-bold">✓</span>
        <span>Compact recovery enabled</span>
      </div>
    </div>

    <button
      type="button"
      data-testid="wizard-done-button"
      class="w-full rounded-lg bg-blue-600 px-6 py-3 text-sm font-semibold text-white hover:bg-blue-500 transition-colors"
      onclick={() => ondone?.()}
    >
      Open dashboard →
    </button>
  {/if}
</div>
