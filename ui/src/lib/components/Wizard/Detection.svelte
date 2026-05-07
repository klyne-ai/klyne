<script lang="ts">
  import type { WizardDetectResponse } from '$lib/types.js';

  interface Props {
    data: WizardDetectResponse | null;
    loading?: boolean;
    error?: string | null;
    onnext?: () => void;
    onback?: () => void;
  }

  const { data, loading = false, error = null, onnext, onback }: Props = $props();

  // Helper: render check/dash indicator.
  function indicator(ok: boolean): string {
    return ok ? '✓' : '–';
  }
</script>

<div data-testid="wizard-detection" class="flex flex-col gap-6 py-8 px-6 max-w-lg mx-auto">
  <div>
    <h2 class="text-xl font-bold text-gray-100">Detecting your setup</h2>
    <p class="text-sm text-gray-400 mt-1">
      We're checking which CLI sessions and API keys are available.
    </p>
  </div>

  {#if loading}
    <div data-testid="detection-loading" class="flex items-center gap-2 text-gray-400 text-sm">
      <span class="animate-spin">⟳</span>
      <span>Detecting…</span>
    </div>
  {:else if error}
    <div data-testid="detection-error" class="rounded-lg border border-red-800 bg-red-900/20 p-4 text-sm text-red-300">
      {error}
    </div>
  {:else if data}
    <!-- Connectors -->
    <section>
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 mb-3">CLI connectors</h3>
      <div class="flex flex-col gap-2">
        <div
          data-testid="detection-claude"
          class="flex items-center justify-between rounded-lg border {data.connectors.claude_ok ? 'border-green-800 bg-green-900/20' : 'border-gray-700 bg-gray-800/50'} px-4 py-3"
        >
          <div>
            <p class="text-sm font-medium text-gray-200">Claude Code</p>
            <p class="text-xs text-gray-500">{data.connectors.claude_root}</p>
          </div>
          <span
            class="text-lg font-bold {data.connectors.claude_ok ? 'text-green-400' : 'text-gray-600'}"
            aria-label={data.connectors.claude_ok ? 'available' : 'not found'}
          >
            {indicator(data.connectors.claude_ok)}
          </span>
        </div>

        <div
          data-testid="detection-codex"
          class="flex items-center justify-between rounded-lg border {data.connectors.codex_ok ? 'border-green-800 bg-green-900/20' : 'border-gray-700 bg-gray-800/50'} px-4 py-3"
        >
          <div>
            <p class="text-sm font-medium text-gray-200">Codex CLI</p>
            <p class="text-xs text-gray-500">{data.connectors.codex_root}</p>
          </div>
          <span
            class="text-lg font-bold {data.connectors.codex_ok ? 'text-green-400' : 'text-gray-600'}"
            aria-label={data.connectors.codex_ok ? 'available' : 'not found'}
          >
            {indicator(data.connectors.codex_ok)}
          </span>
        </div>
      </div>
    </section>

    <!-- Providers -->
    <section>
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 mb-3">API keys (for AI features)</h3>
      <div class="grid grid-cols-2 gap-2">
        {#each [
          { key: 'anthropic', label: 'Anthropic', ok: data.providers.anthropic },
          { key: 'openai',    label: 'OpenAI',    ok: data.providers.openai },
          { key: 'gemini',    label: 'Gemini',    ok: data.providers.gemini },
          { key: 'ollama',    label: 'Ollama',    ok: data.providers.ollama },
        ] as prov (prov.key)}
          <div
            data-testid="detection-provider-{prov.key}"
            class="flex items-center justify-between rounded-lg border {prov.ok ? 'border-green-800 bg-green-900/20' : 'border-gray-700 bg-gray-800/40'} px-3 py-2"
          >
            <span class="text-sm text-gray-300">{prov.label}</span>
            <span class="text-sm font-bold {prov.ok ? 'text-green-400' : 'text-gray-600'}">
              {indicator(prov.ok)}
            </span>
          </div>
        {/each}
      </div>
      <p class="mt-2 text-xs text-gray-600">
        API keys are read from environment variables only — no credential files are read.
      </p>
    </section>
  {/if}

  <!-- Navigation -->
  <div class="flex items-center justify-between pt-2">
    <button
      type="button"
      class="rounded-lg border border-gray-700 px-4 py-2 text-sm text-gray-400 hover:bg-gray-800 transition-colors"
      onclick={() => onback?.()}
      data-testid="wizard-back-button"
    >
      ← Back
    </button>
    <button
      type="button"
      class="rounded-lg bg-blue-600 px-6 py-2 text-sm font-semibold text-white hover:bg-blue-500 transition-colors disabled:opacity-50"
      onclick={() => onnext?.()}
      disabled={loading || !!error}
      data-testid="wizard-next-button"
    >
      Next →
    </button>
  </div>
</div>
