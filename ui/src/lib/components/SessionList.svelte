<script lang="ts">
  import type { Session } from '$lib/types.js';

  interface Props {
    sessions: Session[];
    loading?: boolean;
    error?: string | null;
    activeId?: string | null;
    highlightIndex?: number;
    onselect?: (session: Session) => void;
  }

  const {
    sessions,
    loading = false,
    error = null,
    activeId = null,
    highlightIndex = -1,
    onselect
  }: Props = $props();

  function handleSelect(session: Session): void {
    onselect?.(session);
  }

  function handleKeydown(e: KeyboardEvent, session: Session): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleSelect(session);
    }
  }

  function relativeTime(tsMs: number): string {
    const diff = Date.now() - tsMs;
    const s = Math.floor(diff / 1000);
    if (s < 60) return `${s}s ago`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    const d = Math.floor(h / 24);
    return `${d}d ago`;
  }

  function projectName(path: string): string {
    return path.split('/').filter(Boolean).pop() ?? path;
  }

  function formatUsd(val: number): string {
    if (val === 0) return '$0.00';
    if (val < 0.01) return `$${val.toFixed(4)}`;
    return `$${val.toFixed(2)}`;
  }
</script>

<section data-testid="session-list" class="flex flex-col h-full">
  {#if loading}
    <!-- Skeleton loading state -->
    <ul data-testid="session-loading" class="flex flex-col gap-1 p-2 animate-pulse">
      {#each [1, 2, 3, 4, 5] as _}
        <li class="rounded-lg bg-gray-800 p-3">
          <div class="h-3 bg-gray-700 rounded w-3/4 mb-2"></div>
          <div class="h-3 bg-gray-700 rounded w-1/2"></div>
        </li>
      {/each}
    </ul>

  {:else if error}
    <!-- Error state -->
    <div
      data-testid="session-error"
      class="flex flex-col items-center justify-center flex-1 gap-3 p-6 text-center"
    >
      <p class="text-red-400 text-sm">{error}</p>
    </div>

  {:else if sessions.length === 0}
    <!-- Empty state -->
    <div
      data-testid="session-empty"
      class="flex flex-col items-center justify-center flex-1 gap-2 p-6 text-center"
    >
      <p class="text-gray-400 text-sm">No sessions yet</p>
      <p class="text-gray-600 text-xs">
        Run <code class="bg-gray-800 px-1 rounded">claude</code> or
        <code class="bg-gray-800 px-1 rounded">codex</code> and they'll appear here.
      </p>
    </div>

  {:else}
    <!-- Session items -->
    <ul
      data-testid="session-items"
      class="flex flex-col gap-1 p-2 overflow-y-auto flex-1"
      role="listbox"
      aria-label="Sessions"
    >
      {#each sessions as session, idx (session.id)}
        <li
          role="option"
          aria-selected={session.id === activeId}
          tabindex="0"
          data-testid="session-item"
          data-session-id={session.id}
          class="flex flex-col gap-1 rounded-lg px-3 py-2.5 cursor-pointer transition-colors select-none
                 {session.id === activeId ? 'bg-blue-900/60 border border-blue-700/50' : 'hover:bg-gray-800 border border-transparent'}
                 {idx === highlightIndex ? 'ring-1 ring-blue-400' : ''}"
          onclick={() => handleSelect(session)}
          onkeydown={(e) => handleKeydown(e, session)}
        >
          <!-- Row 1: project + CLI badge + time -->
          <div class="flex items-center justify-between gap-2">
            <span
              class="text-sm font-medium text-gray-100 truncate flex-1"
              title={session.project_path}
            >
              {projectName(session.project_path)}
            </span>

            <span
              class="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium
                     {session.cli === 'claude' ? 'bg-purple-900 text-purple-300' : 'bg-blue-900 text-blue-300'}"
            >
              {session.cli}
            </span>

            <time
              class="shrink-0 text-xs text-gray-500"
              datetime={new Date(session.last_msg_at).toISOString()}
            >
              {relativeTime(session.last_msg_at)}
            </time>
          </div>

          <!-- Row 2: msg count + cost -->
          <div class="flex items-center gap-3 text-xs text-gray-500">
            <span>{session.msg_count} msgs</span>
            <span data-testid="session-cost">{formatUsd(session.cost_usd)}</span>
            {#if session.status !== 'idle'}
              <span
                class="rounded px-1 {session.status === 'active'
                  ? 'bg-green-900/50 text-green-400'
                  : 'bg-gray-800 text-gray-500'}"
              >
                {session.status}
              </span>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>
