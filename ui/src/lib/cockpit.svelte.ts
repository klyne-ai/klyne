/**
 * cockpit.svelte.ts — Shared live-session count for the global top nav.
 *
 * Both the Work view and the TopNav need to know which sessions are
 * currently streaming. Rather than duplicate the threads fetch +
 * tick + filter logic, this store owns it and exposes a single
 * `liveCount` value that the nav reads.
 */

import { fetchCockpitThreads } from '$lib/api.js';
import type { CockpitThread } from '$lib/types.js';

const LIVE_THRESHOLD_MS = 60_000;
const RECENT_WINDOW_MS = 30 * 60 * 1000;
const SINCE_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

export const cockpitStore = $state<{
  threads: CockpitThread[];
  tick: number;
  loaded: boolean;
}>({
  threads: [],
  tick: Date.now(),
  loaded: false,
});

export async function refreshCockpit(): Promise<void> {
  try {
    const since = Date.now() - SINCE_WINDOW_MS;
    const resp = await fetchCockpitThreads({ since, limit: 100 });
    cockpitStore.threads = resp.threads;
    cockpitStore.loaded = true;
  } catch {
    // Next refresh will recover.
  }
}

/** Tile-eligible threads (those still within the 30-min recency window). */
export function recentThreads(): CockpitThread[] {
  return cockpitStore.threads.filter(
    (t) => cockpitStore.tick - t.last_msg_at < RECENT_WINDOW_MS
  );
}

/** Count of threads streaming right now (last message within 1 minute). */
export function liveCount(): number {
  return recentThreads().filter(
    (t) => cockpitStore.tick - t.last_msg_at < LIVE_THRESHOLD_MS
  ).length;
}
