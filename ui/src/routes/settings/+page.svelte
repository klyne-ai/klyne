<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchSettings, updateSettings, fetchWizardDetect } from '$lib/api.js';
  import type { SettingsResponse, WizardDetectResponse, WizardRecommendation } from '$lib/types.js';
  import ModelPicker from '$lib/components/ModelPicker.svelte';

  // State
  let settings = $state<SettingsResponse | null>(null);
  let detectData = $state<WizardDetectResponse | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let saveError = $state<string | null>(null);
  let saved = $state(false);

  // Local editable values.
  let summaryModel = $state('auto');
  let titleModel = $state('auto');
  let claudeEnabled = $state(true);
  let codexEnabled = $state(true);

  function getRecommendation(task: 'summary' | 'title'): WizardRecommendation | null {
    if (!detectData) return null;
    return detectData.recommendations.find((r) => r.task === task) ?? null;
  }

  const detected = $derived(
    settings?.detected ?? { anthropic: false, openai: false, gemini: false, ollama: false }
  );

  // Load settings + detection data.
  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      const [s, d] = await Promise.all([fetchSettings(), fetchWizardDetect()]);
      settings = s;
      detectData = d;
      // Pre-fill form from current settings.
      const ai = s.ai;
      summaryModel = ai.summary_model.provider
        ? `${ai.summary_model.provider}:${ai.summary_model.model}`
        : ai.summary_model.model || 'auto';
      titleModel = ai.title_model.provider
        ? `${ai.title_model.provider}:${ai.title_model.model}`
        : ai.title_model.model || 'auto';
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
  }

  // Save settings via PUT /settings.
  async function save(): Promise<void> {
    if (!settings) return;
    saving = true;
    saveError = null;
    saved = false;
    try {
      // Parse the "<provider>:<model>" string into TaskModel.
      function parseModel(s: string): { provider: string; model: string } {
        if (!s || s === 'auto') return { provider: '', model: 'auto' };
        const idx = s.indexOf(':');
        if (idx > 0) return { provider: s.slice(0, idx), model: s.slice(idx + 1) };
        return { provider: '', model: s };
      }

      await updateSettings({
        ai: {
          summary_model: parseModel(summaryModel),
          title_model: parseModel(titleModel),
          embed_model: settings.ai.embed_model
        }
      });
      saved = true;
      setTimeout(() => { saved = false; }, 3000);
    } catch (e: unknown) {
      saveError = e instanceof Error ? e.message : 'Failed to save settings';
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head>
  <title>agentdeck — Settings</title>
</svelte:head>

<div data-testid="settings-page" class="flex flex-1 overflow-auto p-6">
  <div class="w-full max-w-lg">
    <div class="mb-6">
      <h1 class="text-xl font-bold text-gray-100">Settings</h1>
      <p class="text-sm text-gray-400 mt-1">Configure AI models and connector options.</p>
    </div>

    {#if loading}
      <div data-testid="settings-loading" class="flex items-center gap-2 text-gray-400 text-sm">
        <span class="animate-spin">⟳</span>
        <span>Loading settings…</span>
      </div>
    {:else if error}
      <div data-testid="settings-error" class="rounded-lg border border-red-800 bg-red-900/20 p-4 text-sm text-red-300">
        {error}
        <button
          type="button"
          class="ml-2 underline"
          onclick={() => void load()}
        >Retry</button>
      </div>
    {:else}
      <form
        data-testid="settings-form"
        onsubmit={(e) => { e.preventDefault(); void save(); }}
        class="flex flex-col gap-6"
      >
        <!-- AI model section -->
        <section class="flex flex-col gap-4">
          <h2 class="text-sm font-semibold text-gray-300 border-b border-gray-700 pb-2">AI models</h2>

          <ModelPicker
            task="summarize"
            detected={detected}
            recommended={getRecommendation('summary')}
            value={summaryModel}
            onchange={(v) => { summaryModel = v; }}
          />

          <ModelPicker
            task="title"
            detected={detected}
            recommended={getRecommendation('title')}
            value={titleModel}
            onchange={(v) => { titleModel = v; }}
          />
        </section>

        <!-- Connector toggles -->
        <section class="flex flex-col gap-3">
          <h2 class="text-sm font-semibold text-gray-300 border-b border-gray-700 pb-2">Connectors</h2>

          <label class="flex items-center gap-3 cursor-pointer" data-testid="settings-claude-toggle">
            <input
              type="checkbox"
              bind:checked={claudeEnabled}
              class="h-4 w-4 rounded border-gray-600 bg-gray-800 text-blue-500 focus:ring-blue-500"
            />
            <div>
              <p class="text-sm font-medium text-gray-200">Claude Code</p>
              <p class="text-xs text-gray-500">Watch ~/.claude/projects/ for new sessions</p>
            </div>
          </label>

          <label class="flex items-center gap-3 cursor-pointer" data-testid="settings-codex-toggle">
            <input
              type="checkbox"
              bind:checked={codexEnabled}
              class="h-4 w-4 rounded border-gray-600 bg-gray-800 text-blue-500 focus:ring-blue-500"
            />
            <div>
              <p class="text-sm font-medium text-gray-200">Codex CLI</p>
              <p class="text-xs text-gray-500">Watch ~/.codex/sessions/ for new sessions</p>
            </div>
          </label>
        </section>

        <!-- Save button + feedback -->
        <div class="flex items-center gap-4">
          <button
            type="submit"
            class="rounded-lg bg-blue-600 px-6 py-2 text-sm font-semibold text-white hover:bg-blue-500 transition-colors disabled:opacity-50"
            disabled={saving}
            data-testid="settings-save-button"
          >
            {saving ? 'Saving…' : 'Save settings'}
          </button>

          {#if saved}
            <span data-testid="settings-saved-msg" class="text-sm text-green-400">✓ Saved</span>
          {/if}

          {#if saveError}
            <span data-testid="settings-save-error" class="text-sm text-red-400">{saveError}</span>
          {/if}
        </div>
      </form>
    {/if}
  </div>
</div>
