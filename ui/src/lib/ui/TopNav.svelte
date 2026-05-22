<!--
  TopNav — 4-tab shell.

  Tabs:
    Work     (replaces Dashboard + Cockpit + Projects)
    Runbooks (decision / ops-annotation review; the recall surface)
    Worklog  (stop_summaries audit — signal vs suppressed)
    Insights (project-centric subscription-aware metrics)

  Owns the global fullscreen toggle (button + F-key) and shows the
  live-sessions count chip pulled from the shared cockpit store.
  Search lives behind the `/` overlay, not a dedicated route.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import Logo from './Logo.svelte';
  import Kbd from './Kbd.svelte';
  import UsageBadge from '$lib/components/UsageBadge.svelte';
  import { liveCount } from '$lib/cockpit.svelte.js';

  interface Props {
    onsearch?: () => void;
  }
  const { onsearch }: Props = $props();

  const tabs = [
    { id: 'work',         label: 'Work',         path: '/' },
    { id: 'productivity', label: 'Productivity', path: '/productivity' },
    { id: 'runbooks',     label: 'Runbooks',     path: '/runbooks' },
    { id: 'worklog',      label: 'Worklog',      path: '/worklog' },
    { id: 'insights',     label: 'Insights',     path: '/insights' }
  ] as const;

  function currentRoute(): string {
    const p = $page.url.pathname;
    // Order matters: check /worklog and /productivity before /work since
    // startsWith('/work') would otherwise match both.
    if (p.startsWith('/worklog')) return 'worklog';
    if (p.startsWith('/productivity')) return 'productivity';
    if (p === '/' || p.startsWith('/work') || p.startsWith('/cockpit') || p.startsWith('/projects') || p.startsWith('/sessions')) return 'work';
    if (p.startsWith('/runbooks')) return 'runbooks';
    if (p.startsWith('/insights') || p.startsWith('/stats')) return 'insights';
    return '';
  }

  const route = $derived(currentRoute());
  const live = $derived(liveCount());

  // Fullscreen targets the .work element when present (Work view) so the
  // body's fixed-attachment gradient survives. On routes without .work the
  // button is a no-op; we hide it there to avoid a dead control.
  let fullscreen = $state(false);

  function toggleFullscreen(): void {
    if (typeof document === 'undefined') return;
    const el = document.querySelector('.work') as HTMLElement | null;
    if (!fullscreen) {
      fullscreen = true;
      if (el?.requestFullscreen) {
        el.requestFullscreen().catch(() => { fullscreen = false; });
      }
    } else {
      fullscreen = false;
      if (document.fullscreenElement && document.exitFullscreen) {
        document.exitFullscreen().catch(() => { /* already exited */ });
      }
    }
  }

  function onFullscreenChange(): void {
    if (typeof document === 'undefined') return;
    if (!document.fullscreenElement && fullscreen) {
      fullscreen = false;
    }
  }

  // F-key shortcut — only fires on the Work route since that's the only
  // place .work exists. Skips when focus is in a text field.
  function onKey(e: KeyboardEvent): void {
    if (route !== 'work') return;
    const target = e.target as HTMLElement | null;
    const inField = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable);
    if (e.key === 'Escape' && fullscreen) { e.preventDefault(); toggleFullscreen(); return; }
    if (!inField && (e.key === 'f' || e.key === 'F') && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      toggleFullscreen();
    }
  }

  $effect(() => {
    if (typeof document === 'undefined') return;
    if (fullscreen) {
      document.body.classList.add('klyne-fullscreen');
    } else {
      document.body.classList.remove('klyne-fullscreen');
    }
    return () => document.body.classList.remove('klyne-fullscreen');
  });

  onMount(() => {
    window.addEventListener('keydown', onKey);
    if (typeof document !== 'undefined') {
      document.addEventListener('fullscreenchange', onFullscreenChange);
    }
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    if (typeof document !== 'undefined') {
      document.removeEventListener('fullscreenchange', onFullscreenChange);
    }
  });
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
    {#if live > 0}
      <span
        class="nav-live"
        title="{live} session{live === 1 ? '' : 's'} streaming right now"
      >
        <span class="nav-live-dot"></span>{live} live
      </span>
    {/if}
    {#if route === 'work'}
      <button
        class="nav-fs"
        onclick={toggleFullscreen}
        title={fullscreen ? 'Exit fullscreen (Esc or F)' : 'Fullscreen (F)'}
        aria-label={fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
      >⛶</button>
    {/if}
    <UsageBadge />
    <div class="avatar">M</div>
  </div>
</nav>

<style>
  .nav-live {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 9px;
    border-radius: 999px;
    background: color-mix(in oklch, var(--ad-active-bg, var(--ad-bg-2)) 28%, transparent);
    border: 1px solid color-mix(in oklch, var(--ad-active, #10b981) 35%, var(--ad-border-soft));
    color: var(--ad-active, #10b981);
    font-family: var(--ad-font-mono);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.02em;
    white-space: nowrap;
  }
  .nav-live-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--ad-active, #10b981);
    animation: nav-live-pulse 1.6s ease-in-out infinite;
  }
  @keyframes nav-live-pulse {
    0%, 100% { opacity: 1; }
    50%      { opacity: 0.45; }
  }
  .nav-fs {
    background: transparent;
    border: 1px solid var(--ad-border-soft);
    color: var(--ad-muted);
    width: 28px;
    height: 28px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 13px;
    line-height: 1;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: background var(--t-fast), color var(--t-fast), border-color var(--t-fast);
  }
  .nav-fs:hover {
    background: var(--ad-panel);
    color: var(--ad-fg);
    border-color: var(--ad-border);
  }
</style>
