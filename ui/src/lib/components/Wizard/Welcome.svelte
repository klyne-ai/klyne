<script lang="ts">
  interface Props {
    onnext?: () => void;
  }

  const { onnext }: Props = $props();
</script>

<div data-testid="wizard-welcome" class="flex flex-col items-center gap-8 py-12 px-6 max-w-lg mx-auto text-center">
  <div class="flex flex-col gap-3">
    <div class="text-5xl" aria-hidden="true">🗂</div>
    <h1 class="text-2xl font-bold text-gray-100">Welcome to klyne</h1>
    <p class="text-gray-400 leading-relaxed">
      klyne watches your Claude Code and Codex CLI sessions in real time,
      giving you cost tracking, full-text search, and context-restore when your
      Claude context gets compacted.
    </p>
  </div>

  <div class="flex flex-col gap-4 w-full text-left">
    <div class="flex items-start gap-3 rounded-lg border border-gray-700 bg-gray-800/50 p-4">
      <span class="text-xl" aria-hidden="true">⚡</span>
      <div>
        <p class="text-sm font-medium text-gray-200">Real-time session monitoring</p>
        <p class="text-xs text-gray-500 mt-0.5">Messages appear as Claude Code writes them to disk.</p>
      </div>
    </div>

    <div class="flex items-start gap-3 rounded-lg border border-gray-700 bg-gray-800/50 p-4">
      <span class="text-xl" aria-hidden="true">💰</span>
      <div>
        <p class="text-sm font-medium text-gray-200">Token & cost tracking</p>
        <p class="text-xs text-gray-500 mt-0.5">See exactly what each session costs, grouped by project or day.</p>
      </div>
    </div>

    <div class="flex items-start gap-3 rounded-lg border border-gray-700 bg-gray-800/50 p-4">
      <span class="text-xl" aria-hidden="true">🔄</span>
      <div>
        <p class="text-sm font-medium text-gray-200">Compact recovery</p>
        <p class="text-xs text-gray-500 mt-0.5">
          When Claude compacts your context, get a one-click restore bundle to paste back in.
        </p>
      </div>
    </div>
  </div>

  <button
    type="button"
    data-testid="wizard-next-button"
    class="w-full rounded-lg bg-blue-600 px-6 py-3 text-sm font-semibold text-white hover:bg-blue-500 transition-colors"
    onclick={() => onnext?.()}
  >
    Get started →
  </button>
</div>
