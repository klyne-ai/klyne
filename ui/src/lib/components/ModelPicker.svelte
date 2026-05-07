<script lang="ts">
  import type { WizardRecommendation, DetectedProviders } from '$lib/types.js';

  // A simple model entry for the dropdown.
  export interface ModelOption {
    value: string; // "<provider>:<model>"
    label: string;
  }

  interface Props {
    task: 'summarize' | 'title';
    /** Available providers bitmask from DetectedProviders. */
    detected: DetectedProviders;
    /** Additional model options to show (e.g. from wizard detect). */
    options?: ModelOption[];
    /** Recommended selection from the selector. */
    recommended: WizardRecommendation | null;
    /** Current value: "auto" or "<provider>:<model>". */
    value: string;
    onchange?: (value: string) => void;
  }

  const { task, detected, options = [], recommended, value, onchange }: Props = $props();

  // Build the full options list.
  const allOptions = $derived(() => {
    const base: ModelOption[] = [{ value: 'auto', label: 'Auto (recommended)' }];

    // Add recommended option explicitly if not already in options.
    if (recommended) {
      const recVal = `${recommended.selected.provider}:${recommended.selected.model}`;
      const alreadyPresent = options.some((o) => o.value === recVal);
      if (!alreadyPresent) {
        base.push({ value: recVal, label: `${recommended.selected.provider} / ${recommended.selected.model} ★` });
      }
    }

    // Add provided options.
    for (const opt of options) {
      base.push(opt);
    }

    return base;
  });

  function handleChange(e: Event): void {
    const sel = e.target as HTMLSelectElement;
    onchange?.(sel.value);
  }
</script>

<div class="flex flex-col gap-2" data-testid="model-picker" data-task={task}>
  <label
    class="text-xs font-medium uppercase tracking-wide text-gray-500"
    for="model-picker-{task}"
  >
    {task === 'summarize' ? 'Summarize model' : 'Title model'}
  </label>

  <!-- Recommended badge -->
  {#if recommended}
    <div
      data-testid="model-picker-recommendation"
      class="flex items-start gap-2 rounded-lg border border-blue-800/50 bg-blue-900/20 px-3 py-2"
    >
      <span class="shrink-0 rounded-full bg-blue-600 px-2 py-0.5 text-xs font-semibold text-white">
        Recommended
      </span>
      <div class="min-w-0">
        <p class="text-sm font-medium text-blue-200">
          {recommended.selected.provider} / {recommended.selected.model}
        </p>
        <p class="text-xs text-gray-400 mt-0.5">{recommended.reason}</p>
      </div>
    </div>
  {/if}

  <!-- Dropdown -->
  <select
    id="model-picker-{task}"
    data-testid="model-picker-select"
    class="w-full rounded-lg border border-gray-700 bg-gray-800 px-3 py-2 text-sm text-gray-100
           focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
    {value}
    onchange={handleChange}
  >
    {#each allOptions() as opt (opt.value)}
      <option value={opt.value}>{opt.label}</option>
    {/each}
  </select>

  <!-- Provider availability hint -->
  <p class="text-xs text-gray-600">
    Available:
    {[
      detected.anthropic && 'Anthropic',
      detected.openai && 'OpenAI',
      detected.gemini && 'Gemini',
      detected.ollama && 'Ollama'
    ]
      .filter(Boolean)
      .join(', ') || 'none detected'}
  </p>
</div>
