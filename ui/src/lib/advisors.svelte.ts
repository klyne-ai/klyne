/**
 * advisors.svelte.ts — Shared advisory store for the sidebar badge count
 * and the Advisors page.
 *
 * Mirrors cockpit.svelte.ts in structure. refreshAdvisors() is called
 * from +layout.svelte on mount and on a 60-second interval.
 */

import { fetchAdvisories } from '$lib/api.js';
import type { AdvisoryRow } from '$lib/types.js';

class AdvisorsStore {
  advisories = $state<AdvisoryRow[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);

  get total(): number {
    return this.advisories.length;
  }
}

export const advisorsStore = new AdvisorsStore();

/**
 * Stable derived shape consumed by Shell and Advisors page.
 * Downstream tasks rely on these exact names — do not rename.
 */
export const advisorsDerived = {
  /** Total number of advisories loaded (last 24h, up to 200). */
  get total(): number {
    return advisorsStore.total;
  },
};

/**
 * Fetch advisories (last 24h, up to 200) and populate advisorsStore.
 * Silently swallows network errors — next call will recover.
 */
export async function refreshAdvisors(): Promise<void> {
  advisorsStore.loading = true;
  try {
    const r = await fetchAdvisories({ limit: 200 });
    advisorsStore.advisories = r.advisories;
    advisorsStore.error = null;
  } catch (e) {
    advisorsStore.error =
      e instanceof Error ? e.message : 'failed to load advisories';
  } finally {
    advisorsStore.loading = false;
  }
}
