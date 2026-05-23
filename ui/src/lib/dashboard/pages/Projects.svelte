<!--
  Projects — master/detail page.
  Replaces /projects, /projects/[name], and /insights/projects/[name].
  Left: filtered/sorted project list. Right: ProjectPanel with 4 tabs.
  Selected project read from $page.params.name or defaults to first.
-->
<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { projectsDerived, projectsStore } from '$lib/projects.svelte.js';
  import type { ProjectInsight, Session } from '$lib/types.js';
  import { fetchProjectInsights, fetchSessions } from '$lib/api.js';
  import { onMount } from 'svelte';
  import ProjectsList from './projects/ProjectsList.svelte';
  import ProjectPanel from './projects/ProjectPanel.svelte';

  // --- Filter + sort state ---
  let nameFilter = $state('');
  let cliFilter = $state<'all' | 'claude' | 'codex'>('all');
  let sortBy = $state<'recent' | 'sessions' | 'messages' | 'name'>('recent');

  // --- Derived list ---
  const sorted = $derived.by(() => {
    let p = projectsDerived.projects;
    if (cliFilter !== 'all') {
      const want = cliFilter;
      p = p.filter((x) => x.clis.includes(want));
    }
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      p = p.filter((x) => x.name.toLowerCase().includes(q));
    }
    const sorters: Record<string, (a: typeof p[0], b: typeof p[0]) => number> = {
      recent:   (a, b) => a.lastMsAgo - b.lastMsAgo,
      sessions: (a, b) => b.sessions - a.sessions,
      messages: (a, b) => b.msgs - a.msgs,
      name:     (a, b) => a.name.localeCompare(b.name),
    };
    return [...p].sort(sorters[sortBy] ?? sorters.recent);
  });

  // --- Selected project ---
  // Prefer $page.params.name; fallback to first in sorted list.
  const selectedName = $derived.by((): string | null => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const paramName = ($page.params as Record<string, string>)['name'];
    if (paramName) return decodeURIComponent(paramName);
    return sorted[0]?.name ?? null;
  });

  const selectedProject = $derived(
    selectedName
      ? (projectsDerived.projects.find((p) => p.name === selectedName) ?? sorted[0] ?? null)
      : (sorted[0] ?? null)
  );

  // --- Insights (for overview + files tabs) ---
  let insights = $state<ProjectInsight[]>([]);
  let insightsLoading = $state(false);

  async function loadInsights(): Promise<void> {
    if (insightsLoading) return;
    insightsLoading = true;
    try {
      const resp = await fetchProjectInsights({});
      insights = resp.projects;
    } catch {
      // non-fatal; tabs degrade gracefully
    } finally {
      insightsLoading = false;
    }
  }

  // Insight for the currently selected project
  const selectedInsight = $derived(
    selectedProject
      ? (insights.find((i) => i.project_path === selectedProject.project_path) ?? null)
      : null
  );

  // --- Sessions for the selected project ---
  let projectSessions = $state<Session[]>([]);
  let sessionsLoading = $state(false);
  let lastLoadedPath = $state('');

  async function loadSessions(projectPath: string): Promise<void> {
    if (sessionsLoading || lastLoadedPath === projectPath) return;
    sessionsLoading = true;
    lastLoadedPath = projectPath;
    try {
      const resp = await fetchSessions({ project: projectPath, limit: 200 });
      projectSessions = resp.sessions;
    } catch {
      projectSessions = [];
    } finally {
      sessionsLoading = false;
    }
  }

  // Load sessions when selected project changes
  $effect(() => {
    if (selectedProject) {
      if (lastLoadedPath !== selectedProject.project_path) {
        lastLoadedPath = ''; // reset so loadSessions proceeds
        void loadSessions(selectedProject.project_path);
      }
    }
  });

  onMount(() => {
    void loadInsights();
  });
</script>

<div style="padding: 20px 24px 40px; max-width: 1400px;">
  <!-- Page header -->
  <header style="margin-bottom: 16px;">
    <div style="display: flex; align-items: baseline; justify-content: space-between;">
      <h1 style="font-size: var(--ad-fs-2xl, 22px); font-weight: 600; letter-spacing: -0.02em; margin: 0;">
        Projects
        <span style="color: var(--ad-faint); font-weight: 400; margin-left: 8px; font-size: 14px;">
          ({sorted.length}{sorted.length !== projectsDerived.projects.length ? ` of ${projectsDerived.projects.length}` : ''})
        </span>
      </h1>
    </div>
    <p style="margin: 4px 0 0; font-size: 13px; color: var(--ad-faint);">
      Every project indexed under
      <span class="ad-mono">~/.claude/projects/</span> and
      <span class="ad-mono">~/.codex/sessions/</span>.
      Tabs unify sessions, worklog reflections, token economics, and tooling.
    </p>
  </header>

  <!-- Toolbar -->
  <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 16px; flex-wrap: wrap;">
    <!-- Filter input -->
    <div style="position: relative; min-width: 220px;">
      <input
        class="ad-input"
        placeholder="Filter projects…"
        bind:value={nameFilter}
        style="padding-left: 10px; width: 100%;"
      />
    </div>

    <!-- CLI filter -->
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

    <!-- Sort -->
    <div style="display: flex; align-items: center; gap: 4px; background: var(--ad-bg-2); border: 1px solid var(--ad-border); border-radius: 4px; padding: 2px 4px 2px 10px; height: 30px;">
      <span style="font-size: 11px; color: var(--ad-faint); text-transform: uppercase; letter-spacing: 0.06em;">sort:</span>
      <select
        bind:value={sortBy}
        style="background: transparent; border: 0; outline: 0; color: var(--ad-fg); font-size: 12px; font-weight: 500; padding-right: 4px; cursor: pointer;"
      >
        <option value="recent">recent</option>
        <option value="sessions">sessions</option>
        <option value="messages">messages</option>
        <option value="name">name</option>
      </select>
    </div>

    <!-- Legend -->
    <div style="margin-left: auto; display: flex; gap: 14px; font-size: 11px; color: var(--ad-faint); align-items: center;">
      <span style="display: flex; align-items: center; gap: 5px;">
        <span class="ad-dot ad-dot--active"></span> active
      </span>
      <span style="display: flex; align-items: center; gap: 5px;">
        <span class="ad-dot ad-dot--idle"></span> idle
      </span>
      <span style="display: flex; align-items: center; gap: 5px;">
        <span class="ad-dot ad-dot--compacted"></span> compacted
      </span>
    </div>
  </div>

  {#if projectsStore.loading && projectsDerived.projects.length === 0}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-faint);">Loading projects…</div>
  {:else if projectsStore.error}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-error, #c33);">{projectsStore.error}</div>
  {:else if sorted.length === 0}
    <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-faint);">No projects found.</div>
  {:else}
    <!-- Master/detail grid -->
    <div
      style="
        display: grid;
        grid-template-columns: minmax(260px, 300px) minmax(0, 1fr);
        gap: 16px;
        align-items: start;
      "
    >
      <ProjectsList projects={sorted} selectedName={selectedName} sort={sortBy} />

      {#if selectedProject}
        <ProjectPanel
          project={selectedProject}
          insight={selectedInsight}
          sessions={projectSessions}
        />
      {:else}
        <div class="ad-card" style="padding: 24px; text-align: center; color: var(--ad-faint);">
          Select a project to see details.
        </div>
      {/if}
    </div>
  {/if}
</div>
