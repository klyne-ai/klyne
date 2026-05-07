/**
 * projects.svelte.ts — Client-side project aggregation from /sessions.
 *
 * The real backend exposes individual sessions; this module derives
 * project-level aggregates with stats from those sessions.
 */

import type { Session } from '$lib/types.js';
import { fetchSessions } from '$lib/api.js';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ProjectAggregate {
  name: string;          // basename of project_path
  project_path: string;  // full path
  sessions: number;      // count
  msgs: number;          // sum msg_count
  tokensIn: number;      // sum tokens_in
  tokensOut: number;     // sum tokens_out
  cost: number;          // sum cost_usd
  lastMsAt: number;      // max last_msg_at (epoch-ms)
  lastMsAgo: number;     // Date.now() - lastMsAt
  model: string;         // most common model
  cli: 'claude' | 'codex';
  priced: boolean;       // cost > 0
  status: 'active' | 'idle' | 'compacted';
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function basename(path: string): string {
  // Strip trailing slash, then get last segment
  const p = path.replace(/\/$/, '');
  const idx = p.lastIndexOf('/');
  return idx >= 0 ? p.slice(idx + 1) : p;
}

function mostCommon(values: string[]): string {
  if (!values.length) return '';
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  let best = values[0];
  let bestCount = 0;
  for (const [v, c] of counts) {
    if (c > bestCount) { best = v; bestCount = c; }
  }
  return best;
}

function deriveStatus(sessions: Session[]): 'active' | 'idle' | 'compacted' {
  const now = Date.now();
  const ACTIVE_THRESHOLD_MS = 60_000;
  if (sessions.some((s) => now - s.last_msg_at < ACTIVE_THRESHOLD_MS)) return 'active';
  if (sessions.some((s) => s.status === 'compacted')) return 'compacted';
  return 'idle';
}

// ---------------------------------------------------------------------------
// Aggregation
// ---------------------------------------------------------------------------

function aggregateProjects(sessions: Session[]): ProjectAggregate[] {
  const byPath = new Map<string, Session[]>();
  for (const s of sessions) {
    const group = byPath.get(s.project_path) ?? [];
    group.push(s);
    byPath.set(s.project_path, group);
  }

  const now = Date.now();
  const result: ProjectAggregate[] = [];

  for (const [project_path, group] of byPath) {
    const msgs = group.reduce((a, s) => a + s.msg_count, 0);
    const tokensIn = group.reduce((a, s) => a + s.tokens_in, 0);
    const tokensOut = group.reduce((a, s) => a + s.tokens_out, 0);
    const cost = group.reduce((a, s) => a + s.cost_usd, 0);
    const lastMsAt = group.reduce((a, s) => Math.max(a, s.last_msg_at), 0);
    const cli = (mostCommon(group.map((s) => s.cli)) || 'claude') as 'claude' | 'codex';
    const model = mostCommon(group.map((s) => s.model));

    result.push({
      name: basename(project_path) || project_path,
      project_path,
      sessions: group.length,
      msgs,
      tokensIn,
      tokensOut,
      cost,
      lastMsAt,
      lastMsAgo: now - lastMsAt,
      model,
      cli,
      priced: cost > 0,
      status: deriveStatus(group),
    });
  }

  // Sort by lastMsAt descending (most recent first)
  result.sort((a, b) => b.lastMsAt - a.lastMsAt);
  return result;
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

export async function loadProjects(): Promise<ProjectAggregate[]> {
  // Fetch up to 500 sessions to get a broad view
  const resp = await fetchSessions({ limit: 500 });
  return aggregateProjects(resp.sessions);
}

// ---------------------------------------------------------------------------
// Reactive store (Svelte 5 runes)
// ---------------------------------------------------------------------------

export const projectsStore = $state<{
  items: ProjectAggregate[];
  loading: boolean;
  error: string | null;
}>({
  items: [],
  loading: false,
  error: null,
});

export async function refreshProjects(): Promise<void> {
  projectsStore.loading = true;
  projectsStore.error = null;
  try {
    projectsStore.items = await loadProjects();
  } catch (e) {
    projectsStore.error = e instanceof Error ? e.message : 'Failed to load projects';
  } finally {
    projectsStore.loading = false;
  }
}
