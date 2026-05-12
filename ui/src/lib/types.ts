/**
 * types.ts — TypeScript mirror of W0-frozen Go contract types.
 *
 * Sources mirrored (field-for-field, json tag → TS field name):
 *   internal/connectors/connector.go
 *   internal/api/contracts.go
 *   internal/api/sse_events.go
 *
 * DO NOT edit field names or types without a `contract-change` PR.
 * The check-contracts.ts script enforces this at CI time.
 */

// ---------------------------------------------------------------------------
// connectors package
// ---------------------------------------------------------------------------

/** CLI identifies which agent produced a message/session. */
export type CLI = 'claude' | 'codex';

/** Role enumerates canonical message authorship values. */
export type Role = 'user' | 'assistant' | 'tool' | 'system';

/** SessionStatus enumerates lifecycle values stored in sessions.status. */
export type SessionStatus = 'active' | 'idle' | 'compacted';

/** ToolCall describes a single tool invocation issued by an assistant message. */
export interface ToolCall {
  id: string;
  name: string;
  input: string;
}

/** ToolResult is the response produced by a tool execution. */
export interface ToolResult {
  id: string;
  output: string;
  is_error: boolean;
}

/**
 * Message is the canonical, connector-agnostic representation of a single
 * turn in a CLI session. Mirrors connectors.Message.
 */
export interface Message {
  id: string;
  session_id: string;
  cli: CLI;
  project_path: string;
  role: Role;
  content: string;
  /** May be absent (omitempty in Go) — treat as empty array when missing. */
  tool_calls?: ToolCall[];
  /** May be absent (omitempty in Go) — treat as empty array when missing. */
  tool_results?: ToolResult[];
  /**
   * Total input tokens — fresh + cached_read + cached_write.
   * Use this for "input tokens" display.
   */
  tokens_in: number;
  tokens_out: number;
  /**
   * Cached-read portion of tokens_in (cache hits, billed at ~10× discount).
   */
  cached_read_tokens: number;
  /**
   * Cached-write portion of tokens_in (cache creation, billed at ~25% premium
   * over fresh on Anthropic; OpenAI does not expose this, so always 0 for codex).
   */
  cached_write_tokens: number;
  cost_usd: number;
  model: string;
  /** Epoch-milliseconds. */
  ts: number;
  /** May be absent (omitempty in Go). */
  parent_uuid?: string;
  /** May be absent (omitempty in Go). */
  git_branch?: string;
  /** May be absent (omitempty in Go). */
  cwd?: string;
}

/**
 * Session is the canonical session row. Mirrors connectors.Session.
 */
export interface Session {
  id: string;
  cli: CLI;
  project_path: string;
  encoded_cwd: string;
  /** Epoch-milliseconds. */
  started_at: number;
  /** Epoch-milliseconds. */
  last_msg_at: number;
  msg_count: number;
  /** Total input tokens — fresh + cached_read + cached_write. */
  tokens_in: number;
  tokens_out: number;
  /** Cached-read aggregate over all messages in this session. */
  cached_read_tokens: number;
  /** Cached-write aggregate (Anthropic only; always 0 on codex). */
  cached_write_tokens: number;
  cost_usd: number;
  model: string;
  status: SessionStatus;
  raw_path: string;
}

/** PerTokenRates holds per-million-token pricing for one model. */
export interface PerTokenRates {
  prompt_per_mtok: number;
  completion_per_mtok: number;
  cache_read_per_mtok: number;
  cache_write_per_mtok: number;
}

/** PricingTable mirrors connectors.PricingTable. */
export interface PricingTable {
  models: Record<string, PerTokenRates>;
}

/** RawEvent mirrors connectors.RawEvent (informational; not used in UI). */
export interface RawEvent {
  path: string;
  /** Base64-encoded bytes in JSON transport. */
  line: string;
  ts: number;
}

// ---------------------------------------------------------------------------
// api package — HTTP DTOs (internal/api/contracts.go)
// ---------------------------------------------------------------------------

// --- /sessions ---

/** SessionListResponse is the envelope returned by GET /sessions. */
export interface SessionListResponse {
  sessions: Session[];
  next_before: number;
}

/** SessionResponse is GET /sessions/{id}. */
export interface SessionResponse {
  session: Session;
}

/** MessageListResponse is GET /sessions/{id}/messages. */
export interface MessageListResponse {
  messages: Message[];
  next_before: number;
}

/** RestoreResponse is GET /sessions/{id}/restore. */
export interface RestoreResponse {
  session_id: string;
  summary: string;
  tail: Message[];
  markdown: string;
  resume_cmd: string;
  project_path: string;
  /** Epoch-milliseconds. */
  generated_at: number;
}

/** SummaryResponse is GET /sessions/{id}/summary. */
export interface SummaryResponse {
  session_id: string;
  version: number;
  text: string;
  model: string;
  ts: number;
}

// --- /sessions/{id}/usage — token-savings indicator ---

/**
 * SessionUsageResponse is GET /sessions/{id}/usage. Backs the per-session
 * "context fill + cost-per-turn" indicator and the savings deltas for
 * compacting/restarting.
 *
 * All `*_pct_5h` fields are percentages of the user's 5-hour rate-limit
 * cap, NOT of the context window. They are -1 when the server cannot
 * calibrate (no recent /usage data, or zero observed utilization). The UI
 * MUST render -1 as "—" rather than a misleading number.
 */
export interface SessionUsageResponse {
  session_id: string;
  /** Most-recent assistant model used; empty for fresh sessions. */
  model: string;
  /** Model's max context size in tokens (0 when unknown). */
  context_window: number;
  /** Approx tokens that the next turn would re-send as input. */
  context_used: number;
  /** context_used / context_window * 100. May exceed 100 on overflow. */
  context_fill_pct: number;
  /** Projected % of 5h limit the next turn will consume; -1 when uncalibrated. */
  next_turn_pct_5h: number;
  /** Same projection AFTER /compact, using compact_ratio. -1 when uncalibrated. */
  compacted_next_turn_pct_5h: number;
  /** Same projection in a FRESH session (system-prompt floor). -1 when uncalibrated. */
  restarted_next_turn_pct_5h: number;
  /** next_turn_pct_5h - compacted_next_turn_pct_5h. -1 when uncalibrated. */
  compact_savings_pct_5h: number;
  /** next_turn_pct_5h - restarted_next_turn_pct_5h. -1 when uncalibrated. */
  restart_savings_pct_5h: number;
  /** Assumed post-compact size as fraction of current context (e.g. 0.15). */
  compact_ratio: number;
  /** True when percentages came from vendor /usage; false when fallback. */
  calibrated_from_oauth: boolean;
}

// --- /sessions/{id}/token-timeline — per-turn token usage line chart ---

/**
 * TokenTimelinePoint is one assistant turn's token-usage row. The
 * cockpit chart plots `total_input` (the prefix size at that turn)
 * as the primary curve and may surface `effective_input` and
 * `cached_read_tokens` as secondary signal when the user wants to
 * see the prompt-cache discount.
 */
export interface TokenTimelinePoint {
  /** Epoch-ms of the assistant message. */
  ts_ms: number;
  /** TokensIn - CachedReadTokens — uncached portion that bills against
   *  the 5h rate-limit at full rate. */
  effective_input: number;
  /** Raw TokensIn (fresh + cached_read + cached_write) — the prefix
   *  size at that turn. The primary line plotted by the chart. */
  total_input: number;
  /** Prefix served from prompt cache. */
  cached_read_tokens: number;
  /** New content written to cache. */
  cached_write_tokens: number;
  /** Completion token count. */
  output_tokens: number;
}

/**
 * TokenTimelineResponse is GET /sessions/{id}/token-timeline. It backs
 * the cockpit's per-session token-usage line chart, mirroring the data
 * shape the `klyne tokens` CLI and the `get_token_timeline` MCP tool
 * already produce.
 */
export interface TokenTimelineResponse {
  session_id: string;
  /** Most-recent assistant model used in this session. Empty for
   *  fresh sessions (in which case the chart should label the axis
   *  via the session's model field as a fallback). */
  model: string;
  /** Model's maximum context window in tokens. Zero when unknown — the
   *  chart should hide the "% of context" axis in that case. */
  context_window: number;
  /** Left edge of the displayed window (epoch-ms). In the entire-
   *  session default view this equals the first point's ts_ms. */
  window_start_ms: number;
  /** Right edge of the window (epoch-ms; typically the server clock
   *  at request time). */
  window_end_ms: number;
  /** Per-assistant-turn rows in chronological order. May be empty for
   *  brand-new sessions; the UI should render a "no turns yet" hint
   *  rather than an empty axis. */
  points: TokenTimelinePoint[];
  /** Oldest qualifying turn's total_input — where the session started. */
  first_input: number;
  /** Most recent qualifying turn's total_input — current prefix size. */
  latest_input: number;
  /** Largest single-turn total_input in the window. */
  peak_input: number;
  /** latest_input / context_window × 100, capped at 100. Zero when
   *  context_window is unknown or there are no points. */
  pct_of_context: number;
}

/** Query parameters for fetchTokenTimeline. */
export interface TokenTimelineQuery {
  /** Go duration string (e.g. "30m", "5h", "2h30m"). Empty means
   *  "entire session" — long-paused sessions surface their full history. */
  window?: string;
  /** Convenience integer hours; ignored when window is set. */
  hours?: number;
}

// --- /sessions/{id}/break-advice — AI-powered session-break recommender ---

/** BreakAdviceVerdict mirrors the Go enum string values. */
export type BreakAdviceVerdict =
  | 'start_fresh'
  | 'compact'
  | 'continue'
  | 'unavailable';

/**
 * BreakAdviceResponse is GET /sessions/{id}/break-advice. Cached in-memory
 * for 10 min per session on the server, so re-clicks are free.
 */
export interface BreakAdviceResponse {
  session_id: string;
  verdict: BreakAdviceVerdict;
  /** One-sentence human-readable reason. Always populated. */
  reason: string;
  /** Short topic label for the new session when verdict is "start_fresh". */
  suggested_topic?: string;
  /** AI provider that generated the advice ("anthropic", "gemini", …). */
  provider?: string;
  /** Model identifier used. Empty when verdict is "unavailable". */
  model?: string;
  /** Epoch-ms when this advice was generated. */
  cached_at: number;
}

// --- /search ---

/** SearchHit is one ranked FTS5 result. */
export interface SearchHit {
  message_id: string;
  session_id: string;
  cli: string;
  project_path: string;
  role: string;
  snippet: string;
  score: number;
  ts: number;
}

/** SearchResponse is GET /search?q=&limit=. */
export interface SearchResponse {
  query: string;
  hits: SearchHit[];
  took_ms: number;
}

// --- /advisories ---

/** AdvisoryKind identifies which klyne advisor trigger fired. */
export type AdvisoryKind =
  | 'stale'
  | 'acceleration'
  | 'hard_ceiling'
  | 'window_50'
  | 'window_75'
  | 'topic_shift'
  | 'unknown';

/** AdvisoryRow is one rendered advisory across all klyne-monitored sessions. */
export interface AdvisoryRow {
  message_id: string;
  session_id: string;
  cli: CLI | '';
  project_path: string;
  kind: AdvisoryKind;
  content: string;
  ts: number;
}

/** AdvisoryListResponse is GET /advisories. */
export interface AdvisoryListResponse {
  advisories: AdvisoryRow[];
}

/** FileRelevanceProof is one file's per-file relevance row. */
export interface FileRelevanceProof {
  path: string;
  basename: string;
  bytes: number;
  score: number;
  stale: boolean;
}

/** StaleProof bundles the relevance scorer outputs for the modal. */
export interface StaleProof {
  files: FileRelevanceProof[];
  stale_bytes: number;
  total_bytes: number;
  stale_share: number;
  threshold: number;
}

/** AccelerationProof is the per-turn cost trajectory. */
export interface AccelerationProof {
  recent_mean: number;
  prior_mean: number;
  ratio: number;
  latest_effective: number;
  sampled_turns: number;
  would_fire: boolean;
}

/** ContextWindowProof is the live fill state for the hard-ceiling trigger. */
export interface ContextWindowProof {
  fill_pct: number;
  latest_input: number;
  context_window: number;
  model: string;
  threshold: number;
  would_fire: boolean;
}

/** FiveHourProof is the cross-session rate-limit aggregate. */
export interface FiveHourProof {
  total_effective: number;
  cap: number;
  pct_used: number;
  plan_tier?: string;
}

/** TopicShiftProof is the live signal for the topic_shift advisor. */
export interface TopicShiftProof {
  shifted: boolean;
  would_fire: boolean;
}

/** AdvisorDetailResponse is GET /sessions/{id}/advisor-detail. */
export interface AdvisorDetailResponse {
  session_id: string;
  advisories: AdvisoryRow[];
  stale: StaleProof;
  acceleration: AccelerationProof;
  context_window: ContextWindowProof;
  five_hour?: FiveHourProof;
  topic_shift: TopicShiftProof;
}

// --- /cost/summary ---

/** CostGroup enumerates the supported group-by axes. */
export type CostGroup = 'session' | 'project' | 'day' | 'model';

/** CostBucket is one row of the grouped cost summary. */
export interface CostBucket {
  key: string;
  tokens_in: number;
  tokens_out: number;
  cost_usd: number;
  count: number;
}

/** CostSummaryResponse is GET /cost/summary. */
export interface CostSummaryResponse {
  group: CostGroup;
  /** Epoch-milliseconds (0 = unbounded). */
  since: number;
  /** Epoch-milliseconds (0 = unbounded). */
  until: number;
  buckets: CostBucket[];
  total: CostBucket;
}

// --- /usage ---

/**
 * UsageWindow is one rolling-window aggregate (5h, 7d, or 7d-Sonnet).
 * Tokens are returned raw; the UI converts to percentage against a
 * locally-stored plan-tier table. Vendor caps are not published and the
 * server deliberately stays out of that calibration.
 */
export interface UsageWindow {
  /** Window size in seconds (e.g. 18000 = 5h, 604800 = 7d). */
  window_seconds: number;
  /** tokens_in + tokens_out within the window. */
  tokens: number;
  tokens_in: number;
  tokens_out: number;
  messages: number;
  /**
   * Epoch-ms of the oldest message inside the window. Zero when empty.
   * The frontend computes `resets_at = first_msg_ts + window_seconds*1000`.
   */
  first_msg_ts: number;
}

/**
 * OAuthWindow is one vendor-canonical rolling-window utilization slice
 * returned by Anthropic's /api/oauth/usage. Prefer this over the local
 * token estimate because it's bound to the user's actual plan tier.
 */
export interface OAuthWindow {
  /** Percentage of plan cap consumed (0–100+). */
  utilization_pct: number;
  /** Epoch-ms at which the window rolls over (0 if omitted). */
  resets_at: number;
}

/** OAuthUsage mirrors the relevant subset of /api/oauth/usage. */
export interface OAuthUsage {
  five_hour?: OAuthWindow;
  seven_day?: OAuthWindow;
  seven_day_sonnet?: OAuthWindow;
  /** Plan label (e.g. "pro", "max"). */
  subscription_type?: string;
}

/** UsageCLI groups all rolling-window aggregates for a single CLI. */
export interface UsageCLI {
  window_5h: UsageWindow;
  window_7d: UsageWindow;
  /** Anthropic-specific Sonnet sub-window. Zero-valued for non-Claude CLIs. */
  window_7d_sonnet: UsageWindow;
  /**
   * Vendor-canonical utilization, when available. Always nil for non-Claude
   * CLIs in v1. The frontend MUST prefer these percentages over the local
   * token estimate when present.
   */
  oauth?: OAuthUsage;
}

/** UsageResponse is GET /usage. */
export interface UsageResponse {
  /** Server clock at calculation time (epoch-ms). */
  now: number;
  claude: UsageCLI;
  codex: UsageCLI;
}

// --- /cockpit/threads ---

/**
 * CockpitThread is one tile on the cockpit page. A "thread" is a
 * (session_id, git_branch, cwd) tuple — Claude Code shares a sessionId
 * across parallel `claude --resume <id>` invocations, so we use the
 * branch + cwd a message was written from to disambiguate them.
 */
export interface CockpitThread {
  session_id: string;
  cli: string;
  project_path: string;
  git_branch: string;
  cwd: string;
  model: string;
  /** Epoch-ms of the most recent message in this bucket. */
  last_msg_at: number;
  msg_count: number;
  tokens_in: number;
  tokens_out: number;
}

/** CockpitThreadsResponse is GET /cockpit/threads. */
export interface CockpitThreadsResponse {
  threads: CockpitThread[];
}

// --- /settings ---

/** TaskModel is the per-internal-task model selection. */
export interface TaskModel {
  provider: string;
  model: string;
}

/** SettingsAI mirrors config.AIConfig as exposed over HTTP. */
export interface SettingsAI {
  summary_model: TaskModel;
  title_model: TaskModel;
  embed_model: TaskModel;
}

/** DetectedProviders reports which credentials the daemon currently sees. */
export interface DetectedProviders {
  anthropic: boolean;
  openai: boolean;
  gemini: boolean;
  ollama: boolean;
}

/** SettingsResponse is GET /settings. */
export interface SettingsResponse {
  ai: SettingsAI;
  detected: DetectedProviders;
}

/** SettingsUpdateRequest is PUT /settings. */
export interface SettingsUpdateRequest {
  ai?: SettingsAI;
}

// --- /wizard/* ---

/** WizardConnectors reports whether the v1 source directories exist. */
export interface WizardConnectors {
  claude_root: string;
  claude_ok: boolean;
  codex_root: string;
  codex_ok: boolean;
}

/** WizardRecommendation is one row of the smart-model-picker screen. */
export interface WizardRecommendation {
  task: string;
  selected: TaskModel;
  reason: string;
}

/** WizardDetectResponse is GET /wizard/detect. */
export interface WizardDetectResponse {
  connectors: WizardConnectors;
  providers: DetectedProviders;
  recommendations: WizardRecommendation[];
}

// --- /healthz ---

/** HealthzResponse is GET /healthz. */
export interface HealthzResponse {
  ok: boolean;
  version: string;
  schema_version: number;
}

// ---------------------------------------------------------------------------
// api package — SSE event payloads (internal/api/sse_events.go)
// ---------------------------------------------------------------------------

/** SSE event name constants — must match Go's EventXxx string values. */
export const SSE_EVENT_MSG_NEW = 'msg.new' as const;
export const SSE_EVENT_SUMMARY_READY = 'summary.ready' as const;
export const SSE_EVENT_SESSION_UPDATE = 'session.update' as const;
export const SSE_EVENT_COST_TICK = 'cost.tick' as const;
export const SSE_EVENT_THREAD_REBUILD = 'thread.rebuild' as const;
export const SSE_EVENT_COMPACT_DETECTED = 'compact.detected' as const;

/** MsgNew fires when a new message has been parsed and inserted. */
export interface MsgNew {
  session_id: string;
  message_id: string;
  ts: number;
  role: string;
  model: string;
  tokens_in: number;
  tokens_out: number;
  cost_usd: number;
}

/** SummaryReady fires when the summarizer worker writes a new session_summaries row. */
export interface SummaryReady {
  session_id: string;
  version: number;
  ts: number;
  model: string;
}

/** SessionUpdate fires when session aggregate counters change. */
export interface SessionUpdate {
  session_id: string;
  last_msg_at: number;
  msg_count: number;
  cost_usd: number;
  status: string;
}

/** CostTick fires periodically with the rolling daily total. */
export interface CostTick {
  ts: number;
  total_usd_today: number;
}

/** ThreadRebuild fires after thread builder regenerates thread groupings. */
export interface ThreadRebuild {
  thread_count: number;
  ts: number;
}

/** CompactDetected fires when a /compact event is recognized. */
export interface CompactDetected {
  session_id: string;
  ts: number;
}

// ---------------------------------------------------------------------------
// Query parameter shapes (for typed fetch wrapper inputs)
// ---------------------------------------------------------------------------

export interface SessionListQuery {
  cli?: CLI;
  project?: string;
  limit?: number;
  before?: number;
}

export interface MessageListQuery {
  limit?: number;
  before?: number;
  /** Default 'asc'. Use 'desc' for cockpit-style "give me the tail" reads. */
  order?: 'asc' | 'desc';
  /** Filter to messages emitted from this git branch. Send the empty
   *  string to match rows whose branch is unknown / pre-migration. */
  branch?: string;
  /** Filter to messages emitted from this working directory. */
  cwd?: string;
}

export interface CostSummaryQuery {
  group: CostGroup;
  since?: number;
  until?: number;
}

// ---------------------------------------------------------------------------
// /usage/stats — tokscale-inspired cross-session aggregates
// ---------------------------------------------------------------------------

export interface UsageStatsQuery {
  /** "claude" or "codex". Both when blank. */
  cli?: CLI | '';
  /** Lookback in days. Default 30. Server clamps to [1, 365]. */
  days?: number;
  /** Heatmap span in weeks. 0 disables. Default 12. Server clamps to [0, 52]. */
  heatmap_weeks?: number;
}

export interface DailyRow {
  date: string;
  day_start_ms: number;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  total: number;
  cost_usd: number;
  messages: number;
}

export interface ModelRow {
  model: string;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  total: number;
  cost_usd: number;
  sessions: number;
  share_pct: number;
}

export interface HeatmapCell {
  date: string;
  /** 0 = Sunday, 6 = Saturday. */
  weekday: number;
  messages: number;
  /** 0 (none) .. 4 (max). */
  intensity: number;
}

export interface UsageStatsResponse {
  from: string;
  to: string;
  total_messages: number;
  total_sessions: number;
  total_input: number;
  total_output: number;
  total_cache_read: number;
  total_cache_write: number;
  total_cost_usd: number;
  favorite_model: string;
  peak_hour: number;
  peak_hour_local: string;
  current_streak: number;
  longest_streak: number;
  active_days: number;
  window_days: number;
  daily: DailyRow[];
  models: ModelRow[];
  heatmap?: HeatmapCell[];
}
