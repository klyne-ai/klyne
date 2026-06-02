<!--
  ProjectFilters — chip row that lets the user hide whole projects from
  the productivity dashboard. Clicking a chip toggles repo-level hide;
  hidden chips dim + show a strikethrough so the user can always see
  what they've removed and click again to bring it back.

  The chip row renders every repo in the *unfiltered* report so a
  hidden project never silently disappears — addresses the existing
  hidden-sessions concern that repo-wide hiding can bury real work.
-->
<script lang="ts">
  import type { ProductivityReport } from '$lib/api';
  import { hiddenRepoNames, toggleHiddenRepo, clearHiddenRepos } from '$lib/hidden-sessions.svelte';

  interface Props {
    /** The UNFILTERED report — we need every project, even hidden ones. */
    report: ProductivityReport;
  }

  const { report }: Props = $props();

  const repos = $derived(
    [...new Set((report.services ?? []).map((s) => s.repo).filter(Boolean))].sort()
  );

  const hidden = $derived(hiddenRepoNames());
  const hiddenCount = $derived(hidden.size);
</script>

{#if repos.length > 1}
  <div class="pf">
    <span class="pf-label">Projects</span>
    <div class="pf-chips">
      {#each repos as repo (repo)}
        {@const isHidden = hidden.has(repo)}
        <button
          type="button"
          class="pf-chip"
          class:pf-chip--hidden={isHidden}
          aria-pressed={isHidden}
          title={isHidden ? `Show ${repo}` : `Hide ${repo} and everything under it`}
          onclick={() => toggleHiddenRepo(repo)}
        >
          <span class="pf-chip-dot" aria-hidden="true"></span>
          <span class="pf-chip-name">{repo}</span>
        </button>
      {/each}
    </div>
    {#if hiddenCount > 0}
      <button type="button" class="pf-clear" onclick={clearHiddenRepos}>
        {hiddenCount} hidden — show all
      </button>
    {/if}
  </div>
{/if}

<style>
  .pf {
    display: flex;
    align-items: center;
    gap: var(--ad-s3);
    flex-wrap: wrap;
    padding: var(--ad-s2) var(--ad-s4);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
  }

  .pf-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-weight: 600;
    color: var(--ad-faint);
  }

  .pf-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    min-width: 0;
  }

  .pf-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px;
    background: var(--ad-bg-1, transparent);
    border: 1px solid var(--ad-border-soft);
    border-radius: 999px;
    color: var(--ad-fg-2);
    font-size: var(--ad-fs-xs);
    font-family: var(--ad-font-mono);
    line-height: 1.5;
    cursor: pointer;
    transition: opacity 0.12s ease, color 0.12s ease, border-color 0.12s ease;
  }
  .pf-chip:hover {
    border-color: var(--ad-border);
    color: var(--ad-fg);
  }
  .pf-chip-dot {
    width: 6px;
    height: 6px;
    border-radius: 999px;
    background: var(--ad-live, currentColor);
    opacity: 0.7;
  }
  .pf-chip--hidden {
    opacity: 0.45;
    text-decoration: line-through;
  }
  .pf-chip--hidden .pf-chip-dot {
    background: var(--ad-faint);
    opacity: 0.6;
  }

  .pf-clear {
    margin-left: auto;
    padding: 2px 8px;
    background: transparent;
    border: 1px solid var(--ad-border-soft);
    border-radius: var(--ad-r-sm);
    color: var(--ad-muted);
    font-size: var(--ad-fs-xs);
    cursor: pointer;
  }
  .pf-clear:hover {
    color: var(--ad-fg);
    border-color: var(--ad-border);
  }
</style>
