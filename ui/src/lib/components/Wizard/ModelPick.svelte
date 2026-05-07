<script lang="ts">
  import type { WizardDetectResponse, WizardRecommendation } from '$lib/types.js';
  import ModelPicker from '$lib/components/ModelPicker.svelte';

  interface Props {
    detectData: WizardDetectResponse | null;
    summaryModel: string;
    titleModel: string;
    onsummarychange?: (value: string) => void;
    ontitlechange?: (value: string) => void;
    onnext?: () => void;
    onback?: () => void;
  }

  const {
    detectData,
    summaryModel,
    titleModel,
    onsummarychange,
    ontitlechange,
    onnext,
    onback
  }: Props = $props();

  // Find the recommendation for a task.
  function getRecommendation(task: 'summary' | 'title'): WizardRecommendation | null {
    if (!detectData) return null;
    return detectData.recommendations.find((r) => r.task === task) ?? null;
  }

  const detected = $derived(detectData?.providers ?? {
    anthropic: false,
    openai: false,
    gemini: false,
    ollama: false
  });

  const summaryRec = $derived(getRecommendation('summary'));
  const titleRec = $derived(getRecommendation('title'));
</script>

<div data-testid="wizard-modelpick" class="flex flex-col gap-6 py-8 px-6 max-w-lg mx-auto">
  <div>
    <h2 class="text-xl font-bold text-gray-100">Choose your AI models</h2>
    <p class="text-sm text-gray-400 mt-1">
      agentdeck uses AI to generate rolling summaries and session titles.
      The recommended options are the most cost-effective for your available keys.
    </p>
  </div>

  <!-- Summarize model picker -->
  <ModelPicker
    task="summarize"
    detected={detected}
    recommended={summaryRec}
    value={summaryModel}
    onchange={(v) => onsummarychange?.(v)}
  />

  <!-- Title model picker -->
  <ModelPicker
    task="title"
    detected={detected}
    recommended={titleRec}
    value={titleModel}
    onchange={(v) => ontitlechange?.(v)}
  />

  <!-- Note for Claude-only users (spec §8 "I only have Claude Pro" path) -->
  {#if detected.anthropic && !detected.gemini && !detected.openai && !detected.ollama}
    <div class="rounded-lg border border-yellow-800/50 bg-yellow-900/10 p-4 text-sm text-yellow-300">
      <p class="font-medium mb-1">Tip: Save on API costs</p>
      <p class="text-xs text-yellow-400/80">
        You only have Anthropic configured. Gemini Flash-Lite offers a free tier that covers
        most summarization workloads. Set a <code>GEMINI_API_KEY</code> for cost-free AI features.
      </p>
    </div>
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
      class="rounded-lg bg-blue-600 px-6 py-2 text-sm font-semibold text-white hover:bg-blue-500 transition-colors"
      onclick={() => onnext?.()}
      data-testid="wizard-next-button"
    >
      Next →
    </button>
  </div>
</div>
