<!--
  Worklog v2 — per-project reflection rollup.

  Each project card shows the latest synthesized reflection (klyne's
  worklog_reflections row, written by `/klyne:reflect`). Cards surface
  one of three states:

    stale   — reflection exists, but N new visible entries arrived after it
    cold    — no reflection yet for this project (visible entries waiting)
    fresh   — reflection covers everything; nothing to do

  When stale or cold, the card shows a copy-able command the user can run in
  their own Claude session to (re)trigger reflection synthesis. The UI itself
  cannot trigger the AI synth step — that's by design (no daemon-side LLM call).
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchWorklog, runReflect, type ReflectRunResponse } from '$lib/api.js';
  import type { Reflection, WorklogProjectRollup, WorklogResponse } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  let resp = $state<WorklogResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let copied = $state<string | null>(null); // project_path of last-copied cmd
  // Tracks the project currently running /klyne:reflect via the daemon
  // subprocess endpoint. Used to disable the Run button + show a spinner.
  let running = $state<string | null>(null);
  // Per-project run results so the user can see the output inline without
  // navigating away. Keyed by project_path; cleared on the next refresh.
  let runResults = $state<Record<string, ReflectRunResponse>>({});
  // Active AbortController for the in-flight run, if any. Stop button
  // calls controller.abort() which terminates the fetch, which trips the
  // server's request context, which causes exec.CommandContext to SIGKILL
  // the spawned `claude` subprocess.
  let abortController: AbortController | null = null;
  // Elapsed seconds for the in-flight run, ticked once per second by a
  // setInterval started when the run kicks off.
  let elapsed = $state(0);
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchWorklog();
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load worklog';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  function reflectCmd(projectPath: string): string {
    // Quote the path to handle spaces. --permission-mode bypassPermissions
    // is required so /klyne:reflect can call its MCP tools in -p mode
    // without an interactive approval prompt.
    return `cd "${projectPath}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'`;
  }

  async function copyCmd(projectPath: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(reflectCmd(projectPath));
      copied = projectPath;
      setTimeout(() => { if (copied === projectPath) copied = null; }, 2000);
    } catch {
      // Clipboard may be denied — leave silently; the cmd is visible on screen.
    }
  }

  // runFromBrowser kicks off /klyne:reflect via the daemon's subprocess
  // endpoint. Unlike copyCmd this actually spawns `claude -p` server-side.
  // Output is rendered inline; on success we refresh the worklog so the
  // newly-written reflection row appears in the card body.
  //
  // Single-flight: only one reflection runs at a time so the user always
  // knows which subprocess the Stop button targets.
  async function runFromBrowser(projectPath: string): Promise<void> {
    if (running) return;
    running = projectPath;
    elapsed = 0;
    elapsedTimer = setInterval(() => { elapsed += 1; }, 1000);
    abortController = new AbortController();
    try {
      const r = await runReflect(projectPath, abortController.signal);
      runResults = { ...runResults, [projectPath]: r };
      if (r.status === 'ok') {
        await load();
      }
    } catch (e: unknown) {
      // AbortError is the user clicking Stop — render it as cancelled
      // rather than a failure. DOMException with name='AbortError' is the
      // standard fetch-cancel signal across browsers.
      const aborted = e instanceof DOMException && e.name === 'AbortError';
      runResults = {
        ...runResults,
        [projectPath]: {
          project_path: projectPath,
          status: aborted ? 'timeout' : 'error',
          output: '',
          duration_ms: elapsed * 1000,
          error: aborted ? 'stopped by user — subprocess killed' : (e instanceof Error ? e.message : 'request failed'),
        },
      };
    } finally {
      running = null;
      abortController = null;
      if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
    }
  }

  function stopRun(): void {
    if (!abortController) return;
    abortController.abort();
  }

  // Tier classification for badge text + color.
  function status(p: WorklogProjectRollup): { label: string; klass: 'stale' | 'cold' | 'fresh' } {
    const n = p.pending_entries;
    const ent = n === 1 ? 'entry' : 'entries';
    if (p.stale && p.latest_reflection) return { label: `${n} new ${ent} since reflection`, klass: 'stale' };
    if (!p.latest_reflection) return { label: `${n} ${ent} · no reflection yet`, klass: 'cold' };
    return { label: 'fresh', klass: 'fresh' };
  }

  function reflectionAge(r: Reflection | null): string {
    return r ? `reflected ${relTime(r.ts)}` : 'never reflected';
  }
</script>

<div class="page">
  <header class="head">
    <h1>Worklog</h1>
    <p class="muted">
      Daily reflections per project. When new sessions land on top of the last reflection,
      that project gets flagged stale — run <code>/klyne:reflect</code> in the project to refresh.
    </p>
  </header>

  {#if loading && !resp}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">⚠ {error}</p>
    <p class="muted">Is the klyne daemon running?</p>
  {:else if resp}
    {#if resp.projects.length === 0}
      <p class="muted empty">
        Nothing here yet. As soon as klyne's Stop hook captures meaningful work in any
        project, you'll see it show up here. Then run <code>/klyne:reflect</code> to synthesize.
      </p>
    {:else}
      <p class="counter muted">{resp.projects.length} project{resp.projects.length === 1 ? '' : 's'}</p>
      <div class="list">
        {#each resp.projects as p (p.project_path)}
          {@const s = status(p)}
          <article class="card" class:stale={s.klass === 'stale'} class:cold={s.klass === 'cold'} class:fresh={s.klass === 'fresh'}>
            <header class="card-head">
              <h2><a class="drill-link" href={`/worklog/project?path=${encodeURIComponent(p.project_path)}`}>{p.name}</a></h2>
              <span class="status {s.klass}">{s.label}</span>
            </header>
            <p class="meta muted">
              {reflectionAge(p.latest_reflection)}
              {#if p.latest_entry_ts > 0}· last activity {relTime(p.latest_entry_ts)}{/if}
            </p>
            <p class="path muted small"><code>{p.project_path}</code></p>

            {#if p.latest_reflection}
              <div class="reflection">
                <p class="reflection-title">{p.latest_reflection.title}</p>
                <pre class="body">{p.latest_reflection.body_md}</pre>
                <p class="muted small footer">
                  {p.latest_reflection.evidence_entry_ids.length} evidence
                  · tier {p.latest_reflection.tier}
                  · {p.latest_reflection.summary_source}
                </p>
              </div>
            {/if}

            {#if s.klass === 'stale' || s.klass === 'cold'}
              <div class="cta">
                <p class="muted small">
                  {s.klass === 'cold' ? 'Generate the first reflection:' : 'Refresh with the latest entries:'}
                </p>
                <div class="cmd-row">
                  <code class="cmd">{reflectCmd(p.project_path)}</code>
                  <button class="copy" onclick={() => copyCmd(p.project_path)} disabled={running === p.project_path}>
                    {copied === p.project_path ? '✓ copied' : 'copy'}
                  </button>
                  {#if running === p.project_path}
                    <button
                      class="stop"
                      onclick={stopRun}
                      title="Abort the in-flight `claude` subprocess. The server SIGKILLs the child immediately."
                    >
                      ✖ stop
                    </button>
                  {:else}
                    <button
                      class="run"
                      onclick={() => runFromBrowser(p.project_path)}
                      disabled={running !== null}
                      title="Spawn `claude -p '/klyne:reflect'` in this project. Uses your existing Claude subscription via the local CLI."
                    >
                      ▶ run
                    </button>
                  {/if}
                </div>

                {#if running === p.project_path}
                  <div class="run-live">
                    <p class="live-head">
                      <span class="spinner" aria-hidden="true">⠋</span>
                      <span>Running on the daemon · <span class="mono">{elapsed}s elapsed</span></span>
                    </p>
                    <p class="muted small live-cmd">
                      <span class="kw">$</span> <span class="mono">{reflectCmd(p.project_path)}</span>
                    </p>
                    <p class="muted small">
                      Hit <strong>✖ stop</strong> to kill the subprocess (SIGKILL via context cancel).
                      A 5-minute server-side timeout also applies.
                    </p>
                  </div>
                {/if}

                {#if runResults[p.project_path] && running !== p.project_path}
                  {@const rr = runResults[p.project_path]}
                  <div class="run-result" class:ok={rr.status === 'ok'} class:err={rr.status !== 'ok'}>
                    <p class="muted small">
                      {rr.status === 'ok' ? '✓ reflected' : rr.status === 'timeout' ? '⏱ stopped' : '✗ failed'}
                      · {(rr.duration_ms / 1000).toFixed(1)}s
                      {#if rr.error}· <span class="err-msg">{rr.error}</span>{/if}
                    </p>
                    {#if rr.output}
                      <pre class="output">{rr.output}</pre>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
          </article>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.5rem; max-width: 1100px; margin: 0 auto; }
  .head h1 { margin: 0 0 0.25rem; }
  .muted { color: var(--text-muted, #888); }
  .small { font-size: 0.85em; }
  .error { color: var(--text-error, #c33); }
  .empty { text-align: center; padding: 2rem; }
  .counter { margin: 1rem 0 0.5rem; }

  .list { display: flex; flex-direction: column; gap: 1rem; }

  .card {
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-left-width: 3px;
    border-radius: 6px;
    padding: 1rem 1.25rem;
  }
  .card.stale { border-left-color: #d97a4a; }
  .card.cold  { border-left-color: #888;   }
  .card.fresh { border-left-color: #4a9d5b; }

  .card-head {
    display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap;
  }
  .card-head h2 {
    margin: 0; font-size: 1.1rem;
  }
  .status {
    padding: 0.1rem 0.55rem; border-radius: 999px; font-size: 0.75em;
    text-transform: uppercase; letter-spacing: 0.04em;
  }
  .status.stale { background: #3a2818; color: #e6a878; }
  .status.cold  { background: #232323; color: #aaa; }
  .status.fresh { background: #1e3a1e; color: #8ec98e; }

  .meta { margin: 0.25rem 0; font-size: 0.9em; }
  .path { margin: 0 0 0.5rem; font-family: monospace; font-size: 0.8em; word-break: break-all; }

  .reflection { margin: 0.75rem 0 0; }
  .reflection-title {
    margin: 0 0 0.35rem; font-weight: 600;
    font-size: 0.95em; color: var(--text, #ddd);
  }

  .body {
    white-space: pre-wrap; word-break: break-word;
    background: var(--surface-2, #0d0d0d);
    padding: 0.75rem 1rem; border-radius: 4px;
    font-size: 0.9em; margin: 0;
    line-height: 1.6;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  }
  .footer { margin: 0.4rem 0 0; }

  .cta {
    margin-top: 0.75rem; padding-top: 0.75rem;
    border-top: 1px dashed var(--border, #2a2a2a);
  }
  .cta p { margin: 0 0 0.4rem; }
  .cmd-row {
    display: flex; align-items: center; gap: 0.5rem;
  }
  .cmd {
    flex: 1; padding: 0.5rem 0.75rem;
    background: var(--surface-2, #0d0d0d);
    border-radius: 4px; font-size: 0.85em;
    word-break: break-all;
  }
  .copy {
    padding: 0.4rem 0.75rem;
    background: var(--surface-2, #0d0d0d);
    border: 1px solid var(--border, #2a2a2a);
    color: var(--text, #ddd); cursor: pointer; border-radius: 4px;
    font-size: 0.85em; white-space: nowrap;
  }
  .copy:hover { background: var(--border, #2a2a2a); }

  .run {
    padding: 0.4rem 0.85rem;
    background: #2a3f5b;
    border: 1px solid #3d5a82;
    color: #cfe2ff; cursor: pointer; border-radius: 4px;
    font-size: 0.85em; white-space: nowrap;
    font-weight: 500;
  }
  .run:hover:not(:disabled) { background: #344a6b; }
  .run:disabled { opacity: 0.55; cursor: progress; }

  .stop {
    padding: 0.4rem 0.85rem;
    background: #5b2a2a; border: 1px solid #823d3d;
    color: #ffd1cf; cursor: pointer; border-radius: 4px;
    font-size: 0.85em; white-space: nowrap; font-weight: 500;
  }
  .stop:hover { background: #6b3434; }

  .run-live {
    margin-top: 0.6rem; padding: 0.6rem 0.85rem;
    background: var(--surface-2, #0d0d0d);
    border-left: 3px solid #d97a4a;
    border-radius: 4px;
  }
  .run-live .live-head {
    margin: 0 0 0.35rem; display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.9em; color: #e6a878;
  }
  .run-live .live-cmd {
    margin: 0.25rem 0; padding: 0.4rem 0.65rem;
    background: var(--surface, #1a1a1a);
    border-radius: 3px; font-size: 0.8em;
    word-break: break-all;
  }
  .run-live p { margin: 0.25rem 0; }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .kw { color: #7a9d7a; font-weight: 600; }

  .spinner {
    display: inline-block; font-family: ui-monospace, monospace;
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    0%   { content: '⠋'; transform: rotate(0deg); }
    100% { transform: rotate(360deg); }
  }

  .run-result {
    margin-top: 0.6rem; padding: 0.55rem 0.75rem;
    border-radius: 4px; border-left: 3px solid var(--border, #2a2a2a);
    background: var(--surface-2, #0d0d0d);
  }
  .run-result.ok  { border-left-color: #4a9d5b; }
  .run-result.err { border-left-color: #c5524a; }
  .run-result p { margin: 0 0 0.35rem; }
  .err-msg { color: #e07a72; }
  .output {
    margin: 0.25rem 0 0; padding: 0.5rem 0.75rem;
    background: var(--surface, #1a1a1a);
    border-radius: 3px; font-size: 0.8em;
    white-space: pre-wrap; word-break: break-word;
    max-height: 320px; overflow-y: auto;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }

  .drill-link {
    color: inherit;
    text-decoration: none;
  }
  .drill-link:hover { text-decoration: underline; }
</style>
