<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchAdvisories } from '$lib/api.js';
  import { relTime } from '$lib/format.js';
  import type { AdvisoryRow, AdvisoryKind } from '$lib/types.js';

  // Filter state lives in the URL query so refresh / share preserves it.
  let advisories = $state<AdvisoryRow[]>([]);
  let loading = $state(true);
  let kindFilter = $state<'all' | AdvisoryKind>('all');

  /** Kind → human label + accent color for the badge. Kept tiny on
   * purpose — five rows plus 'unknown', no abstraction needed yet. */
  const kindMeta: Record<AdvisoryKind, { label: string; color: string }> = {
    stale: { label: 'stale context', color: 'var(--ad-amber, #f59e0b)' },
    acceleration: { label: 'acceleration', color: 'var(--ad-orange, #fb923c)' },
    hard_ceiling: { label: 'hard ceiling', color: 'var(--ad-red, #ef4444)' },
    window_50: { label: '5-h window 50%', color: 'var(--ad-yellow, #eab308)' },
    window_75: { label: '5-h window 75%', color: 'var(--ad-red, #ef4444)' },
    unknown: { label: 'other', color: 'var(--ad-muted, #6b7280)' }
  };

  const filteredAdvisories = $derived(
    kindFilter === 'all'
      ? advisories
      : advisories.filter((a) => a.kind === kindFilter)
  );

  /** Group advisories by kind for the summary header.
   * Returns rows sorted by count desc for stable display. */
  const kindCounts = $derived.by(() => {
    const counts: Record<string, number> = {};
    for (const a of advisories) counts[a.kind] = (counts[a.kind] ?? 0) + 1;
    return Object.entries(counts).sort((a, b) => b[1] - a[1]);
  });

  function shortSession(id: string): string {
    return id.length > 8 ? id.slice(0, 8) : id;
  }

  function projectName(path: string): string {
    if (!path) return '(unknown project)';
    const parts = path.split('/').filter(Boolean);
    return parts.length > 0 ? parts[parts.length - 1] : path;
  }

  async function load(): Promise<void> {
    loading = true;
    try {
      const res = await fetchAdvisories({ limit: 500 });
      advisories = res.advisories;
    } catch {
      advisories = [];
    } finally {
      loading = false;
    }
  }

  function selectKind(next: 'all' | AdvisoryKind): void {
    kindFilter = next;
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head>
  <title>Advisors — klyne</title>
</svelte:head>

<section style="padding: 24px; max-width: 1100px;">
  <header style="margin-bottom: 24px;">
    <h1 style="margin: 0 0 8px 0; font-size: 24px; letter-spacing: -0.01em;">Advisors</h1>
    <p style="color: var(--ad-muted); margin: 0; max-width: 720px;">
      Every advisory klyne has fired across all your Claude Code sessions. Each row
      is a real injection into your chat via the <code>UserPromptSubmit</code> hook —
      they were delivered to the AI's context at the moments shown, even if you
      didn't see them rendered visibly in the chat UI.
    </p>
  </header>

  <!-- Filter row: All + one button per kind that has at least one hit. -->
  <div style="display: flex; gap: 8px; margin-bottom: 16px; flex-wrap: wrap;">
    <button
      class="ad-btn ad-btn--ghost"
      style="font-weight: {kindFilter === 'all' ? 600 : 500}; background: {kindFilter === 'all' ? 'var(--ad-panel)' : 'transparent'};"
      onclick={() => selectKind('all')}
    >
      All <span style="color: var(--ad-muted); margin-left: 6px;">{advisories.length}</span>
    </button>
    {#each kindCounts as [kind, count]}
      {@const meta = kindMeta[kind as AdvisoryKind] ?? kindMeta.unknown}
      <button
        class="ad-btn ad-btn--ghost"
        style="font-weight: {kindFilter === kind ? 600 : 500}; background: {kindFilter === kind ? 'var(--ad-panel)' : 'transparent'};"
        onclick={() => selectKind(kind as AdvisoryKind)}
      >
        <span style="display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: {meta.color}; margin-right: 6px;"></span>
        {meta.label}
        <span style="color: var(--ad-muted); margin-left: 6px;">{count}</span>
      </button>
    {/each}
  </div>

  {#if loading}
    <div style="color: var(--ad-muted); padding: 32px 0;">Loading advisories…</div>
  {:else if advisories.length === 0}
    <div style="border: 1px dashed var(--ad-border); padding: 32px; border-radius: 8px; color: var(--ad-muted);">
      No advisories indexed yet. After the daemon ingests your sessions, every
      <code>UserPromptSubmit</code> hook firing from <code>klyne advise</code> will
      appear here. If you're seeing this on a populated install, restart the daemon
      to re-scan transcripts with the latest parser.
    </div>
  {:else if filteredAdvisories.length === 0}
    <div style="border: 1px dashed var(--ad-border); padding: 24px; border-radius: 8px; color: var(--ad-muted);">
      No advisories match the current filter.
    </div>
  {:else}
    <ul style="list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 12px;">
      {#each filteredAdvisories as adv (adv.message_id)}
        {@const meta = kindMeta[adv.kind] ?? kindMeta.unknown}
        <li
          style="border: 1px solid var(--ad-border); border-left: 3px solid {meta.color}; border-radius: 6px; padding: 14px 16px; background: var(--ad-bg-2);"
        >
          <div style="display: flex; justify-content: space-between; align-items: baseline; gap: 12px; margin-bottom: 8px;">
            <div style="display: flex; align-items: center; gap: 10px;">
              <span style="text-transform: uppercase; font-size: 11px; letter-spacing: 0.04em; color: {meta.color}; font-weight: 600;">
                {meta.label}
              </span>
              <span style="color: var(--ad-muted); font-size: 12px;">{adv.cli || '?'}</span>
              <button
                class="ad-btn ad-btn--ghost"
                style="font-size: 12px; padding: 2px 8px;"
                onclick={() => goto(`/sessions/${adv.session_id}`)}
                title="Open session"
              >
                {shortSession(adv.session_id)}
              </button>
              <span style="color: var(--ad-muted); font-size: 12px;">in {projectName(adv.project_path)}</span>
            </div>
            <time style="color: var(--ad-muted); font-size: 12px; font-family: var(--ad-font-mono);" title={new Date(adv.ts).toISOString()}>
              {relTime(adv.ts)}
            </time>
          </div>
          <p style="margin: 0; color: var(--ad-fg); line-height: 1.5;">{adv.content}</p>
        </li>
      {/each}
    </ul>
  {/if}
</section>
