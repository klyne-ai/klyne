<script lang="ts">
  import { goto } from '$app/navigation';
  import { projectsStore, refreshProjects } from '$lib/projects.svelte.js';
  import { kfmt, relAgo } from '$lib/format.js';
  import CliBadge from '$lib/ui/CliBadge.svelte';
  import { onMount } from 'svelte';

  let sortBy = $state<'recent' | 'name' | 'msgs' | 'tokens' | 'sessions'>('recent');
  let cliFilter = $state<'all' | 'claude' | 'codex'>('all');
  let nameFilter = $state('');

  const projects = $derived(projectsStore.items);

  const sorted = $derived.by(() => {
    let p = projects;
    // Use clis[] membership so projects with both Claude AND Codex sessions
    // (e.g. trackIt: 39 claude + 20 codex) show up under either filter.
    if (cliFilter !== 'all') {
      const want = cliFilter; // narrow to 'claude' | 'codex' for the closure
      p = p.filter((x) => x.clis.includes(want));
    }
    if (nameFilter) p = p.filter((x) => x.name.toLowerCase().includes(nameFilter.toLowerCase()));
    const sorters: Record<string, (a: typeof p[0], b: typeof p[0]) => number> = {
      recent: (a, b) => a.lastMsAgo - b.lastMsAgo,
      name: (a, b) => a.name.localeCompare(b.name),
      msgs: (a, b) => b.msgs - a.msgs,
      tokens: (a, b) => b.tokensOut - a.tokensOut,
      sessions: (a, b) => b.sessions - a.sessions,
    };
    return [...p].sort(sorters[sortBy]);
  });

  onMount(() => {
    if (projectsStore.items.length === 0) void refreshProjects();
  });
</script>

<svelte:head>
  <title>agentdeck — Projects</title>
</svelte:head>

<div style="padding: 20px 24px 40px; max-width: 1280px;">
  <div style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 4px;">
    <h1 style="font-size: var(--ad-fs-2xl); font-weight: 600; letter-spacing: -0.02em; margin: 0;">
      Projects <span style="color: var(--ad-faint); font-weight: 400; margin-left: 8px;">({sorted.length})</span>
    </h1>
  </div>
  <p class="ad-muted" style="margin-top: 4px; margin-bottom: 20px; font-size: 13px;">
    All projects across <span class="ad-mono">~/.claude/projects/</span> and <span class="ad-mono">~/.codex/sessions/</span>.
  </p>

  <!-- Filters -->
  <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 12px;">
    <input
      class="ad-input"
      placeholder="Filter by name…"
      bind:value={nameFilter}
      style="max-width: 280px;"
    />

    <!-- CLI filter pill -->
    <div style="display: flex; align-items: center; gap: 4px; background: var(--ad-bg-2); border: 1px solid var(--ad-border); border-radius: 4px; padding: 2px 4px 2px 10px; height: 30px;">
      <span style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">cli:</span>
      <select
        bind:value={cliFilter}
        style="background: transparent; border: 0; outline: 0; color: var(--ad-fg); font-size: 12px; font-weight: 500; padding-right: 4px; cursor: pointer;"
      >
        <option value="all">all</option>
        <option value="claude">claude</option>
        <option value="codex">codex</option>
      </select>
    </div>

    <!-- Sort pill -->
    <div style="display: flex; align-items: center; gap: 4px; background: var(--ad-bg-2); border: 1px solid var(--ad-border); border-radius: 4px; padding: 2px 4px 2px 10px; height: 30px;">
      <span style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">sort:</span>
      <select
        bind:value={sortBy}
        style="background: transparent; border: 0; outline: 0; color: var(--ad-fg); font-size: 12px; font-weight: 500; padding-right: 4px; cursor: pointer;"
      >
        <option value="recent">Recent</option>
        <option value="name">Name</option>
        <option value="msgs">Messages</option>
        <option value="tokens">Tokens</option>
        <option value="sessions">Sessions</option>
      </select>
    </div>

    <div style="margin-left: auto; font-size: 12px; color: var(--ad-faint);" class="ad-mono">
      {sorted.length} of {projects.length}
    </div>
  </div>

  {#if projectsStore.loading}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-muted);">Loading projects…</div>
  {:else if projectsStore.error}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error);">{projectsStore.error}</div>
  {:else}
    <div class="ad-card" style="overflow: hidden;">
      <table class="ad-table">
        <thead>
          <tr>
            <th style="width: 24px;"></th>
            <th>Project</th>
            <th>CLI</th>
            <th>Model</th>
            <th class="num">Sessions</th>
            <th class="num">Messages</th>
            <th class="num">↓ Tokens</th>
            <th>Last active</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as p}
            <tr onclick={() => goto(`/projects/${encodeURIComponent(p.name)}`)}>
              <td><span class="ad-dot ad-dot--{p.status}"></span></td>
              <td style="font-weight: 500; color: var(--ad-fg);">{p.name}</td>
              <td>
                <span style="display: inline-flex; gap: 4px; flex-wrap: wrap;">
                  {#each p.clis as c}
                    <CliBadge cli={c} />
                  {/each}
                </span>
              </td>
              <td class="ad-mono" style="font-size: 11px; color: var(--ad-muted);">{p.model}</td>
              <td class="num">{p.sessions}</td>
              <td class="num">{p.msgs}</td>
              <td class="num">{kfmt(p.tokensOut)}</td>
              <td class="ad-mono" style="font-size: 11px; color: var(--ad-muted);">
                {relAgo(p.lastMsAgo)}
              </td>
              <td style="color: var(--ad-faint); font-size: 14px;">›</td>
            </tr>
          {/each}
          {#if sorted.length === 0}
            <tr>
              <td colspan="9" style="text-align: center; padding: 24px; color: var(--ad-muted);">
                No projects found.
              </td>
            </tr>
          {/if}
        </tbody>
      </table>
    </div>
  {/if}
</div>
