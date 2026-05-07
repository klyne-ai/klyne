<script lang="ts">
  import { goto } from '$app/navigation';
  import { fetchWizardDetect } from '$lib/api.js';
  import { postWizardComplete } from '$lib/wizard.js';
  import type { WizardDetectResponse } from '$lib/types.js';
  import Welcome from '$lib/components/Wizard/Welcome.svelte';
  import Detection from '$lib/components/Wizard/Detection.svelte';
  import ModelPick from '$lib/components/Wizard/ModelPick.svelte';
  import Done from '$lib/components/Wizard/Done.svelte';

  // State machine: which screen is active.
  type Screen = 'welcome' | 'detection' | 'modelpick' | 'done';

  let screen = $state<Screen>('welcome');

  // Detection data.
  let detectData = $state<WizardDetectResponse | null>(null);
  let detectLoading = $state(false);
  let detectError = $state<string | null>(null);

  // Model selections.
  let summaryModel = $state('auto');
  let titleModel = $state('auto');

  // Completion state.
  let saving = $state(false);
  let saveError = $state<string | null>(null);

  // Advance from Welcome → Detection (and kick off detection).
  async function goToDetection(): Promise<void> {
    screen = 'detection';
    detectLoading = true;
    detectError = null;
    try {
      detectData = await fetchWizardDetect();
      // Pre-fill model selections from recommendations.
      const sumRec = detectData.recommendations.find((r) => r.task === 'summary');
      const titleRec = detectData.recommendations.find((r) => r.task === 'title');
      if (sumRec) {
        summaryModel = `${sumRec.selected.provider}:${sumRec.selected.model}`;
      }
      if (titleRec) {
        titleModel = `${titleRec.selected.provider}:${titleRec.selected.model}`;
      }
    } catch (e: unknown) {
      detectError = e instanceof Error ? e.message : 'Detection failed';
    } finally {
      detectLoading = false;
    }
  }

  function goToModelPick(): void {
    screen = 'modelpick';
  }

  function goBack(): void {
    const prev: Record<Screen, Screen> = {
      welcome: 'welcome',
      detection: 'welcome',
      modelpick: 'detection',
      done: 'modelpick'
    };
    screen = prev[screen];
  }

  // POST wizard/complete then navigate to dashboard.
  async function complete(): Promise<void> {
    screen = 'done';
    saving = true;
    saveError = null;
    try {
      await postWizardComplete(summaryModel, titleModel);
      saving = false;
    } catch (e: unknown) {
      saveError = e instanceof Error ? e.message : 'Failed to save settings';
      saving = false;
    }
  }

  function handleDone(): void {
    if (saveError) {
      // Retry.
      void complete();
    } else {
      void goto('/');
    }
  }
</script>

<svelte:head>
  <title>agentdeck — Setup Wizard</title>
</svelte:head>

<div data-testid="wizard-page" class="flex flex-1 items-start justify-center overflow-auto bg-gray-950 py-8">
  <div class="w-full max-w-lg">
    <!-- Progress indicator -->
    <div class="flex items-center gap-2 px-6 mb-6">
      {#each ['welcome', 'detection', 'modelpick', 'done'] as s, i (s)}
        <div class="flex items-center gap-2">
          {#if i > 0}
            <div class="h-px w-6 {['detection', 'modelpick', 'done'].includes(screen) && i <= ['welcome', 'detection', 'modelpick', 'done'].indexOf(screen) ? 'bg-blue-600' : 'bg-gray-700'}"></div>
          {/if}
          <div
            class="h-2 w-2 rounded-full {screen === s ? 'bg-blue-500' :
              ['detection', 'modelpick', 'done'].indexOf(screen) > ['welcome', 'detection', 'modelpick', 'done'].indexOf(s)
                ? 'bg-blue-700'
                : 'bg-gray-700'}"
          ></div>
        </div>
      {/each}
    </div>

    <!-- Screen content -->
    {#if screen === 'welcome'}
      <Welcome onnext={goToDetection} />
    {:else if screen === 'detection'}
      <Detection
        data={detectData}
        loading={detectLoading}
        error={detectError}
        onnext={goToModelPick}
        onback={goBack}
      />
    {:else if screen === 'modelpick'}
      <ModelPick
        detectData={detectData}
        {summaryModel}
        {titleModel}
        onsummarychange={(v) => { summaryModel = v; }}
        ontitlechange={(v) => { titleModel = v; }}
        onnext={complete}
        onback={goBack}
      />
    {:else if screen === 'done'}
      <Done
        {saving}
        error={saveError}
        ondone={handleDone}
      />
    {/if}
  </div>
</div>
