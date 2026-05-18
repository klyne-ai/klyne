<!--
  TopNav — 4-tab shell.

  Tabs:
    Work     (replaces Dashboard + Cockpit + Projects)
    Memory   (decisions / runbooks review)
    Worklog  (stop_summaries audit — signal vs suppressed)
    Insights (project-centric subscription-aware metrics)

  Search lives behind the `/` overlay, not as a dedicated route.
-->
<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import Logo from './Logo.svelte';
  import Kbd from './Kbd.svelte';
  import UsageBadge from '$lib/components/UsageBadge.svelte';

  interface Props {
    onsearch?: () => void;
  }
  const { onsearch }: Props = $props();

  const tabs = [
    { id: 'work',     label: 'Work',     path: '/' },
    { id: 'memory',   label: 'Memory',   path: '/memory' },
    { id: 'worklog',  label: 'Worklog',  path: '/worklog' },
    { id: 'insights', label: 'Insights', path: '/insights' }
  ] as const;

  function currentRoute(): string {
    const p = $page.url.pathname;
    // Order matters: check /worklog before /work since startsWith('/work') would
    // match both.
    if (p.startsWith('/worklog')) return 'worklog';
    if (p === '/' || p.startsWith('/work') || p.startsWith('/cockpit') || p.startsWith('/projects') || p.startsWith('/sessions')) return 'work';
    if (p.startsWith('/memory')) return 'memory';
    if (p.startsWith('/insights') || p.startsWith('/stats')) return 'insights';
    return '';
  }

  const route = $derived(currentRoute());
</script>

<nav class="nav">
  <div class="nav-brand">
    <div class="nav-logo"><Logo size={28} /></div>
    <span class="nav-title">klyne</span>
  </div>

  <div class="nav-tabs">
    {#each tabs as t}
      <button class="nav-tab" class:active={route === t.id} onclick={() => goto(t.path)}>
        {t.label}
      </button>
    {/each}
  </div>

  <div class="nav-search">
    <span class="icon">⌕</span>
    <button class="nav-search-trigger" onclick={() => onsearch?.()} aria-label="Open search">
      Search messages, sessions, projects…
    </button>
    <span class="kbd-wrap"><Kbd>/</Kbd></span>
  </div>

  <div class="nav-right">
    <UsageBadge />
    <div class="avatar">M</div>
  </div>
</nav>
