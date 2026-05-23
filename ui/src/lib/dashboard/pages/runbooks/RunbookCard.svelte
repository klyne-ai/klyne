<!--
  RunbookCard — single runbook entry card.
  Scope/project pill (uppercase), id + ago in mono, title, pre-formatted body
  in var(--bg-inset) inset card (max-height 110px, overflow hidden), tag pills,
  delete button (alert text) on left, open chevron on right.
-->
<script lang="ts">
  import type { Decision } from '$lib/types.js';
  import { relTime } from '$lib/format.js';
  import Icon from '$lib/dashboard/Icon.svelte';
  import { deleteMemory } from '$lib/api.js';

  interface RunbookEntry extends Decision {
    scope: 'global' | 'project';
    project_name: string;
    title: string;
    body: string;
  }

  interface Props {
    runbook: RunbookEntry;
    activeTag: string | null;
    onTagClick: (tag: string) => void;
    onDeleted: () => void;
  }

  const { runbook, activeTag, onTagClick, onDeleted }: Props = $props();

  let deleting = $state(false);
  let deleteError = $state<string | null>(null);

  async function handleDelete(): Promise<void> {
    if (!confirm('Delete this runbook? This cannot be undone.')) return;
    deleting = true;
    deleteError = null;
    try {
      await deleteMemory(runbook.id);
      onDeleted();
    } catch (e: unknown) {
      deleteError = e instanceof Error ? e.message : 'delete failed';
    } finally {
      deleting = false;
    }
  }

  function handleOpen(): void {
    // TODO: wire to a drill-in modal/route when designed
    console.log('TODO: open runbook detail', runbook.id);
  }
</script>

<article class="runbook-card">
  <!-- Top row: scope pill + id/age -->
  <div class="card-top">
    <span class="scope-pill" class:pill-global={runbook.scope === 'global'}>
      {runbook.scope === 'global' ? 'GLOBAL' : runbook.project_name.toUpperCase()}
    </span>
    <span class="card-meta">
      {runbook.id.slice(2, 14)} · {relTime(runbook.ts)}
    </span>
  </div>

  <!-- Title -->
  <h4 class="card-title">{runbook.title}</h4>

  <!-- Body preview -->
  {#if runbook.body}
    <div class="body-inset">
      <pre class="body-pre">{runbook.body}</pre>
    </div>
  {/if}

  <!-- Tag pills -->
  {#if (runbook.tags ?? []).length > 0}
    <div class="tag-row">
      {#each (runbook.tags ?? []).slice(0, 5) as tag (tag)}
        <button
          class="tag-pill"
          class:tag-pill--active={activeTag === tag}
          onclick={() => onTagClick(tag)}
          aria-pressed={activeTag === tag}
        >
          {tag}
        </button>
      {/each}
    </div>
  {/if}

  {#if deleteError}
    <p class="delete-error">{deleteError}</p>
  {/if}

  <!-- Footer: delete left, open right -->
  <div class="card-foot">
    <button
      class="btn-delete"
      onclick={handleDelete}
      disabled={deleting}
      aria-label={`Delete runbook ${runbook.title}`}
    >
      {deleting ? 'deleting…' : 'delete'}
    </button>
    <button class="btn-open" onclick={handleOpen} aria-label="Open runbook detail">
      open <Icon name="chev" size={13} />
    </button>
  </div>
</article>

<style>
  .runbook-card {
    background: var(--bg-card);
    border: 1px solid var(--border-hair);
    border-radius: 10px;
    padding: 14px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .card-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .scope-pill {
    font-size: 10px;
    font-family: var(--font-mono);
    font-weight: 600;
    letter-spacing: 0.06em;
    padding: 2px 7px;
    border-radius: 4px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg-soft);
    text-transform: uppercase;
  }

  .scope-pill.pill-global {
    background: color-mix(in oklch, var(--accent) 12%, var(--bg-card-2));
    color: var(--accent);
    border-color: color-mix(in oklch, var(--accent) 30%, transparent);
  }

  .card-meta {
    font-family: var(--font-mono);
    font-size: 10.5px;
    color: var(--fg-muted);
  }

  .card-title {
    margin: 0;
    font-size: 13.5px;
    font-weight: 500;
    color: var(--fg);
    line-height: 1.4;
  }

  .body-inset {
    background: var(--bg-inset);
    border: 1px solid var(--border-hair);
    border-radius: 7px;
    padding: 10px 12px;
    max-height: 110px;
    overflow: hidden;
  }

  .body-pre {
    margin: 0;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-soft);
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .tag-row {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .tag-pill {
    font-size: 10px;
    font-family: var(--font-sans);
    padding: 2px 7px;
    border-radius: 4px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg-soft);
    cursor: pointer;
    transition: background 0.1s, color 0.1s, border-color 0.1s;
  }

  .tag-pill:hover {
    background: var(--bg-hover);
    color: var(--fg);
  }

  .tag-pill--active {
    background: color-mix(in oklch, var(--accent) 18%, var(--bg-card-2));
    color: var(--accent);
    border-color: color-mix(in oklch, var(--accent) 40%, transparent);
  }

  .delete-error {
    margin: 0;
    font-size: 11px;
    color: var(--alert, var(--danger, #e05));
  }

  .card-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 4px;
  }

  .btn-delete {
    background: none;
    border: none;
    padding: 0;
    font-size: 11px;
    font-family: var(--font-sans);
    color: var(--alert, var(--danger, #e05));
    cursor: pointer;
    opacity: 0.8;
    transition: opacity 0.1s;
  }

  .btn-delete:hover:not(:disabled) {
    opacity: 1;
  }

  .btn-delete:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .btn-open {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    border-radius: 6px;
    border: 1px solid var(--border-hair);
    background: var(--bg-card-2);
    color: var(--fg);
    font-size: 11.5px;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: background 0.1s;
  }

  .btn-open:hover {
    background: var(--bg-hover);
  }
</style>
