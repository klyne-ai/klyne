<!--
  Uncommitted-files modal — opens from an "Uncommitted work" card on the
  productivity page. The card itself only previews the first 5 paths
  (with "… and N more"); this modal shows the FULL list in a wider,
  scrollable panel so long paths like
  `ui/src/lib/dashboard/pages/projects/ProjectPanel.svelte` are readable
  end-to-end with no truncation.

  Deliberately lightweight vs ThreadPeekModal — no tabs, no async fetch.
  All data (the full file list) is already in hand from the card, so it's
  a pure presentational overlay. Reuses the global .thread-scrim /
  .thread-modal / .k-btn styling + the portal action so it matches the
  Live tile's thread peek.

  Click scrim, press Esc, or hit ✕ to dismiss; closing plays the reverse
  fade/scale.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { portal } from '$lib/dashboard/portal';

  // Local copy of the productivity page's fmtMinutes — kept inline so the
  // modal has no cross-module coupling for a four-line helper.
  function fmtMinutes(m: number): string {
    if (!Number.isFinite(m) || m <= 0) return '0m';
    const h = Math.floor(m / 60), mm = m % 60;
    return h > 0 ? `${h}h ${mm}m` : `${mm}m`;
  }

  interface Props {
    repo: string;
    branch: string;
    title: string;
    files: string[];
    ageMinutes: number;
    onClose: () => void;
  }
  const { repo, branch, title, files, ageMinutes, onClose }: Props = $props();

  let closing = $state(false);

  function close(): void {
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
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    document.body.style.overflow = prevOverflow;
  });
</script>

<div use:portal class="modal-portal-host">
<div
  class="thread-scrim"
  class:is-closing={closing}
  onclick={close}
  role="presentation"
></div>

<div
  class="thread-modal ufm"
  class:is-closing={closing}
  role="dialog"
  aria-modal="true"
  aria-label={`Uncommitted files for ${repo} · ${branch}`}
>
  <header class="thread-modal__head">
    <span class="kick kick-alert">Uncommitted</span>
    <div class="meta">
      <h3><span>{repo}</span></h3>
      <div class="sub">
        <span>{branch}</span>
        <span>·</span>
        <span>{files.length} dirty</span>
        {#if ageMinutes > 0}
          <span>·</span>
          <span>{fmtMinutes(ageMinutes)} old</span>
        {/if}
      </div>
    </div>
    <button class="k-btn k-btn--ghost" onclick={close} aria-label="Close">✕</button>
  </header>

  <div class="thread-modal__body ufm-body">
    <p class="ufm-title">{title}</p>
    {#if files.length > 0}
      <ul class="ufm-files">
        {#each files as f, i (f + i)}
          <li class="mono ufm-file">{f}</li>
        {/each}
      </ul>
    {:else}
      <p class="mono dim">No file paths were captured for this risk.</p>
    {/if}
  </div>
</div>
</div>

<style>
  .modal-portal-host {
    /* `display: contents` keeps this wrapper transparent to layout so the
       portaled scrim + modal render at <body> level (see portal action). */
    display: contents;
  }

  /* .kick / .kick-alert are page-scoped on the productivity route, so
     re-declare them here for the portaled modal (rendered at <body>). */
  .kick {
    font-family: var(--font-mono);
    font-size: 11px;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }
  .kick-alert {
    color: var(--alert);
  }

  /* Narrower than ThreadPeekModal — a single column of paths reads best
     without sprawling the full 680px. */
  .ufm {
    width: min(560px, 92vw);
    max-height: min(78vh, 720px);
  }

  .ufm-body {
    gap: 12px;
  }

  .ufm-title {
    margin: 0;
    font-size: 12.5px;
    color: var(--fg-soft);
    line-height: 1.5;
  }

  .ufm-files {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .ufm-file {
    font-size: 12px;
    color: var(--fg-soft);
    line-height: 1.5;
    padding: 5px 10px;
    border-radius: 6px;
    background: var(--bg-card-2);
    border: 1px solid var(--border-hair);
    /* Long paths wrap on slashes instead of truncating — the whole point
       of the modal is that nothing is hidden. */
    overflow-wrap: anywhere;
    word-break: break-word;
  }
</style>
