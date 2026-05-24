<script lang="ts">
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import Sidebar from './Sidebar.svelte';
  import Topbar from './Topbar.svelte';
  import SearchPalette from './SearchPalette.svelte';
  import SessionDrawer from './SessionDrawer.svelte';
  import { navIdForPath, crumbsForNavId } from './nav';
  import { parseDashboardSearch, paletteUrl, sessionUrl } from './url-state';
  import { cockpitDerived } from '$lib/cockpit.svelte.js';
  import { projectsDerived } from '$lib/projects.svelte.js';
  import type { Snippet } from 'svelte';

  interface Props { children: Snippet; rightSlot?: Snippet; }
  const { children, rightSlot }: Props = $props();

  const navId = $derived(navIdForPath($page.url.pathname));
  const crumbs = $derived(crumbsForNavId(navId));
  const search = $derived(parseDashboardSearch($page.url));

  // Re-sync `collapsed` to the live-vs-other default on every nav change,
  // unless the user has manually toggled this session.
  let userToggled = $state(false);
  let collapsed = $state(false);
  $effect(() => { if (!userToggled) collapsed = navId === 'live'; });

  function navigate(url: string): void { void goto(url); }
  function toggleCollapse(): void { userToggled = true; collapsed = !collapsed; }
  function openPalette(): void { navigate(paletteUrl($page.url.pathname + $page.url.search, true)); }
  function closePalette(): void { navigate(paletteUrl($page.url.pathname + $page.url.search, false)); }
  function closeDrawer(): void { navigate(sessionUrl($page.url.pathname + $page.url.search, null)); }

  function onKey(e: KeyboardEvent): void {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); openPalette(); }
    else if (e.key === '/') { e.preventDefault(); openPalette(); }
  }

  const liveCount = $derived(cockpitDerived.liveCount);
  const projectCount = $derived(projectsDerived.projects?.length ?? 0);
</script>

<svelte:window onkeydown={onKey} />

<div class="app" class:collapsed>
  <Sidebar
    current={navId}
    {collapsed}
    {liveCount}
    {projectCount}
    onToggleCollapse={toggleCollapse}
    onOpenSearch={openPalette}
  />
  <div class="main">
    <Topbar {crumbs} daemonStatus={cockpitDerived.daemonStatus} {rightSlot} />
    <div class="page">{@render children()}</div>
  </div>
</div>

{#if search.session}<SessionDrawer sessionId={search.session} onClose={closeDrawer} />{/if}
{#if search.palette}<SearchPalette onClose={closePalette} />{/if}

<style>
  .app {
    display: grid;
    grid-template-columns: var(--sidebar-w, 220px) 1fr;
    width: 100%; height: 100%;
    background: var(--bg-inset);
    min-width: 0;
    transition: grid-template-columns .18s cubic-bezier(.3,.7,.2,1);
  }
  .app.collapsed { --sidebar-w: 64px; }
  .main {
    display: flex; flex-direction: column;
    min-width: 0;
    overflow: hidden;
  }
  .page {
    flex: 1;
    overflow: auto;
    background: var(--bg-inset);
    /* 60px bottom padding keeps content above the topbar shadow on long pages */
    padding: 22px 26px 60px;
    min-width: 0;
  }
</style>
