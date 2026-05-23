<!--
  WorklogCard — per-project reflection rollup card.
  Matches page-worklog.jsx WorklogCard design:
    - 3px left rail in state colour
    - Header: name + state pill + ago + path
    - Latest reflection panel (if any): date, evidence, bullets, shipped, open loops
    - Stale/cold only: refresh command strip with copy + run/stop + live elapsed counter
    - All cards: "See full →" drill-in link
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from '$lib/dashboard/Icon.svelte';
  import { runReflect, type ReflectRunResponse } from '$lib/api.js';
  import type { WorklogProjectRollup, Reflection } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  interface Props {
    w: WorklogProjectRollup;
    onReflected?: () => void; // called after a successful reflect so parent can refresh
  }

  const { w, onReflected }: Props = $props();

  // --- State colour ---
  const tone = $derived(
    w.stale && w.latest_reflection ? 'var(--warn)' :
    !w.latest_reflection            ? 'var(--fg-muted)' :
                                      'var(--ok)'
  );

  // cardState derives a simpler label from the rollup shape
  const cardState = $derived<'stale' | 'cold' | 'fresh'>(
    w.stale && w.latest_reflection ? 'stale' :
    !w.latest_reflection            ? 'cold' :
                                      'fresh'
  );

  // --- State badge text ---
  const stateLabel = $derived(() => {
    const n = w.pending_entries;
    const ent = n === 1 ? 'entry' : 'entries';
    if (cardState === 'stale') return `${n} new ${ent} since reflection`;
    if (cardState === 'cold')  return `${n} ${ent} · no reflection yet`;
    return 'fresh';
  });

  // --- Reflect command ---
  function reflectCmd(path: string): string {
    return `cd "${path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'`;
  }
  const cmd = $derived(reflectCmd(w.project_path));

  // --- Copy ---
  let copied = $state(false);
  async function copyCmd(): Promise<void> {
    try {
      await navigator.clipboard.writeText(cmd);
      copied = true;
      setTimeout(() => { copied = false; }, 2000);
    } catch {
      // Clipboard denied — cmd visible on screen
    }
  }

  // --- Run / Stop plumbing (per-card AbortController + elapsed timer) ---
  let running = $state(false);
  let elapsed = $state(0);
  let runResult = $state<ReflectRunResponse | null>(null);
  let abortController: AbortController | null = null;
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;

  function clearTimer(): void {
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
  }

  onMount(() => {
    return () => { clearTimer(); abortController?.abort(); };
  });

  async function startRun(): Promise<void> {
    if (running) return;
    running = true;
    elapsed = 0;
    runResult = null;
    abortController = new AbortController();
    elapsedTimer = setInterval(() => { elapsed += 1; }, 1000);
    try {
      const r = await runReflect(w.project_path, abortController.signal);
      runResult = r;
      if (r.status === 'ok') {
        onReflected?.();
      }
    } catch (e: unknown) {
      const aborted = e instanceof DOMException && e.name === 'AbortError';
      runResult = {
        project_path: w.project_path,
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

  function stopRun(): void {
    abortController?.abort();
  }

  // --- Reflection helpers ---
  function reflectionAge(r: Reflection | null): string {
    if (!r) return 'never reflected';
    return `reflected ${relTime(r.ts)}`;
  }

  // Parse bullets from body_md: split on newline, filter lines starting with "- " or "* "
  function parseBullets(r: Reflection): string[] {
    return r.body_md
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l.startsWith('- ') || l.startsWith('* '))
      .map((l) => l.slice(2).trim());
  }

  // Parse shipped/open-loops from body_md: look for headings then following bullets
  function parseSection(r: Reflection, heading: string): string[] {
    const lines = r.body_md.split('\n').map((l) => l.trim());
    const idx = lines.findIndex((l) =>
      l.toLowerCase().includes(heading.toLowerCase()) && (l.startsWith('#') || l.startsWith('**'))
    );
    if (idx === -1) return [];
    const items: string[] = [];
    for (let i = idx + 1; i < lines.length; i++) {
      const l = lines[i];
      if (!l) continue;
      if (l.startsWith('#') || (l.startsWith('**') && !l.startsWith('- '))) break;
      if (l.startsWith('- ') || l.startsWith('* ')) items.push(l.slice(2).trim());
    }
    return items;
  }

  // Title derived from reflection
  function reflectionDate(r: Reflection): string {
    // ts is epoch-ms; format as YYYY-MM-DD
    return new Date(r.ts).toISOString().slice(0, 10);
  }

  function evidenceLabel(r: Reflection): string {
    const n = r.evidence_entry_ids.length;
    return `${n} evidence · tier ${r.tier} · ${r.summary_source}`;
  }
</script>

<div class="wcard" style="border-left-color: {tone}">
  <!-- Header -->
  <div class="wcard-head" class:has-body={!!w.latest_reflection}>
    <div class="wcard-head-left">
      <div class="name-row">
        <h3 class="proj-name">{w.name}</h3>
        {#if cardState === 'stale'}
          <span class="pill pill-warn">{stateLabel()}</span>
        {:else if cardState === 'cold'}
          <span class="pill">{stateLabel()}</span>
        {:else}
          <span class="pill pill-ok">fresh</span>
        {/if}
      </div>
      <div class="meta mono dim">
        {reflectionAge(w.latest_reflection)}
        {#if w.latest_entry_ts > 0} · last activity {relTime(w.latest_entry_ts)}{/if}
      </div>
      <div class="path mono">{w.project_path}</div>
    </div>
  </div>

  <!-- Latest reflection body -->
  {#if w.latest_reflection}
    {@const r = w.latest_reflection}
    {@const bullets = parseBullets(r)}
    {@const shipped = parseSection(r, 'shipped')}
    {@const openLoops = parseSection(r, 'open loop')}
    <div class="reflection-body">
      <div class="ref-title-row">
        <h4 class="ref-title">Daily reflection · <span class="mono">{reflectionDate(r)}</span></h4>
        <span class="mono dim evidence">{evidenceLabel(r)}</span>
      </div>
      <div class="ref-card">
        {#if bullets.length > 0}
          <ul class="bullets">
            {#each bullets as b}
              <li>{b}</li>
            {/each}
          </ul>
        {:else}
          <!-- Fallback: show raw body_md lines as plain text when no bullets parsed -->
          <p class="body-raw">{r.body_md}</p>
        {/if}

        {#if shipped.length > 0 || openLoops.length > 0}
          <div class="sections">
            {#if shipped.length > 0}
              <div class="section">
                <div class="kicker ok-kicker">Shipped</div>
                {#each shipped as s}
                  <div class="mono section-line">· {s}</div>
                {/each}
              </div>
            {/if}
            {#if openLoops.length > 0}
              <div class="section">
                <div class="kicker warn-kicker">Open loops</div>
                {#each openLoops as s}
                  <div class="mono section-line">· {s}</div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Refresh strip (stale/cold only) -->
  {#if cardState !== 'fresh'}
    <div class="refresh-strip" class:has-top={!!w.latest_reflection}>
      <div class="kicker strip-kicker">
        {cardState === 'cold' ? 'Generate the first reflection:' : 'Refresh with the latest entries:'}
      </div>
      <div class="cmd-row">
        <code class="cmd mono">{cmd}</code>
        <button class="k-btn" onclick={copyCmd}>
          <Icon name="copy" size={13} />
          {copied ? 'copied' : 'copy'}
        </button>
        {#if running}
          <button
            class="k-btn stop-btn"
            onclick={stopRun}
          >
            ✖ stop
          </button>
        {:else}
          <button
            class="k-btn run-btn"
            onclick={startRun}
          >
            ▶ run
          </button>
        {/if}
      </div>

      <!-- Running readout -->
      {#if running}
        <div class="run-live">
          <div class="live-head-row">
            <span class="mono warn-text">
              <span class="spinner-dots" aria-hidden="true">···</span>
              Running on the daemon
            </span>
            <span class="mono dim elapsed">· {elapsed}s elapsed</span>
          </div>
          <div class="live-cmd mono">
            <span class="prompt-sym">$ </span>{cmd}
          </div>
          <div class="mono dim live-note">
            Hit <span class="alert-text">✖ stop</span> to kill the subprocess (SIGKILL via context cancel).
            A 5-minute server-side timeout also applies.
          </div>
        </div>
      {/if}

      <!-- Run result (after completion) -->
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
    </div>
  {/if}

  <!-- See full link — always visible -->
  <div class="card-foot">
    <a class="see-full" href="/worklog/project?path={encodeURIComponent(w.project_path)}">
      See full →
    </a>
  </div>
</div>

<style>
  .wcard {
    background: var(--bg-card-2, var(--bg-card));
    border: 1px solid var(--border-hair);
    border-left-width: 3px;
    border-radius: 10px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  /* ── Header ── */
  .wcard-head {
    padding: 14px 18px;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }
  .wcard-head.has-body {
    border-bottom: 1px solid var(--border-hair);
  }
  .wcard-head-left { min-width: 0; }

  .name-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 4px;
    flex-wrap: wrap;
  }
  .proj-name {
    margin: 0;
    font-size: 16px;
    font-weight: 500;
    color: var(--fg);
    letter-spacing: -0.01em;
  }

  /* Pills */
  .pill {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    background: var(--bg-inset);
    color: var(--fg-muted);
    border: 1px solid var(--border-hair);
  }
  .pill-warn {
    background: color-mix(in oklch, var(--warn) 12%, var(--bg-card-2));
    color: var(--warn);
    border-color: color-mix(in oklch, var(--warn) 30%, transparent);
  }
  .pill-ok {
    background: color-mix(in oklch, var(--ok) 12%, var(--bg-card-2));
    color: var(--ok);
    border-color: color-mix(in oklch, var(--ok) 30%, transparent);
  }

  .meta {
    font-size: 11px;
    margin-bottom: 2px;
  }
  .path {
    font-size: 11px;
    color: var(--fg-muted);
    margin-top: 4px;
    word-break: break-all;
  }

  /* ── Reflection body ── */
  .reflection-body {
    padding: 16px 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .ref-title-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
  }
  .ref-title {
    margin: 0;
    font-size: 13.5px;
    font-weight: 500;
    color: var(--fg);
  }
  .evidence {
    font-size: 10.5px;
  }

  .ref-card {
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    padding: 14px 16px;
    font-size: 12.5px;
    color: var(--fg-soft, var(--fg-muted));
    line-height: 1.6;
  }

  .bullets {
    margin: 0;
    padding-left: 18px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .bullets li { margin-bottom: 2px; }

  .body-raw {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
  }

  .sections {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px dashed var(--border-soft, var(--border-hair));
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .section { display: flex; flex-direction: column; gap: 4px; }
  .kicker {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    margin-bottom: 2px;
  }
  .ok-kicker   { color: var(--ok); }
  .warn-kicker { color: var(--warn); }
  .section-line {
    font-size: 11.5px;
    color: var(--fg-soft, var(--fg-muted));
    line-height: 1.55;
  }

  /* ── Refresh strip ── */
  .refresh-strip {
    padding: 12px 18px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .refresh-strip.has-top {
    border-top: 1px dashed var(--border-soft, var(--border-hair));
  }
  .strip-kicker {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }

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

  /* ── Card foot ── */
  .card-foot {
    padding: 8px 18px 12px;
    display: flex;
    justify-content: flex-end;
  }
  .see-full {
    font-size: 12px;
    color: var(--fg-muted);
    text-decoration: none;
    opacity: 0.75;
    transition: opacity 0.15s;
  }
  .see-full:hover { opacity: 1; text-decoration: underline; }

  /* ── Shared ── */
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .dim  { color: var(--fg-dim, var(--fg-muted)); opacity: 0.75; }
</style>
