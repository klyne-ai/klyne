<script lang="ts">
  import { onMount, onDestroy } from 'svelte';

  interface Props {
    value?: string;
    placeholder?: string;
    debounceMs?: number;
    onsearch?: (query: string) => void;
  }

  let {
    value = $bindable(''),
    placeholder = 'Search messages… (press / to focus)',
    debounceMs = 300,
    onsearch
  }: Props = $props();

  let inputEl: HTMLInputElement | undefined = $state(undefined);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  function handleInput(e: Event): void {
    const target = e.currentTarget as HTMLInputElement;
    value = target.value;

    if (debounceTimer !== null) {
      clearTimeout(debounceTimer);
    }
    debounceTimer = setTimeout(() => {
      onsearch?.(value);
    }, debounceMs);
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      value = '';
      onsearch?.('');
      inputEl?.blur();
    }
  }

  function handleWindowKeydown(e: KeyboardEvent): void {
    const tag = (e.target as HTMLElement).tagName.toLowerCase();
    if (
      e.key === '/' &&
      tag !== 'input' &&
      tag !== 'textarea' &&
      !e.ctrlKey &&
      !e.metaKey
    ) {
      e.preventDefault();
      inputEl?.focus();
    }
  }

  function clearValue(): void {
    value = '';
    onsearch?.('');
    inputEl?.focus();
  }

  /** Expose focus method for parent keyboard handlers. */
  export function focus(): void {
    inputEl?.focus();
  }

  onMount(() => {
    window.addEventListener('keydown', handleWindowKeydown);
  });

  onDestroy(() => {
    window.removeEventListener('keydown', handleWindowKeydown);
    if (debounceTimer !== null) clearTimeout(debounceTimer);
  });
</script>

<div class="relative w-full" data-testid="search-bar">
  <span class="pointer-events-none absolute inset-y-0 left-3 flex items-center text-gray-500">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-4 w-4"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  </span>

  <input
    bind:this={inputEl}
    type="search"
    aria-label="Search messages"
    class="w-full rounded-lg border border-gray-700 bg-gray-800 py-2 pl-10 pr-4 text-sm text-gray-100
           placeholder-gray-500 outline-none transition-colors
           focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
    {placeholder}
    {value}
    oninput={handleInput}
    onkeydown={handleKeydown}
  />

  {#if value}
    <button
      type="button"
      aria-label="Clear search"
      class="absolute inset-y-0 right-3 flex items-center text-gray-500 hover:text-gray-300 transition-colors"
      onclick={clearValue}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-4 w-4"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <line x1="18" y1="6" x2="6" y2="18" />
        <line x1="6" y1="6" x2="18" y2="18" />
      </svg>
    </button>
  {/if}
</div>
