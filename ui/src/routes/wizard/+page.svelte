<script lang="ts">
  import { goto } from '$app/navigation';
  import { fetchWizardDetect } from '$lib/api.js';
  import { postWizardComplete } from '$lib/wizard.js';
  import type { WizardDetectResponse } from '$lib/types.js';

  let step = $state(0);
  const steps = ['Welcome', 'Detection', 'Models', 'Done'];

  let detectData = $state<WizardDetectResponse | null>(null);
  let detectLoading = $state(false);
  let detectError = $state<string | null>(null);

  let summaryModel = $state('claude-haiku-4-5');
  let titleModel = $state('claude-haiku-4-5');
  let restoreModel = $state('claude-sonnet-4-6');

  let saving = $state(false);
  let saveError = $state<string | null>(null);

  async function loadDetect(): Promise<void> {
    detectLoading = true;
    detectError = null;
    try {
      detectData = await fetchWizardDetect();
      const sumRec = detectData.recommendations.find((r) => r.task === 'summary');
      const titleRec = detectData.recommendations.find((r) => r.task === 'title');
      if (sumRec) summaryModel = sumRec.selected.model;
      if (titleRec) titleModel = titleRec.selected.model;
    } catch (e) {
      detectError = e instanceof Error ? e.message : 'Detection failed';
    } finally {
      detectLoading = false;
    }
  }

  function next(): void {
    if (step === 1 && !detectData && !detectLoading) {
      void loadDetect();
    }
    step = Math.min(steps.length - 1, step + 1);
  }

  function back(): void {
    step = Math.max(0, step - 1);
  }

  async function finish(): Promise<void> {
    next(); // go to step 3 (Done)
    saving = true;
    saveError = null;
    try {
      await postWizardComplete(summaryModel, titleModel);
    } catch (e) {
      saveError = e instanceof Error ? e.message : 'Failed to save settings';
    } finally {
      saving = false;
    }
  }

  function handleNext(): void {
    if (step === 0) {
      next();
      void loadDetect();
    } else if (step === 2) {
      void finish();
    } else {
      next();
    }
  }
</script>

<svelte:head>
  <title>klyne — Setup Wizard</title>
</svelte:head>

<div style="min-height: calc(100vh - var(--ad-nav-h)); display: grid; place-items: center; padding: 24px;">
  <div class="ad-card" style="width: min(560px, 100%); padding: 0; background: var(--ad-bg-2);">
    <!-- Step rail -->
    <div style="display: flex; padding: 16px 20px; border-bottom: 1px solid var(--ad-border-soft); gap: 4px;">
      {#each steps as s, i}
        <div style="flex: 1; display: flex; align-items: center; gap: 8px;">
          <span style="width: 18px; height: 18px; border-radius: 50%; background: {i <= step ? 'var(--ad-fg)' : 'var(--ad-border)'}; color: {i <= step ? 'var(--ad-bg)' : 'var(--ad-muted)'}; display: grid; place-items: center; font-size: 10px; font-weight: 700; font-family: var(--ad-font-mono); flex-shrink: 0;">{i + 1}</span>
          <span style="font-size: 12px; color: {i === step ? 'var(--ad-fg)' : 'var(--ad-muted)'}; font-weight: {i === step ? 600 : 400};">{s}</span>
          {#if i < steps.length - 1}
            <span style="flex: 1; height: 1px; background: var(--ad-border);"></span>
          {/if}
        </div>
      {/each}
    </div>

    <!-- Step content -->
    <div style="padding: 28px 28px 16px; min-height: 280px;">
      {#if step === 0}
        <h2 style="font-size: 22px; font-weight: 600; margin: 0 0 8px; letter-spacing: -0.01em;">Welcome to klyne</h2>
        <p class="ad-muted" style="font-size: 13px; margin-bottom: 20px;">
          One window for every Claude and Codex session on your machine. Read JSONLs, search across them, recover from /compact.
        </p>
        <ul style="padding-left: 18px; font-size: 13px; color: var(--ad-fg-2); line-height: 1.7; margin: 0;">
          <li>No cloud sync — everything stays local in <span class="ad-mono">~/.klyne</span>.</li>
          <li>Bring your own API keys; we'll detect them in the next step.</li>
          <li>Live updates over SSE as new messages stream in.</li>
        </ul>
      {/if}

      {#if step === 1}
        <h2 style="font-size: 18px; font-weight: 600; margin: 0 0 12px;">What we found</h2>
        {#if detectLoading}
          <div style="color: var(--ad-muted); font-size: 13px;">Detecting…</div>
        {:else if detectError}
          <div style="color: var(--ad-error); font-size: 13px; margin-bottom: 12px;">{detectError}</div>
        {:else if detectData}
          {@const rows = [
            ['claude', '~/.claude/projects/', detectData.connectors.claude_ok, detectData.connectors.claude_ok ? 'connected' : 'not found'],
            ['codex',  '~/.codex/sessions/',  detectData.connectors.codex_ok,  detectData.connectors.codex_ok  ? 'connected' : 'not found'],
            ['ANTHROPIC_API_KEY', 'shell env', detectData.providers.anthropic, detectData.providers.anthropic ? 'set' : 'not set'],
            ['OPENAI_API_KEY', 'env or ~/.codex/auth', detectData.providers.openai, detectData.providers.openai ? 'set' : 'not set'],
            ['Ollama', 'localhost:11434', detectData.providers.ollama, detectData.providers.ollama ? 'reachable' : 'not reachable'],
          ]}
          <div class="ad-stack" style="gap: 6px;">
            {#each rows as [n, src, ok, status]}
              <div style="display: flex; align-items: center; gap: 10px; padding: 8px 10px; border: 1px solid var(--ad-border-soft); border-radius: 4px;">
                <span class="ad-dot ad-dot--{ok ? 'active' : 'idle'}"></span>
                <span class="ad-mono" style="font-size: 12px; font-weight: 500; flex: 1;">{n}</span>
                <span class="ad-mono ad-faint" style="font-size: 11px;">{src}</span>
                <span class="ad-badge {ok ? 'ad-badge--active' : 'ad-badge--idle'}">{status}</span>
              </div>
            {/each}
          </div>
        {:else}
          <div style="color: var(--ad-muted); font-size: 13px;">Run detection to see results.</div>
        {/if}
      {/if}

      {#if step === 2}
        <h2 style="font-size: 18px; font-weight: 600; margin: 0 0 4px;">Pick models for background tasks</h2>
        <p class="ad-muted" style="font-size: 12px; margin-bottom: 16px;">
          Defaults are recommendations. You can change these later in Settings.
        </p>
        {#each [
          { task: 'Summarize', model: summaryModel, setter: (v: string) => { summaryModel = v; }, hint: 'fast & cheap; runs after every message' },
          { task: 'Title',     model: titleModel,   setter: (v: string) => { titleModel = v; },   hint: 'names new sessions' },
          { task: 'Restore',   model: restoreModel, setter: (v: string) => { restoreModel = v; }, hint: 'rebuilds context after /compact' },
        ] as t}
          <div style="margin-bottom: 12px;">
            <div style="display: flex; justify-content: space-between; margin-bottom: 4px;">
              <span style="font-weight: 500; font-size: 13px;">{t.task}</span>
              <span class="ad-badge ad-badge--active">recommended</span>
            </div>
            <select
              class="ad-input"
              value={t.model}
              onchange={(e) => t.setter((e.target as HTMLSelectElement).value)}
              style="margin-bottom: 4px;"
            >
              <option value="claude-haiku-4-5">claude-haiku-4-5</option>
              <option value="claude-sonnet-4-6">claude-sonnet-4-6</option>
              <option value="claude-opus-4-6">claude-opus-4-6</option>
              <option value="claude-opus-4-7">claude-opus-4-7</option>
              <option value="gpt-4o-mini">gpt-4o-mini</option>
            </select>
            <div class="ad-faint" style="font-size: 11px;">{t.hint}</div>
          </div>
        {/each}
      {/if}

      {#if step === 3}
        <div style="text-align: center; padding: 24px 0;">
          {#if saving}
            <div style="color: var(--ad-muted); font-size: 13px;">Saving your configuration…</div>
          {:else if saveError}
            <div style="color: var(--ad-error); font-size: 13px; margin-bottom: 12px;">{saveError}</div>
            <button class="ad-btn ad-btn--ghost" onclick={() => void finish()}>Retry</button>
          {:else}
            <div style="width: 56px; height: 56px; border-radius: 50%; background: var(--ad-active-bg); color: var(--ad-active); display: grid; place-items: center; font-size: 28px; margin: 0 auto 16px;">✓</div>
            <h2 style="font-size: 20px; font-weight: 600; margin: 0 0 8px;">You're set up</h2>
            <p class="ad-muted" style="font-size: 13px; margin: 0 0 20px;">
              klyne is now watching your projects. New sessions will appear automatically.
            </p>
          {/if}
        </div>
      {/if}
    </div>

    <!-- Footer navigation -->
    <div style="padding: 12px 20px; border-top: 1px solid var(--ad-border-soft); display: flex; justify-content: space-between;">
      <button
        class="ad-btn ad-btn--ghost"
        onclick={back}
        disabled={step === 0}
        style="opacity: {step === 0 ? 0.4 : 1};"
      >← Back</button>
      <div style="display: flex; gap: 8px;">
        <button class="ad-btn ad-btn--ghost" onclick={() => goto('/')}>Skip</button>
        {#if step < 3}
          <button class="ad-btn ad-btn--primary" onclick={handleNext}>
            {step === 2 ? 'Finish setup' : 'Continue'} →
          </button>
        {:else}
          <button class="ad-btn ad-btn--primary" onclick={() => goto('/')}>Open dashboard →</button>
        {/if}
      </div>
    </div>
  </div>
</div>
