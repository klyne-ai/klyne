<script lang="ts">
  import '../app.css';
  import { onMount, onDestroy } from 'svelte';
  import type { Snippet } from 'svelte';
  import Shell from '$lib/dashboard/Shell.svelte';
  import { subscribe } from '$lib/sse.js';
  import { refreshProjects } from '$lib/projects.svelte.js';
  import { cockpitStore, refreshCockpit } from '$lib/cockpit.svelte.js';
  import { refreshAdvisors } from '$lib/advisors.svelte.js';
  import { onMsgNew } from '$lib/stores.svelte.js';
  import { beforeNavigate, goto } from '$app/navigation';

  // Redirects from the old 15-route shell. Safe to delete one release after cutover.
  beforeNavigate(({ to, cancel }) => {
    if (!to) return;
    if (to.url.pathname === '/cockpit') {
      cancel();
      void goto('/');
    }
    if (to.url.pathname.startsWith('/insights/projects/')) {
      const name = to.url.pathname.replace('/insights/projects/', '');
      cancel();
      void goto(`/projects/${name}`);
      return;
    }
    if (to.url.pathname === '/stats') {
      cancel();
      void goto('/insights?tab=daily');
      return;
    }
    if (to.url.pathname.startsWith('/sessions/')) {
      const id = to.url.pathname.slice('/sessions/'.length);
      cancel();
      void goto(`/?session=${encodeURIComponent(id)}`);
      return;
    }
    if (to.url.pathname.startsWith('/insights/sessions/')) {
      const id = to.url.pathname.slice('/insights/sessions/'.length);
      cancel();
      void goto(`/insights?session=${encodeURIComponent(id)}`);
      return;
    }
    if (to.url.pathname === '/search') {
      cancel();
      void goto('/?palette=1');
      return;
    }
  });

  interface Props {
    children: Snippet;
  }
  const { children }: Props = $props();

  let unsubscribeSSE: (() => void) | null = null;
  let tickHandle: ReturnType<typeof setInterval> | null = null;
  let cockpitRefreshHandle: ReturnType<typeof setInterval> | null = null;
  let advisorRefreshHandle: ReturnType<typeof setInterval> | null = null;

  onMount(() => {
    void refreshProjects();
    void refreshCockpit();
    void refreshAdvisors();
    // Tick drives liveCount recomputation in the nav without refetching.
    tickHandle = setInterval(() => { cockpitStore.tick = Date.now(); }, 5_000);
    // Periodic safety refresh — catches sessions started in other terminals
    // that never fire SSE during this page's lifetime.
    cockpitRefreshHandle = setInterval(() => { void refreshCockpit(); }, 60_000);
    advisorRefreshHandle = setInterval(() => { void refreshAdvisors(); }, 60_000);
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
    unsubscribeSSE?.();
    unsubscribeSSE = null;
    if (tickHandle !== null) clearInterval(tickHandle);
    if (cockpitRefreshHandle !== null) clearInterval(cockpitRefreshHandle);
    if (advisorRefreshHandle !== null) clearInterval(advisorRefreshHandle);
  });
</script>

<svelte:head>
  <title>klyne</title>
</svelte:head>

<Shell>{@render children()}</Shell>
