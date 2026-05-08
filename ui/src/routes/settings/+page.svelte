<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchSettings, updateSettings, fetchWizardDetect } from '$lib/api.js';
  import type { SettingsResponse, WizardDetectResponse } from '$lib/types.js';

  let settings = $state<SettingsResponse | null>(null);
  let detectData = $state<WizardDetectResponse | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let saveError = $state<string | null>(null);
  let saved = $state(false);
  let tab = $state<'providers' | 'tasks' | 'connectors' | 'about'>('providers');

  const tabs: [string, string][] = [
    ['providers', 'Providers'],
    ['tasks', 'Tasks'],
    ['connectors', 'Connectors'],
    ['about', 'About'],
  ];

  // Editable task model values
  let summaryModel = $state('claude-haiku-4-5');
  let titleModel = $state('claude-haiku-4-5');
  let claudeEnabled = $state(true);
  let codexEnabled = $state(true);

  function parseModel(s: string): { provider: string; model: string } {
    if (!s || s === 'auto') return { provider: '', model: 'auto' };
    const idx = s.indexOf(':');
    if (idx > 0) return { provider: s.slice(0, idx), model: s.slice(idx + 1) };
    return { provider: '', model: s };
  }

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      const [s, d] = await Promise.all([fetchSettings(), fetchWizardDetect()]);
      settings = s;
      detectData = d;
      const ai = s.ai;
      summaryModel = ai.summary_model.model || 'claude-haiku-4-5';
      titleModel = ai.title_model.model || 'claude-haiku-4-5';
      claudeEnabled = d.connectors.claude_ok;
      codexEnabled = d.connectors.codex_ok;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load settings';
    } finally {
      loading = false;
    }
  }

  async function save(): Promise<void> {
    if (!settings) return;
    saving = true;
    saveError = null;
    saved = false;
    try {
      await updateSettings({
        ai: {
          summary_model: parseModel(summaryModel),
          title_model: parseModel(titleModel),
          embed_model: settings.ai.embed_model,
        },
      });
      saved = true;
      setTimeout(() => { saved = false; }, 3000);
    } catch (e) {
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
  <title>klyne — Settings</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 880px;">
  <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0 0 16px;">Settings</h1>

  <!-- Tab bar -->
  <div style="display: flex; gap: 2px; border-bottom: 1px solid var(--ad-border); margin-bottom: 20px;">
    {#each tabs as [id, label]}
      <button
        onclick={() => { tab = id as typeof tab; }}
        style="padding: 10px 14px; font-size: 13px; font-weight: 500; color: {tab === id ? 'var(--ad-fg)' : 'var(--ad-muted)'}; border-bottom: {tab === id ? '2px solid var(--ad-claude)' : '2px solid transparent'}; margin-bottom: -1px;"
      >{label}</button>
    {/each}
  </div>

  {#if loading}
    <div style="color: var(--ad-muted); font-size: 13px; padding: 24px; text-align: center;">Loading settings…</div>
  {:else if error}
    <div style="color: var(--ad-error); font-size: 13px; padding: 24px; text-align: center;">{error} <button class="ad-btn ad-btn--ghost ad-btn--sm" onclick={() => void load()}>Retry</button></div>
  {:else}

    <!-- Providers tab -->
    {#if tab === 'providers'}
      {@const detected = settings?.detected ?? { anthropic: false, openai: false, gemini: false, ollama: false }}
      {@const providers = [
        { name: 'Anthropic API', env: 'ANTHROPIC_API_KEY', detected: detected.anthropic, reason: detected.anthropic ? 'Found in shell env' : 'Not set' },
        { name: 'OpenAI API',    env: 'OPENAI_API_KEY',    detected: detected.openai,    reason: detected.openai ? 'Found in env or ~/.codex/auth' : 'Not set' },
        { name: 'Ollama (local)',env: '—',                 detected: detected.ollama,    reason: detected.ollama ? 'Reachable at http://localhost:11434' : 'Not reachable' },
        { name: 'Gemini',        env: 'GEMINI_API_KEY',    detected: detected.gemini,    reason: detected.gemini ? 'Found in env' : 'Not set' },
      ]}
      <div class="ad-card" style="overflow: hidden;">
        {#each providers as p, i}
          <div style="display: flex; align-items: center; gap: 12px; padding: 12px 16px; border-bottom: {i < providers.length - 1 ? '1px solid var(--ad-border-soft)' : 'none'};">
            <span class="ad-dot ad-dot--{p.detected ? 'active' : 'idle'}"></span>
            <div style="flex: 1;">
              <div style="font-weight: 500; font-size: 13px;">{p.name}</div>
              <div class="ad-mono ad-muted" style="font-size: 11px;">{p.env} · {p.reason}</div>
            </div>
            <span class="ad-badge {p.detected ? 'ad-badge--active' : 'ad-badge--idle'}">
              {p.detected ? 'detected' : 'not set'}
            </span>
            <button class="ad-btn ad-btn--ghost ad-btn--sm">docs ↗</button>
          </div>
        {/each}
      </div>
    {/if}

    <!-- Tasks tab -->
    {#if tab === 'tasks'}
      {@const tasks = [
        { task: 'Summarize', recommended: 'claude-haiku-4-5', current: summaryModel, setter: (v: string) => { summaryModel = v; } },
        { task: 'Title',     recommended: 'claude-haiku-4-5', current: titleModel,   setter: (v: string) => { titleModel = v; } },
      ]}
      <div class="ad-card" style="overflow: hidden;">
        {#each tasks as t, i}
          <div style="display: grid; grid-template-columns: 120px 1fr 1fr 80px; align-items: center; gap: 12px; padding: 14px 16px; border-bottom: {i < tasks.length - 1 ? '1px solid var(--ad-border-soft)' : 'none'};">
            <span style="font-weight: 500; font-size: 13px;">{t.task}</span>
            <div>
              <div class="ad-mono" style="font-size: 12px; color: var(--ad-muted); margin-bottom: 2px;">recommended</div>
              <div class="ad-mono" style="font-size: 12px;">{t.recommended}</div>
            </div>
            <div>
              <div class="ad-mono" style="font-size: 12px; color: var(--ad-muted); margin-bottom: 2px;">current</div>
              <select
                class="ad-input"
                style="height: 26px; font-size: 12px;"
                value={t.current}
                onchange={(e) => t.setter((e.target as HTMLSelectElement).value)}
              >
                <option value="claude-haiku-4-5">claude-haiku-4-5</option>
                <option value="claude-sonnet-4-6">claude-sonnet-4-6</option>
                <option value="claude-opus-4-6">claude-opus-4-6</option>
                <option value="claude-opus-4-7">claude-opus-4-7</option>
                <option value="gpt-4o-mini">gpt-4o-mini</option>
              </select>
            </div>
            <span class="ad-badge {t.recommended === t.current ? 'ad-badge--active' : 'ad-badge--compact'}">
              {t.recommended === t.current ? 'ok' : 'drift'}
            </span>
          </div>
        {/each}
      </div>
      <div style="display: flex; gap: 8px; margin-top: 16px; align-items: center;">
        <button
          class="ad-btn ad-btn--primary"
          onclick={() => void save()}
          disabled={saving}
        >{saving ? 'Saving…' : 'Save settings'}</button>
        {#if saved}<span style="font-size: 13px; color: var(--ad-active);">✓ Saved</span>{/if}
        {#if saveError}<span style="font-size: 13px; color: var(--ad-error);">{saveError}</span>{/if}
      </div>
    {/if}

    <!-- Connectors tab -->
    {#if tab === 'connectors'}
      {@const conns = [
        { name: 'claude' as const, path: detectData?.connectors.claude_root ?? '~/.claude/projects/', count: null, enabled: claudeEnabled, setter: (v: boolean) => { claudeEnabled = v; } },
        { name: 'codex' as const,  path: detectData?.connectors.codex_root  ?? '~/.codex/sessions/',  count: null, enabled: codexEnabled,  setter: (v: boolean) => { codexEnabled = v; } },
      ]}
      <div class="ad-card" style="overflow: hidden;">
        {#each conns as c, i}
          <div style="display: flex; align-items: center; gap: 12px; padding: 14px 16px; border-bottom: {i < conns.length - 1 ? '1px solid var(--ad-border-soft)' : 'none'};">
            <span class="ad-badge ad-badge--{c.name}">{c.name}</span>
            <div style="flex: 1;">
              <div class="ad-mono" style="font-size: 12px;">{c.path}</div>
              <div class="ad-muted" style="font-size: 11px;">scanning every 5s</div>
            </div>
            <!-- Toggle -->
            <button
              onclick={() => c.setter(!c.enabled)}
              style="width: 34px; height: 18px; border-radius: 10px; background: {c.enabled ? 'var(--ad-active-bg)' : 'var(--ad-border)'}; position: relative; border: 1px solid var(--ad-border);"
            >
              <span style="position: absolute; top: 1px; left: {c.enabled ? '17px' : '1px'}; width: 14px; height: 14px; border-radius: 50%; background: {c.enabled ? 'var(--ad-active)' : 'var(--ad-muted)'}; transition: left 120ms;"></span>
            </button>
          </div>
        {/each}
      </div>
    {/if}

    <!-- About tab -->
    {#if tab === 'about'}
      <div class="ad-card" style="padding: 16px;">
        <table style="width: 100%; font-size: 13px;">
          <tbody>
            {#each [
              ['Version', '1.1.0'],
              ['Schema version', '7'],
              ['DB path', '~/.klyne/state.db'],
              ['Server', '127.0.0.1:7878'],
            ] as [k, v]}
              <tr>
                <td class="ad-muted" style="padding: 6px 0; width: 160px;">{k}</td>
                <td class="ad-mono">{v}</td>
              </tr>
            {/each}
          </tbody>
        </table>
        <div style="display: flex; gap: 8px; margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--ad-border-soft);">
          <button class="ad-btn">View doctor JSON</button>
          <button class="ad-btn ad-btn--ghost">Export DB</button>
        </div>
      </div>
    {/if}

  {/if}
</div>
