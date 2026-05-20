<!--
  RangeBar — date-range picker + explicit refresh for the productivity
  dashboard.

  Owns the selected window via two-way props. Edits are debounced into
  a single onChange notification so the page reloads once per
  meaningful adjustment, not on each individual date keystroke. The
  parent (+page.svelte) persists the selection to localStorage and the
  URL.
-->
<script lang="ts">
  interface Props {
    since: number;
    until: number;
    /** Notified when the user picks a new range. */
    onChange: (since: number, until: number) => void;
    /** Notified when the user clicks Refresh. */
    onRefresh: () => void;
    /** True while a fetch is in flight (disables Refresh; spins icon). */
    loading: boolean;
    /** When the current data was loaded (for the "loaded N ago" badge). */
    loadedAt: number | null;
  }

  const { since, until, onChange, onRefresh, loading, loadedAt }: Props = $props();

  // ---- date <-> epoch helpers ---------------------------------------------

  /** YYYY-MM-DD for an epoch-ms, in the user's local zone. */
  function dateInputValue(ms: number): string {
    const d = new Date(ms);
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }

  /** Local midnight at the start of a date input value. */
  function startOfDay(value: string): number {
    const [y, m, d] = value.split('-').map(Number);
    return new Date(y, m - 1, d, 0, 0, 0, 0).getTime();
  }

  /** Last millisecond of a date input value (inclusive end-of-day). */
  function endOfDay(value: string): number {
    const [y, m, d] = value.split('-').map(Number);
    return new Date(y, m - 1, d, 23, 59, 59, 999).getTime();
  }

  // ---- preset ranges ------------------------------------------------------

  function todayRange(): { since: number; until: number } {
    const now = new Date();
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    return { since: start, until: now.getTime() };
  }
  function yesterdayRange(): { since: number; until: number } {
    const now = new Date();
    const todayMid = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    return { since: todayMid - 24 * 60 * 60 * 1000, until: todayMid - 1 };
  }
  function lastNDaysRange(n: number): { since: number; until: number } {
    const now = new Date();
    const todayMid = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
    return { since: todayMid - (n - 1) * 24 * 60 * 60 * 1000, until: now.getTime() };
  }

  function applyPreset(r: { since: number; until: number }): void {
    onChange(r.since, r.until);
  }

  // Detect which preset the CURRENT range matches so we can visually
  // highlight the corresponding button. Date-based (not exact-epoch)
  // so a "Today" pick stays highlighted as `until=now` drifts during
  // the day.
  const activePreset = $derived.by(() => {
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);
    const sevenAgo = new Date(today);
    sevenAgo.setDate(sevenAgo.getDate() - 6);

    const sd = new Date(since);
    const ud = new Date(until);
    const sameDate = (a: Date, b: Date) =>
      a.getFullYear() === b.getFullYear() &&
      a.getMonth() === b.getMonth() &&
      a.getDate() === b.getDate();

    if (sameDate(sd, today) && sameDate(ud, today)) return 'today';
    if (sameDate(sd, yesterday) && sameDate(ud, yesterday)) return 'yesterday';
    if (sameDate(sd, sevenAgo) && sameDate(ud, today)) return 'last7';
    return null;
  });

  // ---- date input change --------------------------------------------------

  function onFromChange(e: Event): void {
    const v = (e.target as HTMLInputElement).value;
    if (!v) return;
    const newSince = startOfDay(v);
    // If from > until, snap until to end-of-same-day so the range stays valid.
    const newUntil = newSince > until ? endOfDay(v) : until;
    onChange(newSince, newUntil);
  }

  function onToChange(e: Event): void {
    const v = (e.target as HTMLInputElement).value;
    if (!v) return;
    const newUntil = endOfDay(v);
    const newSince = newUntil < since ? startOfDay(v) : since;
    onChange(newSince, newUntil);
  }

  // ---- "loaded N ago" -----------------------------------------------------

  // Auto-tick once a minute so the relative time stays current without a
  // full re-render.
  let tickNow = $state(Date.now());
  $effect(() => {
    const id = setInterval(() => (tickNow = Date.now()), 30_000);
    return () => clearInterval(id);
  });

  const loadedAgo = $derived.by(() => {
    if (!loadedAt) return '';
    const sec = Math.max(0, Math.round((tickNow - loadedAt) / 1000));
    if (sec < 30) return 'just now';
    if (sec < 90) return '1m ago';
    const mins = Math.round(sec / 60);
    if (mins < 60) return `${mins}m ago`;
    const h = Math.floor(mins / 60);
    return `${h}h ago`;
  });

  // ---- range-summary label ------------------------------------------------

  // "19 May" / "19 May → 20 May" depending on whether the window
  // covers one or several local days.
  const rangeSummary = $derived.by(() => {
    const f = new Date(since);
    const t = new Date(until);
    const fmt = (d: Date) =>
      d.toLocaleDateString([], { day: '2-digit', month: 'short' });
    if (fmt(f) === fmt(t)) return fmt(f);
    return `${fmt(f)} → ${fmt(t)}`;
  });
</script>

<div class="rb">
  <div class="rb-left">
    <span class="rb-label">Range</span>
    <input
      type="date"
      class="rb-date"
      value={dateInputValue(since)}
      onchange={onFromChange}
      aria-label="From date"
    />
    <span class="rb-arrow" aria-hidden="true">→</span>
    <input
      type="date"
      class="rb-date"
      value={dateInputValue(until)}
      onchange={onToChange}
      aria-label="To date"
    />
    <span class="rb-summary ad-mono ad-tnum">{rangeSummary}</span>
  </div>

  <div class="rb-presets" role="group" aria-label="Quick range presets">
    <button
      type="button"
      class="rb-preset"
      class:rb-preset--active={activePreset === 'today'}
      aria-pressed={activePreset === 'today'}
      onclick={() => applyPreset(todayRange())}
    >
      Today
    </button>
    <button
      type="button"
      class="rb-preset"
      class:rb-preset--active={activePreset === 'yesterday'}
      aria-pressed={activePreset === 'yesterday'}
      onclick={() => applyPreset(yesterdayRange())}
    >
      Yesterday
    </button>
    <button
      type="button"
      class="rb-preset"
      class:rb-preset--active={activePreset === 'last7'}
      aria-pressed={activePreset === 'last7'}
      onclick={() => applyPreset(lastNDaysRange(7))}
    >
      Last 7 days
    </button>
  </div>

  <div class="rb-right">
    {#if loadedAgo}
      <span class="rb-loaded ad-mono" aria-live="polite">loaded {loadedAgo}</span>
    {/if}
    <button
      type="button"
      class="rb-refresh"
      onclick={onRefresh}
      disabled={loading}
      title="Force-refresh: bypass PR and git-fetch caches"
      aria-label="Refresh — force a full re-fetch"
    >
      <span class="rb-refresh-icon" class:rb-refresh-icon--spin={loading} aria-hidden="true">↻</span>
      <span class="rb-refresh-text">{loading ? 'Refreshing…' : 'Refresh'}</span>
    </button>
  </div>
</div>

<style>
  .rb {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
    padding: 10px 14px;
    background: var(--ad-panel);
    border: 1px solid var(--ad-border-soft);
    border-radius: 10px;
  }

  .rb-left,
  .rb-right,
  .rb-presets {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  .rb-right {
    margin-left: auto;
  }

  .rb-label {
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ad-faint);
  }

  .rb-date {
    font: inherit;
    font-size: 12px;
    color: var(--ad-fg);
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    border-radius: 6px;
    padding: 4px 6px;
    color-scheme: dark light;
  }
  .rb-date:focus {
    outline: 1px solid var(--ad-codex);
    outline-offset: 1px;
  }

  .rb-arrow {
    color: var(--ad-faint);
    font-size: 12px;
  }

  .rb-summary {
    font-size: 11px;
    color: var(--ad-muted);
    padding: 2px 8px;
    border-radius: 5px;
    background: var(--ad-bg-2);
    border: 1px solid var(--ad-border-soft);
    white-space: nowrap;
  }

  .rb-preset {
    font: inherit;
    font-size: 11px;
    font-weight: 500;
    color: var(--ad-fg-2);
    background: transparent;
    border: 1px solid var(--ad-border-soft);
    border-radius: 6px;
    padding: 4px 9px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease, border-color 100ms ease;
  }
  .rb-preset:hover {
    background: var(--ad-bg-2);
    color: var(--ad-fg);
  }
  /* Active preset: the current range matches this preset's window. */
  .rb-preset--active {
    color: var(--ad-fg);
    font-weight: 600;
    background: color-mix(in oklch, var(--ad-codex) 16%, transparent);
    border-color: color-mix(in oklch, var(--ad-codex) 40%, var(--ad-border));
  }
  .rb-preset--active:hover {
    background: color-mix(in oklch, var(--ad-codex) 22%, transparent);
  }

  .rb-loaded {
    font-size: 10.5px;
    color: var(--ad-faint);
    white-space: nowrap;
  }

  .rb-refresh {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    color: var(--ad-fg);
    background: color-mix(in oklch, var(--ad-codex) 16%, transparent);
    border: 1px solid color-mix(in oklch, var(--ad-codex) 40%, var(--ad-border));
    border-radius: 6px;
    padding: 5px 12px;
    cursor: pointer;
    transition: filter 100ms ease;
  }
  .rb-refresh:hover:not(:disabled) {
    filter: brightness(1.1);
  }
  .rb-refresh:disabled {
    cursor: default;
    opacity: 0.7;
  }

  .rb-refresh-icon {
    display: inline-block;
    font-size: 14px;
    line-height: 1;
  }
  .rb-refresh-icon--spin {
    animation: rb-spin 900ms linear infinite;
  }
  @keyframes rb-spin {
    from {
      transform: rotate(0deg);
    }
    to {
      transform: rotate(360deg);
    }
  }

  @media (max-width: 720px) {
    .rb-right {
      margin-left: 0;
    }
  }
</style>
