<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import Kbd from './Kbd.svelte';

  interface Props {
    onsearch?: () => void;
  }

  const { onsearch }: Props = $props();

  const items = [
    { id: 'dashboard', label: 'Dashboard', path: '/' },
    { id: 'projects',  label: 'Projects',  path: '/projects' },
    { id: 'search',    label: 'Search',    path: '/search' },
  ];

  let searchInputRef: HTMLInputElement | null = $state(null);

  function currentRoute(): string {
    const pathname = $page.url.pathname;
    if (pathname === '/') return 'dashboard';
    if (pathname.startsWith('/projects')) return 'projects';
    if (pathname.startsWith('/search')) return 'search';
    if (pathname.startsWith('/settings')) return 'settings';
    if (pathname.startsWith('/wizard')) return 'wizard';
    return 'dashboard';
  }

  const route = $derived(currentRoute());

  function onKey(e: KeyboardEvent) {
    const target = e.target as HTMLElement;
    if (
      e.key === '/' &&
      target?.tagName !== 'INPUT' &&
      target?.tagName !== 'TEXTAREA'
    ) {
      e.preventDefault();
      searchInputRef?.focus();
    }
  }

  $effect(() => {
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
</script>

<nav style="height: var(--ad-nav-h); display: flex; align-items: center; border-bottom: 1px solid var(--ad-border); padding: 0 16px; gap: 24px; background: var(--ad-bg-2);">
  <!-- Logo -->
  <div style="display: flex; align-items: center; gap: 8px;">
    <div style="width: 18px; height: 18px; border-radius: 4px; background: var(--ad-fg); display: grid; place-items: center; color: var(--ad-bg); font-weight: 800; font-size: 11px; font-family: var(--ad-font-mono);">a</div>
    <span style="font-weight: 600; letter-spacing: -0.01em;">agentdeck</span>
  </div>

  <!-- Nav items -->
  <div style="display: flex; gap: 4px;">
    {#each items as item}
      <button
        class="ad-btn ad-btn--ghost"
        style="color: {route === item.id ? 'var(--ad-fg)' : 'var(--ad-muted)'}; background: {route === item.id ? 'var(--ad-panel)' : 'transparent'}; font-weight: {route === item.id ? 600 : 500};"
        onclick={() => goto(item.path)}
      >{item.label}</button>
    {/each}
  </div>

  <!-- Search -->
  <div style="flex: 1; max-width: 480px; position: relative;">
    <input
      bind:this={searchInputRef}
      class="ad-input"
      placeholder="Search messages, sessions, projects…"
      style="padding-left: 30px; padding-right: 36px;"
      onfocus={() => onsearch?.()}
    />
    <span style="position: absolute; left: 10px; top: 7px; color: var(--ad-faint); font-family: var(--ad-font-mono); font-size: 12px;">⌕</span>
    <span style="position: absolute; right: 8px; top: 5px;"><Kbd>/</Kbd></span>
  </div>

  <!-- Right actions -->
  <div style="margin-left: auto; display: flex; gap: 4px; align-items: center;">
    <button class="ad-btn ad-btn--ghost" onclick={() => goto('/wizard')}>Wizard</button>
    <button class="ad-btn ad-btn--ghost" onclick={() => goto('/settings')}>⚙ Settings</button>
    <div style="width: 26px; height: 26px; border-radius: 50%; background: var(--ad-claude-bg); color: var(--ad-claude); display: grid; place-items: center; font-size: 11px; font-weight: 700; margin-left: 6px;">M</div>
  </div>
</nav>
