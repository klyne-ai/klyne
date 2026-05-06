/**
 * api.ts — Typed fetch wrappers for every agentdeck HTTP route.
 *
 * Routes mirrored from internal/api/contracts.go (W0-frozen).
 * Every wrapper throws an ApiError on non-2xx responses.
 */

import type {
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
  SettingsResponse,
  SettingsUpdateRequest,
  SummaryResponse,
  WizardDetectResponse
} from './types.js';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

/** Base URL for the agentdeck daemon. Override via the API_BASE env var at build time. */
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

/** GET /sessions/{id}/messages — paginated message list. */
export async function fetchMessages(
  sessionId: string,
  opts?: MessageListQuery
): Promise<MessageListResponse> {
  return get<MessageListResponse>(`/sessions/${encodeURIComponent(sessionId)}/messages`, {
    limit: opts?.limit,
    before: opts?.before
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

// ---------------------------------------------------------------------------
// /search
// ---------------------------------------------------------------------------

/** GET /search?q=&limit= — full-text search over messages. */
export async function search(q: string, limit?: number): Promise<SearchResponse> {
  return get<SearchResponse>('/search', { q, limit });
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
// /settings
// ---------------------------------------------------------------------------

/** GET /settings — retrieve current AI model and provider settings. */
export async function fetchSettings(): Promise<SettingsResponse> {
  return get<SettingsResponse>('/settings');
}

/** PUT /settings — partial update; nil fields in the patch are left unchanged. */
export async function updateSettings(patch: SettingsUpdateRequest): Promise<SettingsResponse> {
  return put<SettingsResponse>('/settings', patch);
}

// ---------------------------------------------------------------------------
// /wizard/*  (W15 — stubs provided for completeness)
// ---------------------------------------------------------------------------

/** GET /wizard/detect — first-run credential detection. */
export async function fetchWizardDetect(): Promise<WizardDetectResponse> {
  return get<WizardDetectResponse>('/wizard/detect');
}

/** POST /wizard/complete — finish onboarding; returns 204. */
export async function postWizardComplete(): Promise<void> {
  return post<void>('/wizard/complete');
}

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

/** GET /healthz — daemon liveness check. */
export async function fetchHealthz(): Promise<HealthzResponse> {
  return get<HealthzResponse>('/healthz');
}
