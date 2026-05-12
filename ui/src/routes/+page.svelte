<!--
  Work view — replaces Dashboard + Cockpit + Projects with one screen:

    Left rail   : every project, with live dots, filter, pin star
    Center      : 1-3 column terminal grid of pinned projects, streaming tail
    Right panel : inspector for the currently-selected project

  Pin selection is persisted in localStorage. The advisor modal
  surfaces from each terminal's ⓘ button.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { projectsStore } from '$lib/projects.svelte.js';
  import type { ProjectAggregate } from '$lib/projects.svelte.js';
  import ProjectRail from '$lib/ui/ProjectRail.svelte';
  import Terminal from '$lib/ui/Terminal.svelte';
  import Inspector from '$lib/ui/Inspector.svelte';
  import AdvisorModal from '$lib/components/AdvisorModal.svelte';

  const LS_PINS = 'klyne.work.pins';
  const LS_COLS = 'klyne.work.cols';

  function loadPins(): string[] {
    if (typeof localStorage === 'undefined') return [];
    try {
      const raw = localStorage.getItem(LS_PINS);
      if (!raw) return [];
      const parsed: unknown = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
    } catch { return []; }
  }
  function savePins(p: string[]): void {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(LS_PINS, JSON.stringify(p));
  }
  function loadCols(): 1 | 2 | 3 {
    if (typeof localStorage === 'undefined') return 2;
    const v = parseInt(localStorage.getItem(LS_COLS) ?? '2', 10);
    return v === 1 || v === 3 ? v : 2;
  }
  function saveCols(c: number): void {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(LS_COLS, String(c));
  }

  let pinnedPaths = $state<string[]>(loadPins());
  let selectedPath = $state<string>('');
  let inspectorOpen = $state(true);
  let cols = $state<1 | 2 | 3>(loadCols());
  let advisorSession = $state<string | null>(null);
  let focusPath = $state<string | null>(null);

  const projects = $derived(projectsStore.items);
  const liveCount = $derived(projects.filter((p) => p.lastMsAgo < 60_000).length);

  // Auto-seed selected project from the most-recent one when none chosen.
  $effect(() => {
    if (!selectedPath && projects.length > 0) {
      selectedPath = projects[0].project_path;
    }
  });

  // Drop stale pins (deleted projects) once the list loads.
  $effect(() => {
    if (projects.length === 0) return;
    const valid = new Set(projects.map((p) => p.project_path));
    const next = pinnedPaths.filter((p) => valid.has(p));
    if (next.length !== pinnedPaths.length) {
      pinnedPaths = next;
      savePins(next);
    }
  });

  function selectProject(path: string): void {
    selectedPath = path;
    inspectorOpen = true;
  }

  function togglePin(path: string): void {
    pinnedPaths = pinnedPaths.includes(path)
      ? pinnedPaths.filter((p) => p !== path)
      : [...pinnedPaths, path];
    savePins(pinnedPaths);
  }

  function setCols(c: 1 | 2 | 3): void { cols = c; saveCols(c); }

  const selectedProject: ProjectAggregate | null = $derived(
    projects.find((p) => p.project_path === selectedPath) ?? null
  );
  const pinnedProjects = $derived(
    pinnedPaths
      .map((path) => projects.find((p) => p.project_path === path))
      .filter((p): p is ProjectAggregate => p !== undefined)
  );
  const liveSuggestions = $derived(
    projects.filter((p) => p.lastMsAgo < 60_000 && !pinnedPaths.includes(p.project_path)).slice(0, 5)
  );

  const focusProject: ProjectAggregate | null = $derived(
    focusPath ? projects.find((p) => p.project_path === focusPath) ?? null : null
  );

  function onFocusModalKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') { e.preventDefault(); focusPath = null; }
  }
  onMount(() => {
    window.addEventListener('keydown', onFocusModalKey);
    return () => window.removeEventListener('keydown', onFocusModalKey);
  });
</script>

<svelte:head><title>klyne — Work</title></svelte:head>

<div class="work" class:inspector-collapsed={!inspectorOpen}>
  <ProjectRail
    projects={projects}
    selectedPath={selectedPath}
    pinnedPaths={pinnedPaths}
    onSelect={selectProject}
    onTogglePin={togglePin}
  />

  <main class="center">
    <div class="center-hd">
      <h2>Terminals</h2>
      <span class="sub">{pinnedPaths.length} pinned · {liveCount} live across all projects</span>
      <div class="right">
        <div class="seg" role="group" aria-label="Terminal columns">
          <button class:active={cols === 1} onclick={() => setCols(1)}>1 col</button>
          <button class:active={cols === 2} onclick={() => setCols(2)}>2 col</button>
          <button class:active={cols === 3} onclick={() => setCols(3)}>3 col</button>
        </div>
        <button class="btn btn--ghost btn--sm" onclick={() => (inspectorOpen = !inspectorOpen)}>
          {inspectorOpen ? 'hide inspector ›' : '‹ inspector'}
        </button>
      </div>
    </div>

    {#if pinnedProjects.length === 0}
      <div class="term-grid cols-1" style="padding: 32px;">
        <div class="term-empty">
          <div>
            <h3>No terminals pinned</h3>
            <p>
              Pin a project from the rail to watch its most recent thread here.<br />
              Pin up to <strong>6</strong> and watch them stream side-by-side.
            </p>
            {#if liveSuggestions.length > 0}
              <div class="hr" style="width: 200px; margin: 14px auto;"></div>
              <div class="faint mono" style="margin-bottom: 8px; font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.08em;">Live right now</div>
              <div style="display: flex; gap: 6px; flex-wrap: wrap; justify-content: center;">
                {#each liveSuggestions as p (p.project_path)}
                  <button class="btn" onclick={() => togglePin(p.project_path)}>
                    <span class="dot dot--live" style="margin-right: 6px;"></span>{p.name}
                  </button>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      </div>
    {:else}
      <div class="term-grid cols-{cols}">
        {#each pinnedProjects as p (p.project_path)}
          <Terminal
            project={p}
            onUnpin={togglePin}
            onFocus={(path) => (focusPath = path)}
            onInfo={(sid) => (advisorSession = sid)}
          />
        {/each}
      </div>
    {/if}
  </main>

  {#if inspectorOpen && selectedProject}
    <Inspector project={selectedProject} onClose={() => (inspectorOpen = false)} />
  {/if}
</div>

{#if focusProject}
  <div class="overlay" onclick={() => (focusPath = null)} role="presentation">
    <div class="search-modal" style="width: min(960px, 92vw); height: 78vh; display: flex; flex-direction: column;" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} role="dialog" tabindex="-1" aria-modal="true" aria-label="Focused terminal">
      <Terminal
        project={focusProject}
        onUnpin={() => (focusPath = null)}
        onFocus={() => {}}
        onInfo={(sid) => (advisorSession = sid)}
      />
    </div>
  </div>
{/if}

{#if advisorSession}
  <AdvisorModal sessionId={advisorSession} onClose={() => (advisorSession = null)} />
{/if}
