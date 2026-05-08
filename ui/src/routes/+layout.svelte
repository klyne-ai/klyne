<script lang="ts">
  import '../app.css';
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import type { Snippet } from 'svelte';
  import TopNav from '$lib/ui/TopNav.svelte';
  import Sidebar from '$lib/ui/Sidebar.svelte';
  import { subscribe } from '$lib/sse.js';
  import { refreshProjects } from '$lib/projects.svelte.js';
  import { onMsgNew } from '$lib/stores.svelte.js';

  interface Props {
    children: Snippet;
  }

  const { children }: Props = $props();

  // Determine if sidebar should be hidden (wizard page)
  const hideSidebar = $derived($page.url.pathname.startsWith('/wizard'));

  let unsubscribeSSE: (() => void) | null = null;

  // Keyboard shortcuts: g d, g p, g s, g /
  let keyBuffer = $state('');
  let keyTimer: ReturnType<typeof setTimeout> | null = null;

  function handleKeydown(e: KeyboardEvent): void {
    const target = e.target as HTMLElement;
    if (target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA') return;
    if (e.key === 'Escape') { keyBuffer = ''; return; }

    keyBuffer += e.key;
    if (keyTimer) clearTimeout(keyTimer);
    keyTimer = setTimeout(() => { keyBuffer = ''; }, 800);

    if (keyBuffer === 'gd') { void goto('/'); keyBuffer = ''; }
    else if (keyBuffer === 'gp') { void goto('/projects'); keyBuffer = ''; }
    else if (keyBuffer === 'gs') { void goto('/settings'); keyBuffer = ''; }
    else if (keyBuffer === 'g/') { void goto('/search'); keyBuffer = ''; }
  }

  onMount(() => {
    window.addEventListener('keydown', handleKeydown);

    // Initial projects load
    void refreshProjects();

    // Subscribe to SSE for live updates
    unsubscribeSSE = subscribe({
      onMsgNew: (payload) => {
        onMsgNew(payload);
        // Refresh project aggregates on new messages
        void refreshProjects();
      },
      onSessionUpdate: () => {
        void refreshProjects();
      },
    });
  });

  onDestroy(() => {
    window.removeEventListener('keydown', handleKeydown);
    if (keyTimer) clearTimeout(keyTimer);
    unsubscribeSSE?.();
    unsubscribeSSE = null;
  });
</script>

<svelte:head>
  <title>klyne</title>
</svelte:head>

<div style="height: 100vh; display: flex; flex-direction: column;">
  <TopNav onsearch={() => goto('/search')} />
  <div style="flex: 1; display: flex; overflow: hidden;">
    {#if !hideSidebar}
      <Sidebar />
    {/if}
    <main style="flex: 1; overflow-y: auto;">
      {@render children()}
    </main>
  </div>
</div>
