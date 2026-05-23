<!--
  TabSessions — Sessions tab for a project.
  Day-groups sessions (Today / Yesterday / <Mon DD>), slim rows, eye-off toggle,
  click opens SessionDrawer via ?session=.
-->
<script lang="ts">
  import type { Session } from '$lib/types.js';
  import { kfmt, dayLabel } from '$lib/format.js';
  import { hiddenSessionIds, toggleHidden } from '$lib/hidden-sessions.svelte.js';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { sessionUrl } from '$lib/dashboard/url-state.js';
  import Icon from '$lib/dashboard/Icon.svelte';

  interface Props {
    sessions: Session[];
  }
  const { sessions }: Props = $props();

  type CliFilter = 'all' | 'claude' | 'codex';
  let cliFilter = $state<CliFilter>('all');
  let localShowHidden = $state(false);

  const filtered = $derived(
    sessions.filter((s) => {
      if (!localShowHidden && hiddenSessionIds().has(s.id)) return false;
      if (cliFilter !== 'all' && s.cli !== cliFilter) return false;
      return true;
    })
  );

  // Group by day label
  const grouped = $derived.by(() => {
    const map = new Map<string, Session[]>();
    for (const s of filtered) {
      const label = dayLabel(s.last_msg_at);
      const bucket = map.get(label) ?? [];
      bucket.push(s);
      map.set(label, bucket);
    }
    return map;
  });

  const claudeCount = $derived(sessions.filter((s) => s.cli === 'claude').length);
  const codexCount = $derived(sessions.filter((s) => s.cli === 'codex').length);

  function openSession(id: string): void {
    void goto(sessionUrl($page.url.pathname + $page.url.search, id));
  }
</script>

<div style="display: flex; flex-direction: column; gap: 14px;">
  <!-- Toolbar -->
  <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
    {#each (['all', 'claude', 'codex'] as CliFilter[]) as k}
      <button
        type="button"
        onclick={() => { cliFilter = k; }}
        style="
          padding: 4px 10px;
          border-radius: 6px;
          border: 1px solid {cliFilter === k ? 'var(--ad-accent)' : 'var(--ad-border)'};
          background: {cliFilter === k ? 'color-mix(in oklch, var(--ad-accent) 12%, var(--ad-bg-2))' : 'var(--ad-bg-2)'};
          color: {cliFilter === k ? 'var(--ad-fg)' : 'var(--ad-faint)'};
          font-size: 12px;
          cursor: pointer;
        "
      >
        {k}
        <span style="margin-left: 4px; color: var(--ad-faint); font-size: 11px;">
          {k === 'all' ? sessions.length : k === 'claude' ? claudeCount : codexCount}
        </span>
      </button>
    {/each}

    <button
      type="button"
      onclick={() => { localShowHidden = !localShowHidden; }}
      style="
        margin-left: auto;
        padding: 4px 10px;
        border-radius: 6px;
        border: 1px solid var(--ad-border);
        background: var(--ad-bg-2);
        color: var(--ad-faint);
        font-size: 12px;
        cursor: pointer;
        display: flex;
        align-items: center;
        gap: 6px;
      "
      title="{localShowHidden ? 'Hide' : 'Show'} hidden sessions"
    >
      <Icon name={localShowHidden ? 'eye' : 'eye-off'} size={12} />
      {localShowHidden ? 'hide hidden' : 'show hidden'}
    </button>
  </div>

  <!-- Day groups -->
  {#each [...grouped.entries()] as [day, list]}
    <div>
      <div
        class="ad-mono"
        style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 8px;"
      >{day}</div>
      <div style="display: flex; flex-direction: column; gap: 4px;">
        {#each list as s}
          {@const hidden = hiddenSessionIds().has(s.id)}
          <div
            style="
              display: grid;
              grid-template-columns: 60px minmax(0, 1fr) 80px 70px 70px 16px;
              gap: 10px;
              align-items: center;
              padding: 10px 12px;
              border-radius: 8px;
              background: {hidden ? 'var(--ad-bg-2)' : 'var(--ad-panel)'};
              border: 1px solid var(--ad-border);
              opacity: {hidden ? 0.5 : 1};
            "
          >
            <button
              type="button"
              onclick={() => openSession(s.id)}
              style="
                display: contents;
                cursor: pointer;
                background: none;
                border: none;
                padding: 0;
                text-align: left;
              "
            >
              <span
                class="ad-pill {s.cli === 'claude' ? 'ad-pill--claude' : 'ad-pill--codex'}"
                style="width: fit-content; font-size: 10px;"
              >{s.cli}</span>
              <div style="min-width: 0;">
                <div
                  style="font-size: 12.5px; color: var(--ad-fg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
                >{s.id.slice(0, 12)}…</div>
                <div class="ad-mono" style="font-size: 10.5px; color: var(--ad-faint);">
                  {s.model || '—'}
                </div>
              </div>
              <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-align: right;">{s.msg_count} msgs</span>
              <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint); text-align: right;">↓ {kfmt(s.tokens_out)}</span>
              <span
                class="ad-pill {s.status === 'active' ? 'ad-pill--ok' : ''}"
                style="justify-self: end; font-size: 10px;"
              >{s.status}</span>
            </button>
            <!-- Eye-off toggle — separate from the row click -->
            <button
              type="button"
              onclick={() => toggleHidden(s.id)}
              style="
                color: var(--ad-faint);
                background: none;
                border: none;
                cursor: pointer;
                padding: 0;
                display: flex;
                align-items: center;
                justify-content: center;
              "
              title="{hidden ? 'Show' : 'Hide'} this session"
            >
              <Icon name={hidden ? 'eye-off' : 'eye'} size={13} />
            </button>
          </div>
        {/each}
      </div>
    </div>
  {/each}

  {#if filtered.length === 0}
    <div style="padding: 24px; text-align: center; color: var(--ad-faint); font-size: 13px;">
      No sessions to show.
    </div>
  {/if}
</div>
