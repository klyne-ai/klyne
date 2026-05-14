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
  const LS_RAIL = 'klyne.work.rail';
  const LS_INSP = 'klyne.work.inspector';

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
  function loadBool(key: string, fallback: boolean): boolean {
    if (typeof localStorage === 'undefined') return fallback;
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    return raw === 'true';
  }
  function saveBool(key: string, value: boolean): void {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(key, String(value));
  }

  let pinnedPaths = $state<string[]>(loadPins());
  let selectedPath = $state<string>('');
  let railOpen = $state(loadBool(LS_RAIL, true));
  let inspectorOpen = $state(loadBool(LS_INSP, true));
  let cols = $state<1 | 2 | 3>(loadCols());
  let advisorSession = $state<string | null>(null);
  let focusPath = $state<string | null>(null);
  // fullscreen hides both rails AND the top app nav so the terminal
  // grid fills the entire viewport. Esc exits. The previous rail
  // and inspector visibility states are restored on exit so a user
  // who had the inspector hidden before fullscreen does not get it
  // re-opened on the way back.
  let fullscreen = $state(false);
  let savedRailOpen = $state(true);
  let savedInspectorOpen = $state(true);

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
  function toggleRail(): void {
    railOpen = !railOpen;
    saveBool(LS_RAIL, railOpen);
  }
  function toggleInspector(): void {
    inspectorOpen = !inspectorOpen;
    saveBool(LS_INSP, inspectorOpen);
  }
  // toggleFullscreen hides both rails, the top app nav, AND requests
  // OS-level fullscreen on the .work element so the terminal grid
  // covers the entire monitor — past the browser's tab bar and any
  // pinned extension sidebars (e.g. Brave's vertical sidebar).
  //
  // We request fullscreen on .work (a div) rather than
  // document.documentElement because the body uses
  // background-attachment: fixed radial gradients that fail to
  // render under <html>:fullscreen on some browsers, producing a
  // black screen. Putting the fullscreen on a child div keeps the
  // body's painted background intact and gives .work its own
  // background via the :fullscreen pseudo.
  function toggleFullscreen(): void {
    if (!fullscreen) {
      savedRailOpen = railOpen;
      savedInspectorOpen = inspectorOpen;
      railOpen = false;
      inspectorOpen = false;
      fullscreen = true;
      if (typeof document !== 'undefined') {
        const el = document.querySelector('.work') as HTMLElement | null;
        if (el?.requestFullscreen) {
          el.requestFullscreen().catch(() => {
            // Browser refused (no user gesture, blocked by policy,
            // etc.). The app-level body class still gives the user
            // a maximised layout within the browser viewport.
          });
        }
      }
    } else {
      railOpen = savedRailOpen;
      inspectorOpen = savedInspectorOpen;
      fullscreen = false;
      if (typeof document !== 'undefined' && document.fullscreenElement && document.exitFullscreen) {
        document.exitFullscreen().catch(() => { /* already exited */ });
      }
    }
  }

  // Keep the local fullscreen flag in sync with the browser. When
  // the user dismisses OS fullscreen via Esc / the browser's UI, we
  // also restore the rails and nav to avoid a half-state where
  // chrome is hidden but the user is back in a normal window.
  function onFullscreenChange(): void {
    if (typeof document === 'undefined') return;
    if (!document.fullscreenElement && fullscreen) {
      railOpen = savedRailOpen;
      inspectorOpen = savedInspectorOpen;
      fullscreen = false;
    }
  }

  // Apply / remove the body-level class that hides the top nav.
  // Done in an effect so the class is also stripped on
  // component teardown (e.g. SPA route change) without leaving the
  // shell stuck in fullscreen.
  $effect(() => {
    if (typeof document === 'undefined') return;
    if (fullscreen) {
      document.body.classList.add('klyne-fullscreen');
    } else {
      document.body.classList.remove('klyne-fullscreen');
    }
    return () => document.body.classList.remove('klyne-fullscreen');
  });

  const selectedProject: ProjectAggregate | null = $derived(
    projects.find((p) => p.project_path === selectedPath) ?? null
  );
  const pinnedProjects = $derived(
    pinnedPaths
      .map((path) => projects.find((p) => p.project_path === path))
      .filter((p): p is ProjectAggregate => p !== undefined)
  );
  // Auto-include currently-live projects that aren't already pinned, so a
  // running session shows up in the grid without an explicit pin. Pinned
  // projects keep their user-defined order first; live-but-unpinned trail
  // sorted by most-recent activity.
  const liveUnpinned = $derived(
    projects
      .filter((p) => p.lastMsAgo < 60_000 && !pinnedPaths.includes(p.project_path))
      .sort((a, b) => a.lastMsAgo - b.lastMsAgo)
  );
  const visibleProjects = $derived([...pinnedProjects, ...liveUnpinned]);

  const focusProject: ProjectAggregate | null = $derived(
    focusPath ? projects.find((p) => p.project_path === focusPath) ?? null : null
  );

  function onKey(e: KeyboardEvent): void {
    const target = e.target as HTMLElement | null;
    const inField = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable);
    if (e.key === 'Escape') {
      if (focusPath) { e.preventDefault(); focusPath = null; return; }
      if (fullscreen) { e.preventDefault(); toggleFullscreen(); return; }
    }
    // `f` toggles app-level fullscreen. Skipped when typing in a
    // form field so it doesn't fire when filtering projects.
    if (!inField && (e.key === 'f' || e.key === 'F') && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      toggleFullscreen();
    }
  }
  onMount(() => {
    window.addEventListener('keydown', onKey);
    if (typeof document !== 'undefined') {
      document.addEventListener('fullscreenchange', onFullscreenChange);
    }
    return () => {
      window.removeEventListener('keydown', onKey);
      if (typeof document !== 'undefined') {
        document.removeEventListener('fullscreenchange', onFullscreenChange);
      }
    };
  });
</script>

<svelte:head><title>klyne — Work</title></svelte:head>

<div
  class="work"
  class:inspector-collapsed={!inspectorOpen}
  class:rail-collapsed={!railOpen}
  class:work-fullscreen={fullscreen}
>
  <!--
    ProjectRail is always rendered so the CSS grid keeps its three
    tracks (rail, center, inspector) in the same order regardless of
    which side is collapsed. Hiding the rail by removing the element
    would shift the center and inspector into the wrong grid columns
    (the Inspector would land in the wide 1fr middle column and take
    over the viewport). The CSS `.work.rail-collapsed` rule
    collapses the rail's column to 0px and `.rail { overflow: hidden }`
    clips its content — that is what actually hides it visually.
  -->
  <ProjectRail
    projects={projects}
    selectedPath={selectedPath}
    pinnedPaths={pinnedPaths}
    onSelect={selectProject}
    onTogglePin={togglePin}
  />

  <main class="center">
    <div class="center-hd">
      <button
        class="btn btn--ghost btn--sm btn--icon"
        title={railOpen ? 'Hide projects rail' : 'Show projects rail'}
        aria-label={railOpen ? 'Hide projects rail' : 'Show projects rail'}
        onclick={toggleRail}
      >{railOpen ? '‹' : '›'}</button>
      <h2>Terminals</h2>
      <span class="sub">
        {visibleProjects.length} shown · {pinnedPaths.length} pinned · {liveCount} live
      </span>
      <div class="right">
        <div class="seg" role="group" aria-label="Terminal columns">
          <button class:active={cols === 1} onclick={() => setCols(1)}>1 col</button>
          <button class:active={cols === 2} onclick={() => setCols(2)}>2 col</button>
          <button class:active={cols === 3} onclick={() => setCols(3)}>3 col</button>
        </div>
        <button
          class="btn btn--ghost btn--sm btn--icon"
          title={fullscreen ? 'Exit fullscreen (Esc or F)' : 'Fullscreen (F)'}
          aria-label={fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
          onclick={toggleFullscreen}
        >{fullscreen ? '⛶' : '⛶'}</button>
        <button class="btn btn--ghost btn--sm" onclick={toggleInspector}>
          {inspectorOpen ? 'hide inspector ›' : '‹ inspector'}
        </button>
      </div>
    </div>

    {#if visibleProjects.length === 0}
      <div class="term-grid cols-1" style="padding: 32px;">
        <div class="term-empty">
          <div>
            <h3>No active terminals</h3>
            <p>
              Nothing is live right now. Pin a project from the rail to watch its
              most recent thread, or start a session and it will appear here.
            </p>
          </div>
        </div>
      </div>
    {:else}
      <div class="term-grid cols-{cols}">
        {#each visibleProjects as p (p.project_path)}
          <Terminal
            project={p}
            pinned={pinnedPaths.includes(p.project_path)}
            onTogglePin={togglePin}
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
        pinned={pinnedPaths.includes(focusProject.project_path)}
        onTogglePin={() => (focusPath = null)}
        onFocus={() => {}}
        onInfo={(sid) => (advisorSession = sid)}
      />
    </div>
  </div>
{/if}

{#if advisorSession}
  <AdvisorModal sessionId={advisorSession} onClose={() => (advisorSession = null)} />
{/if}
