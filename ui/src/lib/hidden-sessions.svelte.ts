// Hidden-session store — persists which sessions the user has hidden
// from the productivity dashboard.
//
// Scope: localStorage only. There's no server state, no sync across
// devices, no impact on what the daemon ingests — just a per-browser
// "don't show this row" preference. Cheap to add, easy to clear.
//
// Why session_id and not repo: a session is the smallest meaningful
// unit. The user hid the "observer-sessions" CLI which spawns 100+
// sessions tagged with that repo — hiding by repo would also bury
// useful sessions if the repo ever gets real work. We hide individual
// IDs; the user can click hide on each row, or use the "hide all from
// this repo" affordance in the per-repo view.
//
// Side-effects: importing this module reads localStorage once and
// installs a `storage` event listener so multiple tabs stay in sync.

import type { ProductivityReport, ProductivitySessionStat, ProductivityActiveInterval } from './api';

const STORAGE_KEY = 'klyne.productivity.hiddenSessions';
const REPO_STORAGE_KEY = 'klyne.productivity.hiddenRepos';

function load(key = STORAGE_KEY): Set<string> {
  if (typeof localStorage === 'undefined') return new Set();
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((x) => typeof x === 'string'));
    }
  } catch {
    /* ignore corrupt entries */
  }
  return new Set();
}

function persist(ids: Set<string>, key = STORAGE_KEY): void {
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(key, JSON.stringify([...ids]));
  } catch {
    /* quota / private mode → silently degrade */
  }
}

// Reactive Svelte 5 rune-state container.
const state = $state<{ ids: Set<string> }>({ ids: load() });
// Repo-level hidden set — distinct localStorage key, same shape/pattern.
const repoState = $state<{ names: Set<string> }>({ names: load(REPO_STORAGE_KEY) });

if (typeof window !== 'undefined') {
  // Cross-tab sync — another tab edited the list, mirror it here.
  window.addEventListener('storage', (e) => {
    if (e.key === STORAGE_KEY) {
      state.ids = load();
    } else if (e.key === REPO_STORAGE_KEY) {
      repoState.names = load(REPO_STORAGE_KEY);
    }
  });
}

/** Returns the reactive Set of hidden session_ids. Re-reads trigger re-renders. */
export function hiddenSessionIds(): Set<string> {
  return state.ids;
}

/** Toggle a single session_id's hidden state. */
export function toggleHidden(sessionId: string): void {
  if (!sessionId) return;
  const next = new Set(state.ids);
  if (next.has(sessionId)) {
    next.delete(sessionId);
  } else {
    next.add(sessionId);
  }
  state.ids = next;
  persist(next);
}

/** Hide every session_id in `ids` at once. Used by "Hide all from this repo". */
export function hideMany(ids: Iterable<string>): void {
  const next = new Set(state.ids);
  for (const id of ids) {
    if (id) next.add(id);
  }
  state.ids = next;
  persist(next);
}

/** Show every session_id in `ids` at once. Used by "Show all hidden". */
export function showMany(ids: Iterable<string>): void {
  const next = new Set(state.ids);
  for (const id of ids) {
    next.delete(id);
  }
  state.ids = next;
  persist(next);
}

/** Drop all hidden ids. */
export function clearHidden(): void {
  state.ids = new Set();
  persist(state.ids);
}

// ---------------------------------------------------------------------------
// Repo-level hiding — mirrors the session-level helpers above but keyed by
// repo name and persisted under its own localStorage key. Used by the
// productivity ProjectFilters chip row to hide a whole project at once.
// ---------------------------------------------------------------------------

/** Returns the reactive Set of hidden repo names. Re-reads trigger re-renders. */
export function hiddenRepoNames(): Set<string> {
  return repoState.names;
}

/** Toggle a single repo name's hidden state. */
export function toggleHiddenRepo(repo: string): void {
  if (!repo) return;
  const next = new Set(repoState.names);
  if (next.has(repo)) {
    next.delete(repo);
  } else {
    next.add(repo);
  }
  repoState.names = next;
  persist(next, REPO_STORAGE_KEY);
}

/** Drop all hidden repo names. */
export function clearHiddenRepos(): void {
  repoState.names = new Set();
  persist(repoState.names, REPO_STORAGE_KEY);
}

// ---------------------------------------------------------------------------
// Report-filtering helpers — derive a `ProductivityReport` with hidden
// sessions removed AND derived totals (total_active_minutes, etc.)
// recomputed so headline numbers match what the user actually sees.
// ---------------------------------------------------------------------------

/** Compute the union (in ms) of [start, end) intervals — same algorithm as
 *  ProofOfWork's local unionMs. Kept local so the helper has no UI deps. */
function unionMs(intervals: Array<[number, number]>): number {
  if (intervals.length === 0) return 0;
  const sorted = [...intervals].sort((a, b) => a[0] - b[0]);
  let total = 0;
  let curStart = sorted[0][0];
  let curEnd = sorted[0][1];
  for (let i = 1; i < sorted.length; i++) {
    const [a, b] = sorted[i];
    if (a > curEnd) {
      total += curEnd - curStart;
      curStart = a;
      curEnd = b;
    } else if (b > curEnd) {
      curEnd = b;
    }
  }
  total += curEnd - curStart;
  return total;
}

function parsePair(a: string, b: string): [number, number] | null {
  const t1 = Date.parse(a);
  const t2 = Date.parse(b);
  if (Number.isNaN(t1) || Number.isNaN(t2) || t2 <= t1) return null;
  return [t1, t2];
}

/** Return the report with hidden sessions removed and headline totals
 *  recomputed so they match what the user sees. The original report is
 *  not mutated. Hidden = session_id present in the current hidden set. */
export function applyHiddenFilter(rep: ProductivityReport): ProductivityReport {
  const hidden = state.ids;
  if (hidden.size === 0) return rep;
  const visible: ProductivitySessionStat[] = (rep.sessions ?? []).filter(
    (s) => !hidden.has(s.session_id)
  );
  // No change → return the same reference so consumers' equality checks
  // and memos don't churn.
  if (visible.length === (rep.sessions ?? []).length) return rep;

  // Recompute global active-time union from the visible sessions' intervals.
  const intervals: Array<[number, number]> = [];
  for (const s of visible) {
    for (const iv of s.active_intervals ?? ([] as ProductivityActiveInterval[])) {
      const pair = parsePair(iv.start, iv.end);
      if (pair) intervals.push(pair);
    }
  }
  const totalActiveMinutes = Math.round(unionMs(intervals) / 60_000);

  // Recompute per-CLI active-time union — the SummaryBar's "Claude 11h 6m
  // · Codex 0h 0m" subtotals come from this. Without this recompute,
  // hiding sessions visually drops them from totals but the per-CLI
  // chip still shows the unfiltered minutes, which is confusing.
  const cliIntervals = new Map<string, Array<[number, number]>>();
  for (const s of visible) {
    const cliKey = (s.cli || 'unknown').toLowerCase();
    let bucket = cliIntervals.get(cliKey);
    if (!bucket) {
      bucket = [];
      cliIntervals.set(cliKey, bucket);
    }
    for (const iv of s.active_intervals ?? ([] as ProductivityActiveInterval[])) {
      const pair = parsePair(iv.start, iv.end);
      if (pair) bucket.push(pair);
    }
  }
  const minutesByCli: Record<string, number> = {};
  for (const [cli, ivs] of cliIntervals) {
    minutesByCli[cli] = Math.round(unionMs(ivs) / 60_000);
  }

  return {
    ...rep,
    sessions: visible,
    total_active_minutes: totalActiveMinutes,
    minutes_by_cli: minutesByCli
  };
}
