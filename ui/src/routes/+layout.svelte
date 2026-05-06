<script lang="ts">
  import '../app.css';
  import type { Snippet } from 'svelte';

  interface Props {
    children: Snippet;
  }

  const { children }: Props = $props();
</script>

<div class="flex h-screen flex-col bg-gray-950 text-gray-100">
  <!-- Header shell — navigation and business components added by W14 -->
  <header class="flex h-12 shrink-0 items-center border-b border-gray-800 px-4">
    <span class="text-sm font-semibold tracking-wide text-gray-200">agentdeck</span>
  </header>

  <!-- Main content area -->
  <main class="flex flex-1 overflow-hidden">
    {@render children()}
  </main>
</div>
