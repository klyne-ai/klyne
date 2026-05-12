<!--
  SearchOverlay — full-screen search dialog opened via the `/` key or
  by clicking the nav search field. Fetches results from /search with
  debounced input. Click a result to jump to that session.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { search } from '$lib/api.js';
  import type { SearchHit } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  interface Props {
    open: boolean;
    onClose: () => void;
  }
  const { open, onClose }: Props = $props();

  let q = $state('');
  let cli = $state<'all' | 'claude' | 'codex'>('all');
  let role = $state<'all' | 'user' | 'assistant' | 'tool'>('all');
  let hits = $state<SearchHit[]>([]);
  let tookMs = $state(0);
  let loading = $state(false);
  let inputEl: HTMLInputElement | null = $state(null);

  let timer: ReturnType<typeof setTimeout> | null = null;

  function runQuery(): void {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(async () => {
      const term = q.trim();
      if (!term) { hits = []; tookMs = 0; return; }
      loading = true;
      try {
        const resp = await search(term, 50, 'recent');
        const filtered = resp.hits.filter((h) => {
          if (cli !== 'all' && h.cli !== cli) return false;
          if (role !== 'all' && h.role !== role) return false;
          return true;
        });
        hits = filtered;
        tookMs = resp.took_ms;
      } catch {
        hits = [];
      } finally {
        loading = false;
      }
    }, 180);
  }

  // Re-run whenever filters change after user has typed something.
  $effect(() => {
    void cli;
    void role;
    if (q.trim()) runQuery();
  });

  function onInput(): void { runQuery(); }

  function pickHit(h: SearchHit): void {
    onClose();
    void goto(`/sessions/${encodeURIComponent(h.session_id)}`);
  }

  function basename(p: string): string {
    const m = p.replace(/\/$/, '');
    const i = m.lastIndexOf('/');
    return i >= 0 ? m.slice(i + 1) : m;
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') { e.preventDefault(); onClose(); }
  }

  $effect(() => {
    if (open) setTimeout(() => inputEl?.focus(), 10);
  });

  onMount(() => { window.addEventListener('keydown', onKey); });
  onDestroy(() => { window.removeEventListener('keydown', onKey); if (timer) clearTimeout(timer); });
</script>

{#if open}
  <div class="overlay" onclick={onClose} role="presentation">
    <div class="search-modal" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} role="dialog" tabindex="-1" aria-modal="true" aria-label="Search">
      <input
        bind:this={inputEl}
        bind:value={q}
        oninput={onInput}
        placeholder="Search messages, sessions, projects… (FTS5)"
        autocomplete="off"
      />
      <div class="filters">
        <span class="faint mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.10em; font-weight: 600;">cli</span>
        <div class="seg">
          {#each ['all', 'claude', 'codex'] as x}
            <button class:active={cli === x} onclick={() => (cli = x as typeof cli)}>{x}</button>
          {/each}
        </div>
        <span class="faint mono" style="font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.10em; margin-left: 6px; font-weight: 600;">role</span>
        <div class="seg">
          {#each ['all', 'user', 'assistant', 'tool'] as x}
            <button class:active={role === x} onclick={() => (role = x as typeof role)}>{x}</button>
          {/each}
        </div>
        <span class="spacer"></span>
        <span class="faint mono" style="font-size: 10.5px;">
          {#if loading}searching…{:else if q.trim()}FTS5 · {hits.length} hits in {tookMs}ms{:else}type to search{/if}
        </span>
      </div>
      <div class="search-results">
        {#if !q.trim()}
          <div style="padding: 26px; color: var(--ad-faint); text-align: center; font-size: 12.5px;">
            Start typing to search across every Claude / Codex message ingested by klyne.
          </div>
        {:else if hits.length === 0 && !loading}
          <div style="padding: 26px; color: var(--ad-faint); text-align: center; font-size: 12.5px;">
            No results for &ldquo;{q}&rdquo;.
          </div>
        {:else}
          {#each hits as h, i (h.message_id)}
            <div class="search-row" style="animation: fadeUp 260ms cubic-bezier(.2,.8,.2,1) {i * 24}ms both;" onclick={() => pickHit(h)} role="button" tabindex="0" onkeydown={(e) => { if (e.key === 'Enter') pickHit(h); }}>
              <span class="where">{basename(h.project_path)} / {h.role}</span>
              <span class="snip">{@html h.snippet}</span>
              <span class="when">{relTime(h.ts)}</span>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}
