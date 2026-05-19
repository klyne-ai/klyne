<!--
  Inspector — right panel detailing the currently-selected project.
  Shows headline stats, recent sessions list, and per-project runbooks.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { fetchSessions } from '$lib/api.js';
  import type { Session } from '$lib/types.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import { kfmt, relTime } from '$lib/format.js';

  interface Props {
    project: ProjectAggregate;
    onClose: () => void;
  }
  const { project, onClose }: Props = $props();

  let sessions = $state<Session[]>([]);
  let loading = $state(true);

  async function loadSessions(path: string): Promise<void> {
    loading = true;
    try {
      const resp = await fetchSessions({ project: path, limit: 8 });
      sessions = resp.sessions;
    } catch {
      sessions = [];
    } finally {
      loading = false;
    }
  }

  onMount(() => { void loadSessions(project.project_path); });

  $effect(() => { void loadSessions(project.project_path); });
</script>

<aside class="inspector">
  <div class="insp-hd">
    <div class="row" style="justify-content: space-between; align-items: baseline;">
      <h2>{project.name}</h2>
      <button class="btn btn--ghost btn--sm" onclick={onClose} title="Collapse inspector" aria-label="Collapse">›</button>
    </div>
    <div class="path">{project.project_path}</div>
    <div class="row" style="margin-top: 8px; gap: 6px; flex-wrap: wrap;">
      {#each project.clis as c}
        <span class="badge badge--{c}">{c}</span>
      {/each}
      {#if project.model}
        <span class="badge badge--tag">{project.model}</span>
      {/if}
      <span class="spacer"></span>
      <span class="dot {project.lastMsAgo < 60_000 ? 'dot--live' : 'dot--idle'}"></span>
      <span class="mono faint" style="font-size: 11px;">
        {project.lastMsAgo < 60_000 ? 'live' : 'idle ' + relTime(project.lastMsAt)}
      </span>
    </div>
  </div>

  <div class="insp-stats">
    <div class="insp-stat"><div class="k">Sessions</div><div class="v">{project.sessions}</div></div>
    <div class="insp-stat"><div class="k">Messages</div><div class="v">{kfmt(project.msgs)}</div></div>
    <div class="insp-stat"><div class="k">Tokens in</div><div class="v">{kfmt(project.tokensIn)}</div></div>
    <div class="insp-stat"><div class="k">Tokens out</div><div class="v">{kfmt(project.tokensOut)}</div></div>
  </div>

  <div class="insp-section">
    <h4>Recent sessions ({sessions.length})</h4>
    {#if loading}
      <div class="muted" style="font-size: 12px;">Loading…</div>
    {:else if sessions.length === 0}
      <div class="muted" style="font-size: 12px;">No sessions in this project.</div>
    {:else}
      <div style="display: flex; flex-direction: column; gap: 2px;">
        {#each sessions as s (s.id)}
          <div class="session-row" onclick={() => goto(`/sessions/${encodeURIComponent(s.id)}`)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') goto(`/sessions/${encodeURIComponent(s.id)}`); }}>
            <span class="dot {s.status === 'active' ? 'dot--live' : 'dot--idle'}"></span>
            <span class="id">{s.id.slice(0, 14)}…</span>
            <span class="msgs">{s.msg_count} · {relTime(s.last_msg_at)}</span>
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <div class="insp-section" style="border-bottom: 0;">
    <h4>Actions</h4>
    <div style="display: flex; flex-direction: column; gap: 6px;">
      <button class="btn" onclick={() => goto(`/projects/${encodeURIComponent(project.name)}`)}>Open project page →</button>
      <button class="btn btn--ghost">Export sessions as JSONL</button>
    </div>
  </div>
</aside>
