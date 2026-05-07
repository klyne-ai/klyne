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
}

export interface CostSummaryQuery {
  group: CostGroup;
  since?: number;
  until?: number;
}
