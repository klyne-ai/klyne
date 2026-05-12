<script lang="ts">
  import '../app.css';
  import { onMount, onDestroy } from 'svelte';
  import type { Snippet } from 'svelte';
  import TopNav from '$lib/ui/TopNav.svelte';
  import SearchOverlay from '$lib/ui/SearchOverlay.svelte';
  import { subscribe } from '$lib/sse.js';
  import { refreshProjects } from '$lib/projects.svelte.js';
  import { onMsgNew } from '$lib/stores.svelte.js';

  interface Props {
    children: Snippet;
  }
  const { children }: Props = $props();

  let searchOpen = $state(false);
  let unsubscribeSSE: (() => void) | null = null;

  function onKey(e: KeyboardEvent): void {
    const target = e.target as HTMLElement | null;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
    if (e.key === '/') { e.preventDefault(); searchOpen = true; }
    else if (e.key === 'Escape' && searchOpen) { e.preventDefault(); searchOpen = false; }
  }

  onMount(() => {
    window.addEventListener('keydown', onKey);
    void refreshProjects();
    unsubscribeSSE = subscribe({
      onMsgNew: (payload) => {
        onMsgNew(payload);
        void refreshProjects();
      },
      onSessionUpdate: () => { void refreshProjects(); }
    });
  });

  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    unsubscribeSSE?.();
    unsubscribeSSE = null;
  });
</script>

<svelte:head>
  <title>klyne</title>
</svelte:head>

<div style="height: 100vh; display: flex; flex-direction: column;">
  <TopNav onsearch={() => (searchOpen = true)} />
  <div style="flex: 1; min-height: 0; overflow: auto;">
    {@render children()}
  </div>
</div>
<SearchOverlay open={searchOpen} onClose={() => (searchOpen = false)} />
