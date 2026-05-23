<!--
  RunStrip — copy-command + run/stop button + live elapsed readout.
  Extracted from WorklogCard to keep that file under 300 lines.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import Icon from '$lib/dashboard/Icon.svelte';
  import { runReflect, type ReflectRunResponse } from '$lib/api.js';

  interface Props {
    name: string;
    path: string;
    refreshCmd: string;
    onReflected?: () => void;
  }
  const { name, path, refreshCmd, onReflected }: Props = $props();

  let copied = $state(false);
  let running = $state(false);
  let elapsed = $state(0);
  let runResult = $state<ReflectRunResponse | null>(null);
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;
  let abortController: AbortController | null = null;

  function clearTimer(): void {
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
  }

  function copyCmd(): void {
    void navigator.clipboard.writeText(refreshCmd).then(() => {
      copied = true;
      setTimeout(() => { copied = false; }, 2000);
    });
  }

  async function startRun(): Promise<void> {
    if (running) return;
    running = true;
    elapsed = 0;
    runResult = null;
    abortController = new AbortController();
    elapsedTimer = setInterval(() => { elapsed += 1; }, 1000);
    try {
      const r = await runReflect(path, abortController.signal);
      runResult = r;
      if (r.status === 'ok') {
        onReflected?.();
      }
    } catch (e: unknown) {
      const aborted = e instanceof DOMException && e.name === 'AbortError';
      runResult = {
        project_path: path,
        status: aborted ? 'timeout' : 'error',
        output: '',
        duration_ms: elapsed * 1000,
        error: aborted
          ? 'stopped by user — subprocess killed'
          : (e instanceof Error ? e.message : 'request failed'),
      };
    } finally {
      running = false;
      abortController = null;
      clearTimer();
    }
  }

  function stopRun(): void { abortController?.abort(); }

  onDestroy(() => {
    abortController?.abort();
    clearTimer();
  });
</script>

<div class="cmd-row">
  <code class="cmd mono">{refreshCmd}</code>
  <button class="k-btn" onclick={copyCmd} aria-label={`Copy reflect command for ${name}`}>
    <Icon name="copy" size={13} />
    {copied ? 'copied' : 'copy'}
  </button>
  {#if running}
    <button class="k-btn stop-btn" onclick={stopRun} aria-label={`Stop /klyne:reflect for ${name}`}>✖ stop</button>
  {:else}
    <button class="k-btn run-btn" onclick={startRun} aria-label={`Run /klyne:reflect for ${name}`}>▶ run</button>
  {/if}
</div>

{#if running}
  <div class="run-live" aria-live="polite">
    <div class="live-head-row">
      <span class="mono warn-text">
        <span class="spinner-dots" aria-hidden="true">···</span>
        Running on the daemon
      </span>
      <span class="mono dim elapsed">· {elapsed}s elapsed</span>
    </div>
    <div class="live-cmd mono">
      <span class="prompt-sym">$ </span>{refreshCmd}
    </div>
    <div class="mono dim live-note">
      Hit <span class="alert-text">✖ stop</span> to kill the subprocess (SIGKILL via context cancel).
      A 5-minute server-side timeout also applies.
    </div>
  </div>
{/if}

{#if runResult && !running}
  <div class="run-result" class:result-ok={runResult.status === 'ok'} class:result-err={runResult.status !== 'ok'}>
    <span class="mono dim">
      {runResult.status === 'ok' ? '✓ reflected' : runResult.status === 'timeout' ? '⏱ stopped' : '✗ failed'}
      · {(runResult.duration_ms / 1000).toFixed(1)}s
      {#if runResult.error} · <span class="err-text">{runResult.error}</span>{/if}
    </span>
    {#if runResult.output}
      <pre class="run-output mono">{runResult.output}</pre>
    {/if}
  </div>
{/if}

<style>
  .cmd-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .cmd {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 6px;
    padding: 8px 12px;
    font-size: 11.5px;
    color: var(--fg-soft, var(--fg-muted));
  }

  .stop-btn {
    background: color-mix(in oklch, var(--alert, #e05b5b) 18%, var(--bg-card-2));
    border-color: color-mix(in oklch, var(--alert, #e05b5b) 45%, transparent);
    color: var(--alert, #e05b5b);
  }
  .run-btn {
    background: color-mix(in oklch, var(--info, #5b9bd5) 14%, var(--bg-card-2));
    border-color: color-mix(in oklch, var(--info, #5b9bd5) 40%, transparent);
    color: var(--info, #5b9bd5);
  }

  /* Running readout */
  .run-live {
    background: var(--bg-inset);
    border: 1px solid color-mix(in oklch, var(--warn) 35%, var(--border-hair));
    border-left: 3px solid var(--warn);
    border-radius: 8px;
    padding: 12px 14px;
    margin-top: 4px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .live-head-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .warn-text { color: var(--warn); font-size: 12px; }
  .elapsed { font-size: 11px; }
  .spinner-dots {
    display: inline-block;
    animation: blink 0.8s step-start infinite;
    letter-spacing: 1px;
  }
  @keyframes blink {
    0%   { opacity: 1; }
    33%  { opacity: 0.4; }
    66%  { opacity: 0.7; }
    100% { opacity: 1; }
  }

  .live-cmd {
    background: var(--bg);
    border: 1px solid var(--border-hair);
    border-radius: 6px;
    padding: 8px 12px;
    font-size: 11px;
    color: var(--fg-soft, var(--fg-muted));
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .prompt-sym { color: var(--fg-dim, var(--fg-muted)); }
  .live-note {
    font-size: 10.5px;
    line-height: 1.55;
  }
  .alert-text { color: var(--alert, #e05b5b); }

  /* Run result */
  .run-result {
    padding: 8px 12px;
    border-radius: 6px;
    border-left: 3px solid var(--border-hair);
    background: var(--bg-inset);
    font-size: 12px;
  }
  .result-ok  { border-left-color: var(--ok); }
  .result-err { border-left-color: var(--alert, #e05b5b); }
  .err-text { color: var(--alert, #e05b5b); }
  .run-output {
    margin: 6px 0 0;
    padding: 8px 10px;
    background: var(--bg);
    border-radius: 4px;
    font-size: 11px;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 260px;
    overflow-y: auto;
    color: var(--fg-soft, var(--fg-muted));
  }

  /* Shared */
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .dim  { color: var(--fg-dim, var(--fg-muted)); opacity: 0.75; }
</style>
