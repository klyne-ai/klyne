<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchSessions, deleteSession, fetchMessages } from '$lib/api.js';
  import { projectsStore, refreshProjects } from '$lib/projects.svelte.js';
  import { removeSession } from '$lib/stores.svelte.js';
  import { kfmt, relAgo, dayLabel, costFmt } from '$lib/format.js';
  import type { Session, Message } from '$lib/types.js';
  import { isConversationalMessage } from '$lib/messageFilters.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import StatusBadge from '$lib/ui/StatusBadge.svelte';
  import { sessionHref } from '$lib/navlinks.js';

  let {
    projectName,
    basePath = '',
    backHref = '/projects',
  }: { projectName: string; basePath?: string; backHref?: string } = $props();

  const project = $derived(
    projectsStore.items.find((p) => p.name === projectName) ?? null
  );

  let sessions = $state<Session[]>([]);
  let sessionPreviews = $state<Record<string, string>>({});
  let loadError = $state<string | null>(null);
  let deletingId = $state<string | null>(null);
  let previewLoadSeq = 0;

  async function handleDelete(s: Session, e: Event): Promise<void> {
    e.stopPropagation();
    const ok = window.confirm(
      `Delete session ${s.id.slice(0, 8)}?\n\n${s.msg_count} msgs · ↓ ${kfmt(s.tokens_out)} tokens\n\nThis cannot be undone. The source JSONL on disk is untouched.`
    );
    if (!ok) return;
    deletingId = s.id;
    try {
      await deleteSession(s.id);
      removeSession(s.id);
      sessions = sessions.filter((x) => x.id !== s.id);
      void refreshProjects();
    } catch (err) {
      loadError = err instanceof Error ? err.message : 'delete failed';
    } finally {
      deletingId = null;
    }
  }

  // Tab filter: all | claude | codex. Defaults to 'all'.
  let cliFilter = $state<'all' | 'claude' | 'codex'>('all');

  // Transient hint shown next to "Open in CLI" after a clipboard copy.
  let copyHint = $state('');

  // Sessions filtered by the active CLI tab.
  const filteredSessions = $derived(
    cliFilter === 'all' ? sessions : sessions.filter((s) => s.cli === cliFilter)
  );

  // Per-CLI counts for tab badges. Recomputed when sessions change.
  const counts = $derived({
    all: sessions.length,
    claude: sessions.filter((s) => s.cli === 'claude').length,
    codex: sessions.filter((s) => s.cli === 'codex').length,
  });

  // Group sessions by day (after CLI filter).
  const grouped = $derived.by(() => {
    const g = new Map<string, Session[]>();
    for (const s of filteredSessions) {
      const label = dayLabel(s.last_msg_at);
      const existing = g.get(label) ?? [];
      existing.push(s);
      g.set(label, existing);
    }
    return g;
  });

  // Preview filter aliases the global rule (see $lib/messageFilters.ts):
  // only user + assistant prose ever surfaces, so the project index
  // shares the same source of truth as Terminal / cockpit / sessions.
  const isPreviewable = isConversationalMessage;

  function compactPreview(text: string): string {
    return text.replace(/\s+/g, ' ').trim().slice(0, 140);
  }

  async function loadSessionPreviews(sessionList: Session[]): Promise<void> {
    const seq = ++previewLoadSeq;
    const targets = sessionList.slice(0, 120);
    const entries = await Promise.all(targets.map(async (s) => {
      try {
        const resp = await fetchMessages(s.id, { limit: 30, order: 'desc' });
        const preview = resp.messages.find(isPreviewable);
        if (!preview) return null;
        return [s.id, compactPreview(preview.content)] as const;
      } catch {
        return null;
      }
    }));
    if (seq !== previewLoadSeq) return;
    sessionPreviews = Object.fromEntries(entries.filter((entry): entry is readonly [string, string] => entry !== null));
  }

  async function loadSessions(): Promise<void> {
    loadError = null;
    try {
      const resp = await fetchSessions({ limit: 500 });
      // Filter by project path matching the project name
      const next = resp.sessions.filter(
        (s) => s.project_path.endsWith('/' + projectName) || s.project_path === projectName
      );
      sessions = next;
      sessionPreviews = {};
      void loadSessionPreviews(next);
    } catch (e) {
      loadError = e instanceof Error ? e.message : 'Failed to load sessions';
    }
  }

  onMount(() => {
    if (projectsStore.items.length === 0) void refreshProjects();
    void loadSessions();
  });
</script>

<svelte:head>
  <title>klyne — {projectName}</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1280px;">
  <button onclick={() => goto(backHref)} class="ad-btn ad-btn--ghost" style="margin-bottom: 12px; padding-left: 4px;">
    ‹ projects
  </button>

  {#if project}
    <div style="display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px;">
      <div>
        <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 6px;">
          <span class="ad-dot ad-dot--{project.status}"></span>
          <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0;">{project.name}</h1>
          {#each project.clis as c}
            <CliBadge cli={c} />
          {/each}
          {#if project.clis.length > 1}
            <span class="ad-mono ad-faint" style="font-size: 11px;">
              · claude {project.sessionsByCli.claude} / codex {project.sessionsByCli.codex}
            </span>
          {/if}
        </div>
        <div class="ad-mono ad-muted" style="font-size: 12px;">{project.project_path}</div>
      </div>
      <div style="display: flex; gap: 8px;">
        <button
          class="ad-btn"
          title="Copy a shell command that cd's into this project and starts the CLI"
          onclick={async () => {
            try {
              await navigator.clipboard.writeText(`cd "${project.project_path}" && ${project.cli}`);
              copyHint = 'copied';
              setTimeout(() => { copyHint = ''; }, 1500);
            } catch {
              copyHint = 'copy failed';
              setTimeout(() => { copyHint = ''; }, 2000);
            }
          }}
        >Open in CLI{#if copyHint} · {copyHint}{/if}</button>
      </div>
    </div>

    <!-- Stats strip -->
    <div class="ad-card" style="display: grid; grid-template-columns: repeat(5, 1fr); padding: 0; margin-bottom: 20px;">
      {#each [
        ['Sessions', String(project.sessions)],
        ['Messages', String(project.msgs)],
        ['↑ tokens in', kfmt(project.tokensIn)],
        ['↓ tokens out', kfmt(project.tokensOut)],
        ['Cost', costFmt(project.cost, project.priced)],
      ] as [label, value], i}
        <div style="padding: 14px 16px; border-right: {i < 4 ? '1px solid var(--ad-border-soft)' : 'none'};">
          <div style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px;">{label}</div>
          <div class="ad-mono ad-tnum" style="font-size: 18px; font-weight: 600;">{value}</div>
        </div>
      {/each}
    </div>
  {:else}
    <div style="margin-bottom: 20px; color: var(--ad-muted);">Loading project…</div>
  {/if}

  <!-- CLI tabs (only when this project actually has both CLIs) -->
  {#if counts.claude > 0 && counts.codex > 0}
    <div role="tablist" style="display: flex; gap: 2px; border-bottom: 1px solid var(--ad-border); margin-bottom: 14px;">
      {#each [
        ['all', 'All', counts.all],
        ['claude', 'Claude', counts.claude],
        ['codex', 'Codex', counts.codex],
      ] as [id, label, n]}
        <button
          role="tab"
          aria-selected={cliFilter === id}
          onclick={() => (cliFilter = id as 'all' | 'claude' | 'codex')}
          style="
            padding: 8px 14px;
            font-size: 13px;
            font-weight: 500;
            color: {cliFilter === id ? 'var(--ad-fg)' : 'var(--ad-muted)'};
            border-bottom: 2px solid {cliFilter === id ? 'var(--ad-claude)' : 'transparent'};
            margin-bottom: -1px;
            display: inline-flex;
            align-items: center;
            gap: 6px;
          "
        >
          <span>{label}</span>
          <span class="ad-mono" style="font-size: 11px; color: var(--ad-faint);">{n}</span>
        </button>
      {/each}
    </div>
  {/if}

  <!-- Sessions grouped by day -->
  {#if loadError}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error);">{loadError}</div>
  {:else if sessions.length === 0}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">No sessions found for this project.</div>
  {:else if filteredSessions.length === 0}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">
      No <span class="ad-mono">{cliFilter}</span> sessions in this project.
    </div>
  {:else}
    {#each [...grouped.entries()] as [day, list]}
      <div style="margin-bottom: 16px;">
        <div class="ad-section-h" style="margin-bottom: 6px; padding: 0 4px;">{day}</div>
        <div class="ad-card" style="overflow: hidden;">
          {#each list as s, i}
            <div
              role="button"
              tabindex="0"
              onclick={() => goto(sessionHref(basePath, s.id))}
              onkeydown={(e) => { if (e.key === 'Enter') goto(sessionHref(basePath, s.id)); }}
              style="display: grid; grid-template-columns: 70px minmax(0, 1fr) 80px 90px 70px 110px 28px 20px; align-items: center; gap: 12px; padding: 10px 14px; border-bottom: {i < list.length - 1 ? '1px solid var(--ad-border-soft)' : 'none'}; font-size: 13px; cursor: pointer;"
              onmouseenter={(e) => ((e.currentTarget as HTMLElement).style.background = 'var(--ad-panel-hi)')}
              onmouseleave={(e) => ((e.currentTarget as HTMLElement).style.background = 'transparent')}
            >
              <span><CliBadge cli={s.cli} /></span>
              <span style="min-width: 0;">
                <span class="ad-truncate" style="display: block; color: var(--ad-fg); font-weight: 500;">
                  {sessionPreviews[s.id] ?? 'Loading recent activity...'}
                </span>
                <span class="ad-mono ad-truncate" style="display: block; color: var(--ad-faint); font-size: 11px; margin-top: 2px;">
                  {s.id}
                </span>
              </span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: var(--ad-fg-2);">{s.msg_count} msgs</span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: var(--ad-muted);">↓ {kfmt(s.tokens_out)}</span>
              <span class="ad-mono ad-tnum num" style="text-align: right; color: {s.cost_usd > 0 ? 'var(--ad-fg-2)' : 'var(--ad-faint)'};">{costFmt(s.cost_usd, s.cost_usd > 0)}</span>
              <span><StatusBadge status={s.status === 'compacted' ? 'compacted' : (Date.now() - s.last_msg_at < 60_000 ? 'active' : 'idle')} /></span>
              <button
                type="button"
                onclick={(e) => handleDelete(s, e)}
                disabled={deletingId === s.id}
                title="Delete this session"
                aria-label="Delete session"
                style="background: transparent; border: 0; color: var(--ad-faint); font-size: 14px; cursor: pointer; padding: 2px 4px; border-radius: 3px;"
                onmouseenter={(e) => { (e.currentTarget as HTMLElement).style.color = 'var(--ad-error, #f87171)'; (e.currentTarget as HTMLElement).style.background = 'var(--ad-bg-2)'; }}
                onmouseleave={(e) => { (e.currentTarget as HTMLElement).style.color = 'var(--ad-faint)'; (e.currentTarget as HTMLElement).style.background = 'transparent'; }}
              >{deletingId === s.id ? '…' : '✕'}</button>
              <span style="color: var(--ad-faint);">›</span>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>
