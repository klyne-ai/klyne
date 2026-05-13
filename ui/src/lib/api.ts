/**
 * api.ts — Typed fetch wrappers for every klyne HTTP route.
 *
 * Routes mirrored from internal/api/contracts.go (W0-frozen).
 * Every wrapper throws an ApiError on non-2xx responses.
 */

import type {
  AdvisorDetailResponse,
  AdvisoryKind,
  AdvisoryListResponse,
  BreakAdviceResponse,
  CockpitThreadsResponse,
  CostSummaryQuery,
  CostSummaryResponse,
  HealthzResponse,
  MessageListQuery,
  MessageListResponse,
  RestoreResponse,
  SearchResponse,
  SessionListQuery,
  SessionListResponse,
  SessionResponse,
  SessionUsageResponse,
  SummaryResponse,
  TokenTimelineQuery,
  TokenTimelineResponse,
  UsageResponse,
  UsageStatsQuery,
  UsageStatsResponse,
} from './types.js';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

/** Base URL for the klyne daemon. Override via the API_BASE env var at build time. */
const API_BASE =
  typeof import.meta !== 'undefined' &&
  typeof (import.meta as { env?: { VITE_API_BASE?: string } }).env !== 'undefined'
    ? ((import.meta as { env?: { VITE_API_BASE?: string } }).env?.VITE_API_BASE ?? '')
    : '';

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

/** Typed error thrown by all API wrappers on non-2xx responses. */
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, body: unknown, message?: string) {
    super(message ?? `API error ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/**
 * Perform a typed GET request. Throws ApiError on non-2xx.
 */
async function get<T>(path: string, params?: Record<string, string | number | undefined>): Promise<T> {
  const url = buildUrl(path, params);
  const res = await fetch(url);
  return handleResponse<T>(res);
}

/**
 * Perform a typed PUT request with a JSON body. Throws ApiError on non-2xx.
 */
async function put<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  return handleResponse<T>(res);
}

/**
 * Perform a typed POST request. Throws ApiError on non-2xx.
 */
async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: 'POST',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined
  });
  return handleResponse<T>(res);
}

function buildUrl(path: string, params?: Record<string, string | number | undefined>): string {
  const base = `${API_BASE}${path}`;
  if (!params) return base;
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) {
      qs.set(key, String(value));
    }
  }
  const queryString = qs.toString();
  return queryString ? `${base}?${queryString}` : base;
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let body: unknown;
    try {
      body = await res.json();
    } catch {
      body = await res.text();
    }
    throw new ApiError(res.status, body);
  }
  // 204 No Content
  if (res.status === 204) {
    return undefined as unknown as T;
  }
  return res.json() as Promise<T>;
}

// ---------------------------------------------------------------------------
// /sessions
// ---------------------------------------------------------------------------

/** GET /sessions — list sessions with optional filters. */
export async function fetchSessions(opts?: SessionListQuery): Promise<SessionListResponse> {
  return get<SessionListResponse>('/sessions', {
    cli: opts?.cli,
    project: opts?.project,
    limit: opts?.limit,
    before: opts?.before
  });
}

/** GET /sessions/{id} — fetch a single session by ID. */
export async function fetchSession(id: string): Promise<SessionResponse> {
  return get<SessionResponse>(`/sessions/${encodeURIComponent(id)}`);
}

/** DELETE /sessions/{id} — permanently remove the session, its messages, and FTS rows. */
export async function deleteSession(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/sessions/${encodeURIComponent(id)}`, {
    method: 'DELETE'
  });
  if (!res.ok && res.status !== 204) {
    let body: unknown;
    try { body = await res.json(); } catch { body = await res.text(); }
    throw new Error(`DELETE /sessions/${id} failed (${res.status}): ${typeof body === 'string' ? body : JSON.stringify(body)}`);
  }
}

/** GET /sessions/{id}/messages — paginated message list. */
export async function fetchMessages(
  sessionId: string,
  opts?: MessageListQuery
): Promise<MessageListResponse> {
  return get<MessageListResponse>(`/sessions/${encodeURIComponent(sessionId)}/messages`, {
    limit: opts?.limit,
    before: opts?.before,
    order: opts?.order,
    branch: opts?.branch,
    cwd: opts?.cwd
  });
}

/** GET /sessions/{id}/restore — restore context payload (spec Flow C). */
export async function fetchRestore(sessionId: string): Promise<RestoreResponse> {
  return get<RestoreResponse>(`/sessions/${encodeURIComponent(sessionId)}/restore`);
}

/** GET /sessions/{id}/summary — latest rolling summary. */
export async function fetchSummary(sessionId: string): Promise<SummaryResponse> {
  return get<SummaryResponse>(`/sessions/${encodeURIComponent(sessionId)}/summary`);
}

/**
 * GET /sessions/{id}/usage — context-fill + per-turn cost projection used
 * by the per-session token-savings indicator. Projections are calibrated
 * against the user's 5h rate-limit window when vendor /usage data is
 * available; otherwise the *_pct_5h fields come back as -1 and the UI
 * must render them as "—".
 */
export async function fetchSessionUsage(id: string): Promise<SessionUsageResponse> {
  return get<SessionUsageResponse>(`/sessions/${encodeURIComponent(id)}/usage`);
}

/**
 * GET /sessions/{id}/break-advice — AI-generated recommendation on
 * whether to start a fresh session, /compact, or keep going. Server
 * caches per-session for 10 minutes, so repeated calls are cheap.
 */
export async function fetchBreakAdvice(id: string): Promise<BreakAdviceResponse> {
  return get<BreakAdviceResponse>(`/sessions/${encodeURIComponent(id)}/break-advice`);
}

/**
 * GET /sessions/{id}/token-timeline — per-assistant-turn token usage
 * series for the cockpit's line chart. Server reuses the same
 * computation that powers `klyne tokens` and the get_token_timeline
 * MCP tool, so all three surfaces stay in sync.
 *
 * Defaults to the entire-session view. Pass `window` ("30m", "5h",
 * "2h30m") or `hours` to clip to a recent window — useful when the
 * session is large and the user only wants to see today's activity.
 */
export async function fetchTokenTimeline(
  sessionId: string,
  opts?: TokenTimelineQuery
): Promise<TokenTimelineResponse> {
  return get<TokenTimelineResponse>(
    `/sessions/${encodeURIComponent(sessionId)}/token-timeline`,
    {
      window: opts?.window,
      hours: opts?.hours
    }
  );
}

// ---------------------------------------------------------------------------
// /search
// ---------------------------------------------------------------------------

/** Sort order for /search results. 'recent' (default) ranks by ts DESC,
 *  'relevance' ranks by FTS5 BM25. */
export type SearchSort = 'recent' | 'relevance';

/** GET /search?q=&limit=&sort= — full-text search over messages. */
export async function search(q: string, limit?: number, sort: SearchSort = 'recent'): Promise<SearchResponse> {
  return get<SearchResponse>('/search', { q, limit, sort });
}

// ---------------------------------------------------------------------------
// /advisories
// ---------------------------------------------------------------------------

/** Optional filter for fetchAdvisories — by trigger kind. */
export interface AdvisoriesQuery {
  limit?: number;
  kind?: AdvisoryKind;
}

/** GET /advisories — every klyne advisor message ingested into the index,
 * newest first. Pass `kind` to filter by trigger type. */
export async function fetchAdvisories(opts: AdvisoriesQuery = {}): Promise<AdvisoryListResponse> {
  return get<AdvisoryListResponse>('/advisories', {
    limit: opts.limit,
    kind: opts.kind
  });
}

/** GET /sessions/{id}/advisor-detail — per-session advisories + proof
 * data the cockpit modal renders alongside each advisory. */
export async function fetchAdvisorDetail(sessionId: string): Promise<AdvisorDetailResponse> {
  return get<AdvisorDetailResponse>(`/sessions/${encodeURIComponent(sessionId)}/advisor-detail`);
}

// ---------------------------------------------------------------------------
// /cost/summary
// ---------------------------------------------------------------------------

/** GET /cost/summary — grouped cost breakdown. */
export async function fetchCostSummary(opts: CostSummaryQuery): Promise<CostSummaryResponse> {
  return get<CostSummaryResponse>('/cost/summary', {
    group: opts.group,
    since: opts.since,
    until: opts.until
  });
}

// ---------------------------------------------------------------------------
// /usage
// ---------------------------------------------------------------------------

/** GET /usage — rolling 5h, 7d, and 7d-Sonnet token aggregates per CLI. */
export async function fetchUsage(): Promise<UsageResponse> {
  return get<UsageResponse>('/usage');
}

// ---------------------------------------------------------------------------
// /usage/stats — tokscale-inspired cross-session aggregates
// ---------------------------------------------------------------------------

/** GET /usage/stats — Overview / Daily / Stats / Models data for the
 *  dashboard's Stats page. Default lookback is 30 days; heatmap covers
 *  the last 12 weeks. Pass cli='claude' | 'codex' to scope. */
export async function fetchUsageStats(opts: UsageStatsQuery = {}): Promise<UsageStatsResponse> {
  return get<UsageStatsResponse>('/usage/stats', {
    cli: opts.cli || undefined,
    days: opts.days,
    heatmap_weeks: opts.heatmap_weeks
  });
}

// ---------------------------------------------------------------------------
// /cockpit/threads
// ---------------------------------------------------------------------------

/** GET /cockpit/threads — per-(session, branch, cwd) buckets for the
 *  cockpit grid. `since` defaults to the last 7 days; pass 0 for unbounded. */
export async function fetchCockpitThreads(opts?: { since?: number; limit?: number }): Promise<CockpitThreadsResponse> {
  return get<CockpitThreadsResponse>('/cockpit/threads', {
    since: opts?.since,
    limit: opts?.limit
  });
}

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

/** GET /healthz — daemon liveness check. */
export async function fetchHealthz(): Promise<HealthzResponse> {
  return get<HealthzResponse>('/healthz');
}
