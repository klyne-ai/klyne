<!--
  Confirm delete for project-wide data wipe.

  On mount, fetches a dry-run preview from DELETE /api/projects to show
  exactly how many rows will be removed in each scope (entries,
  reflections, decisions, runbook dismissals, work spans, git snapshots).
  Sessions/messages stay; we tell the user that explicitly.

  Cancel / Esc / scrim click dismisses. Clicking the red Delete button
  fires the real delete; on success the parent's `onDeleted` callback
  refreshes the list and the modal closes.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { deleteProjectData, type ProjectDeleteCounts } from '$lib/api';
  import { portal } from '$lib/dashboard/portal';
  import { SHOW_REFLECTIONS } from '$lib/featureFlags';

  interface Props {
    projectName: string;
    projectPath: string;
    onClose: () => void;
    onDeleted: () => void;
  }
  const { projectName, projectPath, onClose, onDeleted }: Props = $props();

  let closing = $state(false);
  let preview = $state<ProjectDeleteCounts | null>(null);
  let previewError = $state<string | null>(null);
  let deleting = $state(false);
  let deleteError = $state<string | null>(null);

  async function loadPreview(): Promise<void> {
    previewError = null;
    preview = null;
    try {
      const res = await deleteProjectData(projectPath, { dryRun: true });
      preview = res.counts;
    } catch (e) {
      previewError = e instanceof Error ? e.message : 'failed to load preview';
    }
  }

  async function confirmDelete(): Promise<void> {
    if (!preview || deleting) return;
    deleting = true;
    deleteError = null;
    try {
      await deleteProjectData(projectPath, { dryRun: false });
      onDeleted();
      close();
    } catch (e) {
      deleteError = e instanceof Error ? e.message : 'delete failed';
      deleting = false;
    }
  }

  function close(): void {
    if (deleting) return;
    closing = true;
    setTimeout(onClose, 140);
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  let prevOverflow = '';
  onMount(() => {
    window.addEventListener('keydown', onKey);
    prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    void loadPreview();
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    document.body.style.overflow = prevOverflow;
  });

  type Row = { label: string; count: number };
  const rows = $derived<Row[]>(
    preview
      ? [
          { label: 'worklog entries', count: preview.stop_summaries },
          // Daily reflections row hidden while reflections are dark-launched off.
          ...(SHOW_REFLECTIONS
            ? [{ label: 'daily reflections', count: preview.worklog_reflections }]
            : []),
          { label: 'decisions', count: preview.decisions },
          { label: 'runbook dismissals', count: preview.runbook_dismissals },
          { label: 'work spans', count: preview.work_spans },
          { label: 'git snapshots', count: preview.git_session_snapshots },
        ]
      : []
  );

  const nothingToDelete = $derived(preview !== null && preview.total === 0);
</script>

<div use:portal class="modal-portal-host">
<div
  class="thread-scrim"
  class:is-closing={closing}
  onclick={close}
  role="presentation"
></div>

<div
  class="thread-modal cdpm"
  class:is-closing={closing}
  role="dialog"
  aria-modal="true"
  aria-label={`Delete project data for ${projectName}`}
>
  <header class="thread-modal__head">
    <div class="meta">
      <h3>Delete project data</h3>
      <div class="sub">
        <span class="mono">{projectName}</span>
      </div>
    </div>
    <button class="k-btn k-btn--ghost" onclick={close} aria-label="Close" disabled={deleting}>✕</button>
  </header>

  <div class="thread-modal__body cdpm-body">
    <p class="cdpm-lead">
      This will permanently delete every klyne record scoped to
      <span class="mono path">{projectPath}</span>:
    </p>

    {#if preview === null && previewError === null}
      <p class="mono dim">Counting…</p>
    {:else if previewError !== null}
      <div class="cdpm-error">
        <p>Couldn't load impact preview.</p>
        <p class="mono dim">{previewError}</p>
        <button class="k-btn" onclick={loadPreview}>Retry</button>
      </div>
    {:else if preview !== null}
      <ul class="cdpm-list">
        {#each rows as r}
          <li>
            <span class="cdpm-num mono">{r.count}</span>
            <span class="cdpm-lbl">{r.label}</span>
          </li>
        {/each}
      </ul>

      <p class="cdpm-note">
        Session transcripts (raw conversation history) are <strong>not</strong> deleted.
      </p>

      {#if nothingToDelete}
        <p class="cdpm-empty mono dim">
          No project-scoped rows found — nothing to delete.
        </p>
      {/if}

      {#if deleteError}
        <p class="cdpm-deleteerr">Delete failed: {deleteError}</p>
      {/if}
    {/if}
  </div>

  <footer class="thread-modal__foot cdpm-foot">
    <button class="k-btn" onclick={close} disabled={deleting}>Cancel</button>
    <button
      class="k-btn cdpm-delete"
      onclick={confirmDelete}
      disabled={!preview || nothingToDelete || deleting}
      aria-busy={deleting}
    >
      {deleting ? 'Deleting…' : 'Delete project data'}
    </button>
  </footer>
</div>
</div>

<style>
  .modal-portal-host {
    /* `display: contents` keeps the wrapper transparent to layout so the
       portaled scrim + modal still render at <body> level with their
       own position: fixed rules unaffected. */
    display: contents;
  }
  .cdpm {
    width: min(520px, 92vw);
    max-height: min(70vh, 640px);
  }
  .cdpm-body {
    gap: 14px;
  }
  .cdpm-lead {
    margin: 0;
    font-size: 13px;
    color: var(--fg-soft);
    line-height: 1.55;
  }
  .cdpm-lead .path {
    color: var(--fg);
    word-break: break-all;
  }

  .cdpm-list {
    list-style: none;
    margin: 0;
    padding: 10px 12px;
    border: 1px solid var(--border-hair);
    border-radius: 8px;
    background: var(--bg-card-2);
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px 14px;
  }
  .cdpm-list li {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-size: 12.5px;
    color: var(--fg-soft);
  }
  .cdpm-num {
    min-width: 1.5em;
    color: var(--fg);
    font-variant-numeric: tabular-nums;
  }
  .cdpm-lbl {
    color: var(--fg-muted);
  }

  .cdpm-note {
    margin: 0;
    font-size: 11.5px;
    color: var(--fg-muted);
    line-height: 1.5;
  }
  .cdpm-note strong {
    color: var(--fg-soft);
    font-weight: 600;
  }
  .cdpm-empty {
    margin: 0;
    font-size: 12px;
  }
  .cdpm-error {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid color-mix(in oklch, var(--warn) 30%, transparent);
    border-radius: 8px;
    background: color-mix(in oklch, var(--warn) 8%, var(--bg-card-2));
  }
  .cdpm-error p {
    margin: 0;
    font-size: 12px;
    color: var(--fg);
  }
  .cdpm-deleteerr {
    margin: 0;
    font-size: 12px;
    color: var(--alert);
  }

  .cdpm-foot {
    gap: 8px;
    justify-content: flex-end;
  }
  .cdpm-delete {
    color: var(--alert);
    border-color: color-mix(in oklch, var(--alert) 35%, var(--border));
    background: color-mix(in oklch, var(--alert) 8%, var(--bg-card-2));
  }
  .cdpm-delete:not(:disabled):hover {
    background: color-mix(in oklch, var(--alert) 18%, var(--bg-card-2));
    border-color: color-mix(in oklch, var(--alert) 55%, var(--border));
  }
  .cdpm-delete:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
