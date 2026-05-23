<script lang="ts">
  import '../app.css';
  import { onMount, onDestroy } from 'svelte';
  import type { Snippet } from 'svelte';
  import TopNav from '$lib/ui/TopNav.svelte';
  import SearchOverlay from '$lib/ui/SearchOverlay.svelte';
  import Shell from '$lib/dashboard/Shell.svelte';
  import { env } from '$env/dynamic/public';
  import { subscribe } from '$lib/sse.js';
  import { refreshProjects } from '$lib/projects.svelte.js';
  import { cockpitStore, refreshCockpit } from '$lib/cockpit.svelte.js';
  import { onMsgNew } from '$lib/stores.svelte.js';

  const useNewShell = env.PUBLIC_DASHBOARD_V2 === '1';

  interface Props {
    children: Snippet;
  }
  const { children }: Props = $props();

  let searchOpen = $state(false);
  let unsubscribeSSE: (() => void) | null = null;
  let tickHandle: ReturnType<typeof setInterval> | null = null;
  let cockpitRefreshHandle: ReturnType<typeof setInterval> | null = null;

  function onKey(e: KeyboardEvent): void {
    const target = e.target as HTMLElement | null;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
    if (e.key === '/') { e.preventDefault(); searchOpen = true; }
    else if (e.key === 'Escape' && searchOpen) { e.preventDefault(); searchOpen = false; }
  }

  onMount(() => {
    window.addEventListener('keydown', onKey);
    void refreshProjects();
    void refreshCockpit();
    // Tick drives liveCount recomputation in the nav without refetching.
    tickHandle = setInterval(() => { cockpitStore.tick = Date.now(); }, 5_000);
    // Periodic safety refresh — catches sessions started in other terminals
    // that never fire SSE during this page's lifetime.
    cockpitRefreshHandle = setInterval(() => { void refreshCockpit(); }, 60_000);
    unsubscribeSSE = subscribe({
      onMsgNew: (payload) => {
        onMsgNew(payload);
        void refreshProjects();
        void refreshCockpit();
      },
      onSessionUpdate: () => {
        void refreshProjects();
        void refreshCockpit();
      }
    });
  });

  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    unsubscribeSSE?.();
    unsubscribeSSE = null;
    if (tickHandle !== null) clearInterval(tickHandle);
    if (cockpitRefreshHandle !== null) clearInterval(cockpitRefreshHandle);
  });
</script>

<svelte:head>
  <title>klyne</title>
</svelte:head>

{#if useNewShell}
  <Shell>{@render children()}</Shell>
{:else}
  <!-- existing TopNav + children — UNCHANGED -->
  <div style="height: 100vh; display: flex; flex-direction: column;">
    <TopNav onsearch={() => (searchOpen = true)} />
    <div style="flex: 1; min-height: 0; overflow: auto;">{@render children()}</div>
  </div>
  <SearchOverlay open={searchOpen} onClose={() => (searchOpen = false)} />
{/if}
