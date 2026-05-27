/**
 * api.ts — Typed fetch wrappers for every klyne HTTP route.
 *
 * Routes mirrored from internal/api/contracts.go (W0-frozen).
 * Every wrapper throws an ApiError on non-2xx responses.
 */

import type {
  AdvisorDetailResponse,
  BreakAdviceResponse,
  CockpitThreadsResponse,
  CostSummaryQuery,
  CostSummaryResponse,
  HealthzResponse,
  InsightsQuery,
  MemoryResponse,
  MessageListQuery,
  MessageListResponse,
  ProjectInsightsResponse,
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
  WorklogProjectResponse,
  WorklogResponse,
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

// Read the response body once as text, then try to parse as JSON. The two-step
// `try res.json() / catch res.text()` pattern is broken: res.json() consumes
// the body stream even on parse failure, so the catch then throws "body stream
// already read" — masking the actual server error.
async function readBody(res: Response): Promise<unknown> {
  const text = await res.text();
  if (!text) return text;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    throw new ApiError(res.status, await readBody(res));
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
    const body = await readBody(res);
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
// /sessions/{id}/advisor-detail
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// /memory/items
// ---------------------------------------------------------------------------

/** GET /memory/items — every memory grouped by global vs project. */
export async function fetchMemory(): Promise<MemoryResponse> {
  return get<MemoryResponse>('/memory/items');
}

/** DELETE /memory/items/{id} — remove a single memory row. */
export async function deleteMemory(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/memory/items/${encodeURIComponent(id)}`, {
    method: 'DELETE'
  });
  if (!res.ok && res.status !== 204) {
    throw new ApiError(res.status, await readBody(res));
  }
}

// ---------------------------------------------------------------------------
// /worklog/items
// ---------------------------------------------------------------------------

/**
 * GET /worklog/items — per-project reflection rollup.
 *
 * Returns one entry per project that has either a reflection or at least one
 * visible stop_summaries row. Sorted server-side: stale-with-reflection first,
 * then cold-start (no reflection yet), then fresh.
 */
export async function fetchWorklog(): Promise<WorklogResponse> {
  return get<WorklogResponse>('/worklog/items');
}

/**
 * GET /worklog/items/project?path=<abs> — per-project drill-in.
 *
 * Returns the project's rollup + its full daily-reflection list
 * newest-first. The path arg is sent verbatim as the query value;
 * the URLSearchParams machinery URL-encodes it.
 */
export async function fetchWorklogProject(path: string): Promise<WorklogProjectResponse> {
  return get<WorklogProjectResponse>('/worklog/items/project', { path });
}

// ---------------------------------------------------------------------------
// /api/projects — DELETE for project-wide wipe
// ---------------------------------------------------------------------------

/** Per-table row counts surfaced by the project-wide delete endpoint. */
export interface ProjectDeleteCounts {
  stop_summaries: number;
  worklog_reflections: number;
  decisions: number;
  runbook_dismissals: number;
  work_spans: number;
  git_session_snapshots: number;
  total: number;
}

/** Response from DELETE /api/projects. `deleted=false` for dry-run. */
export interface ProjectDeleteResponse {
  deleted: boolean;
  counts: ProjectDeleteCounts;
}

/**
 * DELETE /api/projects — preview (dry_run=true) or execute the wipe of every
 * project-scoped row across the worklog/decision/runbook/work-span/git-snapshot
 * tables. Sessions and messages are intentionally preserved.
 */
export async function deleteProjectData(
  projectPath: string,
  opts: { dryRun?: boolean } = {}
): Promise<ProjectDeleteResponse> {
  const url = buildUrl('/api/projects', {
    path: projectPath,
    dry_run: opts.dryRun ? 'true' : undefined,
  });
  const res = await fetch(url, { method: 'DELETE' });
  return handleResponse<ProjectDeleteResponse>(res);
}

/**
 * Response shape from POST /worklog/reflect/run.
 *
 * status="ok" — subprocess exited 0; output holds the AI-written
 *   reflection (the same blob the user would have seen in their terminal).
 * status="error" — subprocess returned non-zero; error holds the exec
 *   error string and output holds whatever the process printed to stdout/
 *   stderr before failing.
 * status="timeout" — the 5-minute budget elapsed; output is whatever
 *   the subprocess produced before being killed.
 */
export interface ReflectRunResponse {
  project_path: string;
  status: 'ok' | 'error' | 'timeout';
  output: string;
  duration_ms: number;
  error?: string;
}

/**
 * POST /worklog/reflect/run — kick off `/klyne:reflect` via the local
 * `claude` CLI for the given project path. The path MUST already exist
 * in the worklog rollup; the server rejects unknown paths with 403.
 *
 * Pass `signal` to support cancellation from the UI. When aborted, the
 * fetch is cancelled which terminates the HTTP request which trips the
 * server-side request context. exec.CommandContext on the server then
 * SIGKILLs the spawned `claude` process — no orphans.
 */
export async function runReflect(
  projectPath: string,
  signal?: AbortSignal,
): Promise<ReflectRunResponse> {
  const res = await fetch(`${API_BASE}/worklog/reflect/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ project_path: projectPath }),
    signal,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new ApiError(res.status, text, `runReflect failed (${res.status})`);
  }
  return (await res.json()) as ReflectRunResponse;
}

/**
 * POST /productivity/compile — spawn `/klyne:productivity-sync` (LLM #2)
 * for one (project, day). Reads typed worklog_reflections rows and
 * writes an LLM-compiled WhatWasDoneCard with cohesive Tier 1 prose +
 * llm_compiled=true.
 */
export async function compileProductivity(
  projectPath: string,
  day: string,
  signal?: AbortSignal,
): Promise<{ project_path: string; day: string; status: string; output: string; duration_ms: number; error?: string }> {
  const res = await fetch(`${API_BASE}/api/productivity/compile`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ project_path: projectPath, day }),
    signal,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new ApiError(res.status, text, `compileProductivity failed (${res.status})`);
  }
  return await res.json();
}

// ---------------------------------------------------------------------------
// /insights/projects
// ---------------------------------------------------------------------------

/** GET /insights/projects — per-project rollup powering the Insights view. */
export async function fetchProjectInsights(opts: InsightsQuery = {}): Promise<ProjectInsightsResponse> {
  return get<ProjectInsightsResponse>('/insights/projects', {
    since: opts.since,
    until: opts.until,
    top: opts.top
  });
}

// ---------------------------------------------------------------------------
// /productivity
// ---------------------------------------------------------------------------

/**
 * GET /productivity — the deterministic-first AI productivity dashboard
 * (spec docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md).
 *
 * PROTOTYPE: the response is the Go `productivity.Report` shape. It is
 * intentionally typed loosely here — the real wire contract + frozen
 * types are deferred to the separate UI/UX brainstorm (spec scope).
 * since/until are epoch-ms; both default server-side (today 00:00 -> now).
 */
export async function fetchProductivity(
  since?: number,
  until?: number,
  refresh?: boolean
): Promise<ProductivityReport> {
  return get<ProductivityReport>('/api/productivity', {
    since,
    until,
    // `refresh=1` bypasses both the merged-PR cache TTL and the
    // FETCH_HEAD staleness gate on the backend — the user's explicit
    // "I want fresh data NOW" path.
    refresh: refresh ? 1 : undefined
  });
}

/** GET /api/productivity/dates — local days that have productivity data. */
export async function fetchProductivityDates(): Promise<ProductivityDatesResponse> {
  return get<ProductivityDatesResponse>('/api/productivity/dates');
}

/**
 * GET /api/klyne-usage?day=YYYY-MM-DD — per-day breakdown of tokens
 * spent by klyne's own LLM subprocesses (productivity-sync, reflect)
 * vs the user's full-day Claude usage. Tokens only; no USD by design.
 */
export async function fetchKlyneUsage(day?: string): Promise<KlyneUsageResponse> {
  return get<KlyneUsageResponse>('/api/klyne-usage', { day });
}

export interface KlyneUsageOpBreakdown {
  operation: string;
  runs: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  duration_ms: number;
}

export interface KlyneUsageKlyne {
  day: string;
  runs: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  duration_ms: number;
  by_operation: KlyneUsageOpBreakdown[];
}

export interface KlyneUsageUserTotal {
  day: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  message_count: number;
}

export interface KlyneUsageResponse {
  day: string;
  klyne: KlyneUsageKlyne;
  user_total: KlyneUsageUserTotal;
  /** Rounded to one decimal place; 0 when user_total.total_tokens == 0. */
  share_pct: number;
}

export interface ProductivityDatesResponse {
  days: string[];
  min_day?: string;
  max_day?: string;
}

/** Loose mirror of Go productivity.Report — prototype only. */
export interface ProductivityReport {
  day: string;
  services: ProductivityService[];
  reflection_status: string;
  nudge: string;
  /**
   * Headline AI time: the GLOBAL union of every session's active
   * wall-clock intervals across ALL repos in the window — true elapsed
   * wall-clock, structurally <= 24h/day. NOT the sum of the per-Service
   * unions (that double-counts parallel cross-repo agents).
   */
  total_active_minutes: number;
  /**
   * Per-CLI GLOBAL union (cli -> that CLI's all-repo wall-clock union),
   * NOT the sum of per-Service minutes_by_cli. When both CLIs ran at
   * once claude + codex may slightly exceed total_active_minutes —
   * expected.
   */
  minutes_by_cli: Record<string, number>;
  /**
   * Deterministic per-session proof-of-work breakdown: every
   * contributing session in the window, sorted by started_at — the
   * evidence behind total_active_minutes.
   */
  sessions: ProductivitySessionStat[];
  /**
   * Optional overall worklog reflection narrative (body_md). Empty when
   * there is no single sensible project-agnostic reflection; per-Service
   * reflection_markdown carries the per-repo body.
   */
  reflection_markdown?: string;
  /**
   * Iterative-reflection groups for the first service that has any —
   * mirrors that service's reflection_groups. Empty when no reflections
   * for the day. See docs/features/iterative-reflection.md.
   */
  reflection_groups?: ReflectionGroup[];
  /**
   * Number of stop_summary worklog entries written since the last
   * /klyne:reflect run for any project in the window — the
   * "needs-sync" signal that drives the highlighted reflect-now
   * button. Optional / may be undefined while the backend rolls out
   * the field (spec docs/plan/2026-05-26-wwd-typed-cards.md §2 Agent U).
   */
  pending_entries?: number;
  /**
   * Count of services in the report that have at least one typed
   * reflection but whose `what_was_done` card is NOT llm_compiled —
   * a /klyne:productivity-sync run is owed. Drives the "Generate
   * productivity" button visibility on the dashboard.
   */
  pending_compile?: number;
}
/** One row from worklog_reflections — one /klyne:reflect run. */
export interface ReflectionGroup {
  id: string;
  /** Epoch ms when this reflection was written. */
  ts: number;
  body_md: string;
  evidence_entry_ids: string[];
  stop_summary_cursor_ts?: number;
}

// ---------------------------------------------------------------------------
// What-was-done typed cards (spec: docs/plan/2026-05-26-wwd-typed-cards.md §1.2)
// ---------------------------------------------------------------------------

/**
 * One typed Tier-2 detail synthesized by /klyne:reflect from a single
 * stop_summary row. Mirrors §1.1 stored shape. Cards group these by
 * service. Evidence tokens are drawn LITERALLY from the source
 * stop_summary — the citation invariant.
 */
export type WWDKind =
  | 'SHIPPED'
  | 'MAJOR'
  | 'FIXED'
  | 'DECISION'
  | 'INVESTIGATED'
  | 'IN_PROGRESS';

export interface WWDDetail {
  kind: WWDKind;
  /** HH:MM local — when the originating stop_summary landed. */
  when: string;
  /** ≤200 chars, verb-led. */
  text: string;
  /** At least one literal token from the source stop_summary. */
  evidence: string[];
  /** Originating stop_summary session id — the worklog drill-in target. */
  session_id: string;
}

/**
 * Deterministic per-service headline derived in Go from the merged
 * Tier-2 details. No LLM at render time — recomputed each /klyne:reflect.
 */
export interface WWDTier1 {
  /** Templated one-liner: "{N} shipped · {N} fixed · … · latest: {text…}". */
  tldr: string;
  /** Counts by lowercased kind. Missing kinds may be omitted by the backend. */
  pill_counts: Partial<Record<Lowercase<WWDKind> | 'decisions', number>>;
  /** Up to 3 commit-sha-looking tokens from any detail's evidence, dedup, newest-first. */
  top_evidence: string[];
  /** Distinct `session_id`s across the merged details. */
  turn_count: number;
  /** Count of commit-sha-looking tokens across all evidence. */
  commit_count: number;
}

/**
 * Per-service "What was done" card carried on
 * GET /api/productivity → services[].what_was_done. Null/absent on
 * services whose reflections are all legacy prose (pre-body_json) —
 * the UI falls back to the existing bullet rendering in that case.
 */
export interface WhatWasDoneCard {
  service: string;
  tier1: WWDTier1;
  tier2: {
    /**
     * Merged details across every body_json row for (project, day).
     * Ordered SHIPPED → MAJOR → FIXED → DECISION → INVESTIGATED →
     * IN_PROGRESS; within a kind, newest-first.
     */
    details: WWDDetail[];
  };
  /**
   * True when the card's `tier1.tldr` and (optionally refined) detail
   * prose were authored by the second-pass Sonnet
   * `/klyne:productivity-sync` compiler. False when the card was
   * lazily composed from typed reflection rows by the deterministic
   * Go fallback (cold start / legacy days).
   */
  llm_compiled: boolean;
  /**
   * V2 narrative payload (2026-05-27 redesign). When present the
   * dashboard prefers this over tier1/tier2 — stat tiles, summary
   * paragraph, sectioned per-ticket cards with prose bodies + typed
   * refs. Absent on legacy llm_compiled=true rows or deterministic
   * fallback cards.
   */
  narrative?: WWDNarrative | null;
}

/**
 * V2 narrative payload — what the redesigned dashboard renders.
 * One service summary paragraph, per-kind stat counts, and N
 * narrative cards each grouped by (ticket, kind).
 */
export interface WWDNarrative {
  /** 1-2 sentence service-level theme paragraph. May be absent. */
  summary?: string;
  /** Per-kind counts shown as stat tiles. */
  stats: WWDStats;
  /** Ordered narrative cards. */
  cards: WWDNarrativeCard[];
  /** Optional "open question for tomorrow" line. */
  followup?: string;
}

export interface WWDStats {
  shipped: number;
  fixed: number;
  decisions: number;
  investigated: number;
  in_progress?: number;
}

export interface WWDNarrativeCard {
  kind: WWDKind;
  /** CLI-NNNN — absent for ticket-less work. */
  ticket_id?: string;
  /** Outcome-led headline, ≤160 chars. */
  title: string;
  /** Markdown prose narrative (2-4 sentences), ≤1200 chars. */
  body: string;
  /** Typed reference tokens drawn LITERALLY from the source rows. */
  refs?: WWDRef[];
}

export interface WWDRef {
  type: 'file' | 'branch' | 'pr' | 'commit' | 'ticket' | 'test' | 'session';
  text: string;
}
export interface ProductivityService {
  repo: string;
  project_path: string;
  branches: ProductivityBranch[];
  risks: ProductivityRisk[];
  manual_only: boolean;
  /**
   * Per-CLI AI time for this repo (cli -> merged active minutes).
   * Per-CLI values are union totals, so claude + codex may sum to
   * slightly more than the all-CLI attributed total when both ran at
   * once — expected.
   */
  minutes_by_cli: Record<string, number>;
  /**
   * Worklog reflection body (markdown) for this repo on the report's
   * day — the worklog's own account of what was done. Empty when no
   * reflection exists for the project+day. Legacy concatenated form;
   * prefer `reflection_groups` for the per-row chronological view.
   */
  reflection_markdown: string;
  /**
   * Every worklog_reflections row for (project, day) ordered ts ASC —
   * the iterative-reflection workflow's T1/T2/T3 history
   * (docs/features/iterative-reflection.md). Empty when no reflections
   * for the day.
   */
  reflection_groups?: ReflectionGroup[];
  /**
   * Typed "What was done" card for this service — Tier-1 deterministic
   * headline + Tier-2 merged details. Null/absent on services whose
   * reflections are all legacy prose (no `body_json`); the UI falls
   * back to the bullet-parsing path in that case. Spec:
   * docs/plan/2026-05-26-wwd-typed-cards.md §1.2.
   */
  what_was_done?: WhatWasDoneCard | null;
  /**
   * GitHub pull requests the user merged within the report window — a
   * Layer-2 `gh`-sourced enrichment, NOT a deterministic git fact.
   * Empty when gh/auth/network is unavailable.
   */
  merged_prs: MergedPR[];
  /**
   * When the merged-PR data was fetched (cache timestamp, RFC3339).
   * The zero value ("0001-01-01T00:00:00Z") means no PR data.
   */
  merged_prs_as_of: string;
  /**
   * When this repo's local mirror of origin was last refreshed via
   * `git fetch` (FETCH_HEAD mtime, RFC3339). Zero value means no
   * fetch has ever run in this clone — ahead/behind data may be
   * unreliable until then.
   */
  git_fetched_at: string;
}
/**
 * One GitHub pull request the user authored and merged within the
 * window. Mirrors Go productivity.MergedPR. Populated by the API-layer
 * `gh pr list` enrichment, not by the deterministic core.
 */
export interface MergedPR {
  number: number;
  title: string;
  /** PR head branch name. */
  head_ref: string;
  /** When the PR merged (RFC3339). */
  merged_at: string;
  /** When the PR was opened (RFC3339). */
  opened_at: string;
  /**
   * Minutes from opened_at → merged_at (PR open → merge cycle). 0
   * when openedAt is unknown.
   */
  time_to_ship_minutes: number;
}
/**
 * One gap-capped active wall-clock sub-interval of a session. The union
 * of every session's active_intervals produces total_active_minutes;
 * the timeline draws these as solid segments. Mirrors Go
 * productivity.ActiveInterval.
 */
export interface ProductivityActiveInterval {
  /** Interval start (RFC3339). */
  start: string;
  /** Interval end (RFC3339). */
  end: string;
}
/**
 * One session's proof-of-work contribution to the headline AI time.
 * Mirrors Go productivity.SessionStat.
 */
export interface ProductivitySessionStat {
  session_id: string;
  cli: string;
  /** Repo/Service the session is attributed to (derived from project_path). */
  repo: string;
  /** First in-window message timestamp (RFC3339). */
  started_at: string;
  /** Last in-window message timestamp (RFC3339). */
  ended_at: string;
  /** This session's own gap-capped active total, in minutes. */
  active_minutes: number;
  /**
   * Gap-capped active sub-intervals — the same intervals whose global
   * union produces total_active_minutes. Lengths sum to active_minutes.
   * Always present (never null); [] when the session has no span.
   */
  active_intervals: ProductivityActiveInterval[];
  message_count: number;
}
export interface ProductivityBranch {
  name: string;
  ticket_id: string;
  ship: string;
  ahead: number;
  behind: number;
  commits: ProductivityCommit[];
  attributed_minutes: number;
  narrative: string;
  /** Earliest commit timestamp on the branch (RFC3339). */
  first_commit_at: string;
  /** Latest commit timestamp on the branch (RFC3339). */
  last_commit_at: string;
  /**
   * Minutes between the earliest and latest commit on the branch — the
   * honest work-span / time-to-ship proxy. 0 when the branch has fewer
   * than 2 commits.
   */
  ship_span_minutes: number;
}
export interface ProductivityCommit {
  sha: string;
  subject: string;
  author: string;
  author_email: string;
  committed_at: string;
  files: number;
  insertions: number;
  deletions: number;
  is_user: boolean;
}
/** A minimal commit reference attached to an "unpushed" risk. */
export interface ProductivityRiskCommit {
  sha: string;
  subject: string;
}
export interface ProductivityRisk {
  kind: string;
  detail: string;
  age_minutes: number;
  /** Branch the risk is on — disambiguates per-worktree rows. */
  branch: string;
  /** Working directory the risk was observed in. */
  worktree_path: string;
  /**
   * For an "unpushed" risk: the commits ahead of origin (SHA + subject)
   * — the concrete evidence behind the count. Empty for other kinds.
   */
  commits: ProductivityRiskCommit[];
  /**
   * For a "done-uncommitted" risk: the uncommitted/untracked file
   * paths. Empty for other kinds.
   */
  files: string[];
}
