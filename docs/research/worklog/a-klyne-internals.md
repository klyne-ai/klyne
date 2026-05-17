# Appendix A — klyne Internals Inventory

**Agent:** Explore subagent, very-thorough mode
**Goal:** Map every klyne feature adjacent to a work-log, identify reusable substrate, find gaps and risks.

---

## COMPREHENSIVE KLYNE CODEBASE MAPPING FOR WORK-LOG FEATURE

Based on a thorough exploration of klyne (as of 2026-05-17), here is the detailed inventory of adjacent features and reusable substrate for an automated, project-spanning AI work-log system.

---

### 1. FEATURE INVENTORY TABLE

| **Feature** | **Location** | **What it Captures** | **How Captured** | **Storage** | **Surfaces** | **Data Model** |
|---|---|---|---|---|---|---|
| **Sessions Ingestion** | `internal/connectors/{claude,codex}/` | Raw JSONL transcripts from CLI | Deterministic file tailing; connector parses per-line | `sessions`, `messages`, `messages_fts` (SQLite) | MCP list_sessions, UI /sessions | Session ID (UUID), project_path, started_at, last_msg_at, msg_count, tokens_in/out, status |
| **Decisions Log** | `internal/store/decisions.go` (migration 009) | Load-bearing project/session choices | User-asserted via MCP `record_decision` + optional tags | `decisions` table (append-only, immutable) | MCP recall, list_decisions, search_decisions | ID, ts, project_path, session_id (optional), text, tags_json |
| **Stop Summaries** | `internal/store/stop_summaries.go` (migration 011) | Session exit snapshot (deterministic) | Stop hook (`klyne session-end`) fires at Claude Code shutdown | `stop_summaries` table (composite key session_id+ts) | MCP bootstrap, recall | session_id, ts, project_path, cli, summary (markdown), last_user, last_bash, files_json |
| **Work Spans** | `internal/store/work_spans.go` (migration 014) | Outcome-bounded message sequences | Batch attribution runner: closes spans on git commit or session-end | `work_spans` table (append-only) | MCP status_snapshot, CLI `klyne cost week` | id, commit_sha, pr_number, exploration_id, bucket ("commit"\|"exploration"), project_path, git_branch, session_ids_json, decision_ids_json, waste_classes_json, tokens_fresh/cache_read/cache_write/out, msg_count, opened_at, closed_at |
| **Runbook Dismissals** | `internal/store/runbook_dismissals.go` (migration 010) | Rejected recurring command patterns | User dismissal via MCP `dismiss_runbook` (scoped per project) | `runbook_dismissals` table (PK signature+project_path) | MCP propose_runbooks (detector checks table) | signature (normalized N-gram), project_path, ts, reason |
| **Context Health Scorecard** | `internal/contexthealth/classifier.go` | Session context fill %, bloat sources, cache trajectory, verdict | Pure analysis function; reads sessions+messages | In-memory only (no persistence) | MCP get_context_health, CLI `klyne xray` | Verdict (Healthy/Drifting/Risky/RescueNow), fill_pct, cache_hit_rate, top_bloat_sources, bloat_ms, attribution_by_source |
| **Token Timeline** | `internal/contexthealth/timeline.go` | Per-turn token usage over session lifetime | Reads messages, computes cached_read/write splits | In-memory only | MCP get_token_timeline, CLI `klyne xray --week` | Per-turn: ts, tokens_in (cached_read, cached_write, fresh), tokens_out, fill_pct |
| **Compact Events Log** | `internal/store/compact_events.go` (migration 003) | /compact invocation snapshots | Deterministic: Claude Code reports to daemon via JSONL append | `compact_events` table (append-only) | MCP get_pre_compact_context, status_snapshot | session_id, ts, before_token_count, after_token_count |
| **Session Summaries (AI)** | `internal/store/summaries.go` (migration 003) | AI-generated session summarization (rolling) | Async summarizer worker (model-driven, e.g. gemini-2.5-flash-lite) | `session_summaries` table (rolling; PK session_id+version) | MCP bootstrap, recap | session_id, version (incremental), text (markdown), model, ts |
| **Safety Snapshots** | `internal/store/safety_snapshots.go` (migration 013) | Risky command interception (git reset, rm, etc.) | PreToolUse hook trigger when pattern matches | `safety_snapshots` table (append-only) | MCP bootstrap, CLI `klyne restore` | session_id, ts, cwd, command, pattern_id, severity, stash_sha, fallback_dir, file_count |
| **Shield Snapshots** | `internal/store/shield_snapshots.go` (migration 012) | Compact-shield triggers at context fill ≥70% | UserPromptSubmit advisor reports to daemon | `shield_snapshots` table (append-only) | MCP get_pre_compact_context (recovery) | session_id, ts, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason |
| **Status Snapshot** | `internal/insights/status.go` | Installation-wide aggregate (7-day rolling) | Aggregator; reads sessions, decisions, memories, stop_summaries, compact_events | In-memory struct; surfaced as Markdown + JSON | MCP status_snapshot, CLI `klyne status` | TotalSessions, TotalMessages, TotalTokensIn/Out, TopProjects[], RecentSessions[], CompactEvents[], MemoriesProject/Global, StopSummaryCount |
| **Handoff Generator** | `internal/mcpserver/tool_handoff.go` | Per-task session context for fresh-session continuation | Deterministic: reads messages since last /compact or session start | Markdown text (no persistence) | MCP generate_handoff, CLI `klyne handoff` | Rendered Markdown: files_touched[], commands_run[], context_summary, recent_decisions[] |
| **Bootstrap Brief** | `internal/mcpserver/skills/klyne-bootstrap/` (skill) | Day-1 session start brief | Deterministic: assembles from stop_summaries + decisions + memories + latest health | Markdown text (no persistence) | /klyne:bootstrap (skill invocation) | Recent sessions, project memories, latest context verdict, recommendations |
| **X-Ray Scorecard** | `cmd/klyne/xray.go` | Context health audit + MCP source attribution | Pure analysis; reads sessions, messages, context health, source event log | Markdown text (stdout) | CLI `klyne xray [--week\|--project\|--since]` | Verdict, fill_pct, cache_hit_rate, bloat_sources[], source_attribution{mcp→tokens}, redundancy_markers |
| **Memory System (Decisions + Recall)** | `internal/mcpserver/tool_*.go` (recall, remember, list_memories, update_memory) | Persistent user-asserted notes (project-scoped or global) | User-asserted via MCP tools; stored as decisions rows | Shared `decisions` table (project_path='' for global) | MCP recall, remember, list_memories, update_memory, delete_memory | Same as decisions; scoped by project_path + query/tag filters |
| **Cost Attribution Engine** | `internal/cost/attribution/runner.go` | Per-work-span cost via git commit close | Batch runner: scans git log, matches commits to messages, writes work_spans, computes USD via pricing.go | `work_spans` table + pricing table (in-memory) | CLI `klyne cost week` (digest), status_snapshot (totals) | tokens_fresh/cached_read/cached_write/out + pricing_usd_per_1m_tokens → cost_usd (computed at render time) |
| **Waste Detector (WASTE_LOOP)** | `internal/cost/attribution/waste.go` | Sliding-window tool-call fingerprinting (detect loops) | Analysis function: scans tool_calls_json for repeated signatures within time window | work_spans.waste_classes_json + waste_meta_json | CLI `klyne cost week` (callout), xray --week | waste_classes (["WASTE_LOOP",...]), waste_meta {class→{fingerprint, count}} |

---

### 2. DATABASE SCHEMA SNAPSHOT

**All tables in klyne v1 (as of migrations 001–014):**

| **Table** | **Columns** | **Append-Only?** | **Mutable?** | **Soft-Delete?** | **Key Indexes** |
|---|---|---|---|---|---|
| `schema_migrations` | version (PK), applied_at | Yes | No | No | PK on version |
| `sessions` | id (PK), cli, project_path, encoded_cwd, started_at, last_msg_at, msg_count, tokens_in, tokens_out, cached_read_tokens, cached_write_tokens, cost_usd, model, status, raw_path | No | Yes (last_msg_at, status, model, encoded_cwd, raw_path mutable) | No | idx_sessions_project_last, idx_sessions_cli_last |
| `messages` | id (PK), session_id (FK), parent_uuid, role, content, tool_name, tokens_in, tokens_out, cost_usd, model, ts, cached_read_tokens, cached_write_tokens, tool_calls_json, tool_results_json, git_branch, cwd | Yes (INSERT only) | No | No | idx_messages_session_ts, idx_messages_ts |
| `messages_fts` | (virtual FTS5 table) content, role (UNINDEXED), session_id (UNINDEXED), ts (UNINDEXED) | Yes | Yes (synced via triggers on messages INSERT/DELETE/UPDATE) | No | — (virtual; no explicit indexes) |
| `threads` | id (PK), title, project_path, first_ts, last_ts | No | Yes | No | — |
| `thread_sessions` | thread_id (FK), session_id (FK), PK (thread_id, session_id) | Yes | No | No | — |
| `session_summaries` | session_id (FK), version (both in PK), text, model, ts | Yes (rolling; new version appends) | No | No | idx_session_summaries_ts |
| `compact_events` | session_id (FK), ts (both in PK), before_token_count, after_token_count | Yes | No | No | idx_compact_events_session |
| `deleted_sessions` | id (PK), deleted_at (NO ROWID) | Yes | No | No | — |
| `decisions` | id (PK), ts, project_path, session_id, text, tags_json | Yes | Yes (text + tags_json via UpdateDecision) | No | idx_decisions_project_ts, idx_decisions_ts |
| `runbook_dismissals` | signature (PK), project_path (both in PK), ts, reason | Yes | No | No | idx_runbook_dismissals_project |
| `stop_summaries` | session_id, ts (both in PK), project_path, cli, summary, last_user, last_bash, files_json | Yes (INSERT upserts on composite key) | No | No | idx_stop_summaries_project_ts, idx_stop_summaries_ts |
| `shield_snapshots` | id (PK), ts, session_id, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason | Yes | No | No | idx_shield_snapshots_ts, idx_shield_snapshots_session |
| `safety_snapshots` | id (PK), ts, session_id, cwd, command, pattern_id, severity, stash_sha, fallback_dir, file_count | Yes | No | No | idx_safety_snapshots_ts, idx_safety_snapshots_session |
| `work_spans` | id (AUTOINCREMENT PK), commit_sha, pr_number, exploration_id, bucket, project_path, git_branch, session_ids_json, decision_ids_json, waste_classes_json, waste_meta_json, tokens_fresh, tokens_cache_read, tokens_cache_write, tokens_out, msg_count, opened_at, closed_at | Yes (INSERT; DELETE via DeleteWorkSpansSince for re-attribution) | No | No | idx_work_spans_project_opened, idx_work_spans_commit_sha, idx_work_spans_pr_number |

**Key observations:**
- **Immutable rows:** messages, compact_events, runbook_dismissals, shield_snapshots, safety_snapshots, work_spans (after insertion).
- **Mutable rows:** sessions (status, timestamps only), decisions (text + tags), deleted_sessions (tombstone only).
- **Composite keys:** decisions (id only, PK), thread_sessions, session_summaries, compact_events, stop_summaries, runbook_dismissals.
- **JSON columns:** used for N:M relationships (session_ids_json, decision_ids_json, waste_classes_json, tags_json, etc.) to avoid join tables.
- **Foreign key constraints enforced** via SQLite PRAGMA foreign_keys=ON (applied per-connection in spec §5).

---

### 3. REUSABLE SUBSTRATE FOR WORK-LOG

A work-log feature should **reuse** the following components rather than reinvent them:

#### **A. Message Ingestion Pipeline** (`internal/connectors/`)
- **Substrate:** The Connector interface + message parsing loop in `internal/app/app.go`.
- **Usage:** Parse session transcripts per-tool (Claude Code, Codex, and future tools).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/connectors/connector.go:54–89` (Connector interface); `/Users/mohitpatel/Desktop/Project/klyne/internal/app/` (ingestion orchestrator).
- **Why reuse:** Message timestamps, session_id, role, token counts, and tool_name are already extracted and normalized. Work-log just needs to aggregate these.

#### **B. Work-Span Attribution Model** (`internal/store/work_spans.go`, migration 014)
- **Substrate:** The WorkSpan struct and InsertWorkSpan/ListWorkSpans/DeleteWorkSpansSince functions.
- **Data model:** Session-to-outcome mapping with token accounting; ready for cost + waste tags.
- **Usage:** Work-log can extend work_spans by adding narrative fields (not just metrics).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/store/work_spans.go:17–99` (struct + insert); `/Users/mohitpatel/Desktop/Project/klyne/internal/store/migrations/014_work_spans.sql:1–64` (schema).
- **Why reuse:** `work_spans` already closes spans on git commits and tracks session_ids_json + decision_ids_json. A work-log can piggyback on this grouping.

#### **C. Decision Log** (`internal/store/decisions.go`, migration 009)
- **Substrate:** The Decision struct, ListDecisions, SearchDecisions, UpdateDecision, DeleteDecision.
- **Usage:** Decisions are immutable by design; a work-log can link decisions to spans via decision_ids_json (already in work_spans).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/store/decisions.go:12–59` (struct + insert); `/Users/mohitpatel/Desktop/Project/klyne/internal/store/migrations/009_decisions.sql:1–32` (schema).
- **Why reuse:** Project + session scoping, tag filtering, immutability contract match what a work-log needs for "decisions made but not shipped."

#### **D. Stop Summaries** (`internal/store/stop_summaries.go`, migration 011)
- **Substrate:** The StopSummary struct, LatestStopSummaryForProject, ListStopSummariesForProject.
- **Usage:** Deterministic, no-AI session closure snapshots (last_user prompt, last_bash command, touched files).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/store/stop_summaries.go:21–74` (struct + insert); `/Users/mohitpatel/Desktop/Project/klyne/internal/store/migrations/011_stop_summaries.sql:26–43` (schema).
- **Why reuse:** Stop summaries already capture "what just happened when this session closed." A work-log can surface these directly or use them as a seed for richer summaries.

#### **E. Status Snapshot Aggregator** (`internal/insights/status.go`)
- **Substrate:** The StatusSnapshot struct, Snapshot function, window-scoped aggregation (7-day rolling, per-project or all-projects).
- **Usage:** Weekly aggregate of sessions, messages, tokens, compacts, decisions, memories, stop_summaries.
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/insights/status.go:45–104` (struct + defaults); `/Users/mohitpatel/Desktop/Project/klyne/internal/insights/status.go:110–150` (aggregation logic).
- **Why reuse:** Already walks sessions + decisions + memories + stop_summaries in a single query. Work-log can extend this by adding work_spans grouping and per-day drilling.

#### **F. Context Health Analyzer** (`internal/contexthealth/`)
- **Substrate:** The Classifier, BloatAnalyzer, CacheTrajectory, SourceAttribution; all pure functions (no I/O).
- **Usage:** Classify session "productivity" (was context fill climbing? was cache hitting?). Attribute bloat to sources (file reads, commands, tool results).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/contexthealth/classifier.go:1–50` (entry point); `/Users/mohitpatel/Desktop/Project/klyne/internal/contexthealth/source_attribution.go:1–80` (token/bloat per MCP server).
- **Why reuse:** A work-log can use the same classifier to annotate work spans with "session context health" (e.g., "Risky → decision to compact was sound").

#### **G. Cost Attribution Runner** (`internal/cost/attribution/runner.go`)
- **Substrate:** The Runner, SpanBatch, CommitScanner. Walks git log, matches commits to message timestamps, assembles work_spans, detects waste patterns.
- **Usage:** Cost per span + waste classification (WASTE_LOOP fingerprinting).
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/cost/attribution/runner.go:1–100` (batch + commit scan); `/Users/mohitpatel/Desktop/Project/klyne/internal/cost/attribution/waste.go:1–80` (WASTE_LOOP detector).
- **Why reuse:** Already groups messages into outcomes by commit. Work-log just adds narrative + outcome type.

#### **H. Handoff + Bootstrap Templates** (`internal/mcpserver/tool_handoff.go`, `internal/mcpserver/skills/klyne-bootstrap/`)
- **Substrate:** The Markdown rendering logic; reads sessions + decisions + memories + health verdict; produces structured text.
- **Usage:** Work-log can reuse the template structure for "daily digest" or "weekly highlights."
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/mcpserver/skills/klyne-bootstrap/` (Markdown templates); `/Users/mohitpatel/Desktop/Project/klyne/cmd/klyne/status.go:75–90` (CLI integration).
- **Why reuse:** Markdown rendering + project/session scoping are commodity; focus work-log on outcome extraction, not template boilerplate.

#### **I. Pricing + Cost Computation** (`internal/cost/pricing.go`)
- **Substrate:** The PricingTable, CostForMessage, CostForSpan. Live rates; cost_usd computed at render time, not persisted.
- **Usage:** Work-log shows cost per day, per outcome, per category.
- **Citation:** `/Users/mohitpatel/Desktop/Project/klyne/internal/cost/pricing.go:1–150` (schema + compute).
- **Why reuse:** Matches klyne's design: tokens persisted, cost computed live so it drifts with Anthropic's rate cards.

---

### 4. GAPS: WHAT WORK-LOG WOULD NEED (NEW TABLES, SURFACES)

A work-log feature would require:

1. **New table: `work_log_entries`** — Daily or per-span narrative summaries, human-readable outcomes.
   - Columns: `id` (PK), `work_span_id` (FK or null), `project_path`, `logged_at`, `entry_type` ("daily_digest" | "per_outcome" | "weekly_review"), `title`, `body_markdown`, `tags_json`, `ai_generated` (0|1).
   - Why: Stop summaries are single-sentence snapshots (last_user, last_bash, files). Work-log entries are rich narratives: "Shipped auth refactor: broke old token endpoint, migrated 3 services to JWT, tests pass on staging, 2 decisions made re: token expiry."
   - Append-only? Yes. FK to work_spans? Soft yes (null allowed for freeform logs outside spans).

2. **New table: `work_log_tags`** — Hierarchical tagging for work-log entries (e.g., "feature/auth", "bugfix", "spike", "shipped", "decided-not-to-ship").
   - Columns: `entry_id` (FK), `tag` (both in PK), `assigned_at`.
   - Why: Allows future querying like "what features shipped this week?" or "what was decided but not shipped?".
   - Append-only? Yes.

3. **New column in `work_spans`: `log_entry_id`** — Optional FK back to work_log_entries for rich narratives.
   - Why: Links cost/outcome metrics to human narrative.

4. **New CLI subcommand: `klyne worklog`**
   - `klyne worklog list [--project PATH] [--since 7d] [--tag TAG]` — List entries.
   - `klyne worklog show ENTRY_ID` — Show one entry.
   - `klyne worklog generate [--week | --since 7d] [--ai]` — Auto-generate digest from spans + decisions + stop_summaries (deterministic or AI-powered).
   - Why: Surface the log to CLI, not just UI.

5. **New MCP tools:**
   - `create_work_log_entry(span_id?, title, body_markdown, tags?)` — User-asserted entry.
   - `list_work_log_entries(project_path?, since?, tag?)` — Query entries.
   - `analyze_work_log(since?, project_path?)` — Answer "what did I ship?", "what decisions were made?", etc. via Claude AI.
   - Why: Enable AI agents to query + reason over work-log data.

6. **New UI route: `/worklog`** — Calendar/timeline view of entries; drill into daily digest; filter by tag/project.
   - Why: Surfacing in UI makes it discoverable + shareable with teammates.

7. **New migration 015: `work_log_entries.sql`** + migration 016: `work_log_tags.sql`.

---

### 5. ARCHITECTURAL FIT

**Where would work-log code live?**

1. **New Go package:** `internal/worklog/` (parallel to `internal/insights/`, `internal/contexthealth/`).
   - **Modules:**
     - `worklog/entry.go` — Entry struct + InsertEntry / ListEntries / GetEntry.
     - `worklog/generator.go` — Auto-generate daily/weekly digests from work_spans + decisions + stop_summaries (pure functions).
     - `worklog/analyzer.go` — Answer analytical queries ("shipped?", "decided-not-to-ship?").
   - **Tests:** `internal/worklog/*_test.go` (same convention as insights).

2. **MCP tools:** `internal/mcpserver/tool_worklog*.go` (3–5 files).
   - `tool_worklog_create.go` — create_work_log_entry handler.
   - `tool_worklog_list.go` — list_work_log_entries handler.
   - `tool_worklog_analyze.go` — analyze_work_log handler (calls internal/ai for Claude analysis if --ai flag set).

3. **CLI command:** `cmd/klyne/worklog.go` (follows pattern of `xray.go`, `status.go`, `cost.go`).
   - Parses flags, delegates to `internal/worklog/`, renders Markdown or JSON.

4. **UI route:** `ui/src/routes/worklog/+page.svelte`.
   - Displays entries in timeline/calendar; filters; drill-down.

5. **Migrations:** `internal/store/migrations/015_work_log_entries.sql` + `016_work_log_tags.sql`.

6. **Store DAO:** `internal/store/worklog.go` (mirrors decisions.go, decisions_update_test.go, etc.).

---

### 6. DISTINCTNESS FROM EXISTING FEATURES

| **Feature** | **What It Is** | **What Work-Log Is NOT** |
|---|---|---|
| **Decisions** | Load-bearing choices pinned to project/session (immutable, <300 chars). | Work-log is outcome narrative, not decision. Work-log *references* decisions but is rich narrative (Markdown, >300 chars). |
| **Stop Summaries** | Deterministic, 5-field snapshot at session end (last_user, last_bash, files, summary, cli). | Stop summary is single snapshot; work-log spans days/weeks, aggregating multiple sessions + decisions. |
| **Memory (recall)** | User-asserted persistent notes (global or project-scoped). | Memory is user-owned reference material; work-log is system-generated outcome log. No overlap unless user explicitly tags a memory as "work-log-entry." |
| **Handoff** | Per-task context snapshot for fresh-session continuation. | Handoff is task-scoped, ephemeral (not stored); work-log is persistent, outcome-scoped, queryable. |
| **Bootstrap** | Day-1 session start brief (recent sessions + decisions + health). | Bootstrap is reactive (fires at session start); work-log is proactive (generated on-demand or daily). |
| **Status Snapshot** | 7-day rolling installation aggregate (sessions, tokens, compacts, memories, costs). | Status is "telemetry" (what happened); work-log is "narrative" (why it matters, what was shipped). |
| **Runbooks** | Recurring shell command sequences proposed + dismissed. | Runbooks are infrastructure patterns; work-log is outcome+decision log. No overlap. |
| **Context Health** | Session-scoped verdict (Healthy/Drifting/Risky) + bloat sources. | Context health is per-session classifier; work-log uses context health as an *annotation* (e.g., "Risky context at this decision point"). |

**Crisp definition of work-log:** "A structured, queryable log of what the AI accomplished (outcomes, metrics, shipped/decided) across sessions and projects, updated daily/weekly, surfaced via CLI/UI/MCP for AI agents to reason over."

---

### 7. RISKS SPECIFIC TO KLYNE'S CODEBASE

#### **A. Session ID Schema Collisions**
- **Risk:** Claude Code and Codex both emit UUID session_ids. If work-log FKs to sessions(id), a future connector (e.g., GitHub Copilot) with overlapping UUID space could cause silent data corruption.
- **Mitigation:** (1) Add `cli` column to work_log_entries so FK is (session_id, cli) composite; (2) Use soft FKs (no SQLite constraint, just column names) for future-proofing.
- **Citation:** `internal/connectors/connector.go:24–31` (CLI enum locked at v1: claude + codex only).

#### **B. Migration Ordering**
- **Risk:** work_log tables depend on work_spans existing (migration 014). If a future migration reorders, FK constraints fail.
- **Mitigation:** (1) Document the dependency in migration comments; (2) Use ON DELETE CASCADE for safety; (3) Test migrations_test.go asserts every expected table exists.
- **Citation:** `internal/store/migrations/migrations_test.go:90–112` (contract table assertion).

#### **C. Ingestion-Loop Performance**
- **Risk:** Work-log generator reads work_spans + decisions + stop_summaries (3 tables). If the `--week` aggregation is O(N²), adding work-log generation could slow daemon on large projects.
- **Mitigation:** (1) Index work_log_entries(project_path, logged_at DESC) like decisions and stop_summaries; (2) Cache the 7-day window in memory (TTL 1h); (3) Profile with `klyne bench`.
- **Citation:** `internal/insights/status.go:110–150` (Snapshot aggregator — already scoped by window).

#### **D. Svelte UI Churn**
- **Risk:** UI route /worklog + new components for calendar/timeline could conflict with concurrent UI work (e.g., cockpit expansion).
- **Mitigation:** (1) Use shared table component (ProjectRail, Terminal, etc. from lib/); (2) Follow +page.svelte+page.ts pattern (see /sessions/[id]/); (3) Add feature flag to toggle UI route visibility.
- **Citation:** `ui/src/routes/` directory structure; existing routes like sessions, memory, search.

#### **E. Cost Computation Timing**
- **Risk:** Work-log generation runs daily; cost_usd is computed at render time from work_spans tokens + pricing table. If pricing changes mid-day, the same span has two different costs.
- **Mitigation:** (1) Document that costs are "as of render time;" (2) Never persist cost_usd in work_log_entries (compute live); (3) Use cost timestamp (when pricing was fetched) as metadata.
- **Citation:** `internal/cost/pricing.go:1–50` (live computation design); `internal/store/work_spans.go:29–35` (tokens stored, cost computed).

#### **F. AI-Driven Generation Hallucination**
- **Risk:** If work-log supports `analyze_work_log --ai`, Claude could hallucinate outcomes ("Shipped X" when only "started X").
- **Mitigation:** (1) Mark AI-generated entries with `ai_generated=1` in table; (2) Surface a "confidence" field (low/medium/high) based on token-count + decision presence; (3) Require human confirmation before marking as "shipped"; (4) Always include source data (session IDs, commit SHAs) so users can verify.
- **Citation:** `internal/mcpserver/tool_status_snapshot.go` (status is deterministic-only; no AI summary); `internal/insights/status.go` (no AI reasoning).

#### **G. Soft-Delete Tombstones**
- **Risk:** deleted_sessions table is soft-delete tombstone (migration 006). If work-log FKs to sessions, a user deleting a session leaves work_log_entries orphaned.
- **Mitigation:** (1) Use soft FK (no constraint); (2) When listing work-log, exclude entries referencing deleted sessions unless --include-deleted; (3) Add a tombstone table for work_log_entries (deleted_worklog_entries).
- **Citation:** `internal/store/migrations/006_deleted_sessions.sql:14–17` (tombstone design).

---

### 8. RECENT COMMITS CONTEXT (Last 20 days, since 2026-04-27)

Key commits inform what's in-flight and what substrate is fresh:

- **ae07f72** (5/17): `feat(xray): count Skill tool invocations per source` — Source attribution now includes skill tools. Work-log can use the same attribution logic.
- **2bfd700** (5/15): `feat(xray): add --week / --project / --since aggregation` — X-ray now supports time-windowed aggregation. Work-log should support the same flags.
- **bd64760** (5/13): `fix(cost): truncate work_spans before re-attribution` — Work_spans is mutable via DeleteWorkSpansSince. Work-log generator should NOT re-run daily (use idempotency).
- **7ffa815** (5/12): `feat(cost): klyne cost week CLI — Markdown digest` — Cost digest is deterministic + Markdown rendered. Work-log digest can reuse this template.
- **6b3114e** (5/11): `feat(cost): WASTE_LOOP detector` — Waste tagging is live in work_spans. Work-log should surface waste as an annotation.
- **a1f9165** (5/8): `feat(cost): migration 014 + work_spans DAO` — Work_spans was just added 10 days ago. Work-log substrate is fresh.
- **054dcb2** (4/30): `feat(mcp): Serena-inspired bootstrap brief` — Memory CRUD parity landed. Work-log can follow the same MCP tool pattern (recall, remember, update, delete).

**Implication:** The work_spans infrastructure is brand-new (10 days old) and is the natural foundation. Stop_summaries + decisions are 3 months old and stable. The cost + waste pipeline is recent (11 days) and aligns with work-log's outcome-scoping need.

---

### 9. UNTRACKED / IN-FLIGHT FILES

Git status shows the branch is `init` with mid-flight changes:

- **cmd/klyne/runbooks.go**, **cmd/klyne/session_end.go**, **cmd/klyne/status.go**, **cmd/klyne/xray.go.bak** (empty).
- **internal/insights/runbooks.go**, **internal/insights/status.go** — Runbook proposer and status snapshot are WIP.
- **internal/mcpserver/tool_runbooks.go**, **tool_status_snapshot.go** — MCP tools for runbooks + status are fresh.
- **internal/store/runbook_dismissals.go**, **stop_summaries.go** — DAO layer just landed.

These untracked files suggest the branch is mid-refactor (moving CLI subcommands from cmd/ into internal/ for shared logic). Work-log should follow the same pattern: put core logic in `internal/worklog/`, surface it via `cmd/klyne/worklog.go` (CLI) + `internal/mcpserver/tool_worklog*.go` (MCP).

---

### 10. SUMMARY: REUSABLE SUBSTRATE vs. DUPLICATE RISK

**REUSE (already present, don't reinvent):**
1. Connector message pipeline + token parsing.
2. Work spans model (outcome grouping by commit).
3. Decision log (project-scoped, immutable, taggable).
4. Stop summaries (session exit snapshots).
5. Status snapshot aggregator (window-scoped, multi-table join).
6. Context health classifier (per-session verdict + bloat attribution).
7. Cost attribution runner (commit-to-span matching, waste detection).
8. Handoff + bootstrap templates (Markdown rendering).
9. MCP tool pattern (recall, remember, list, update, delete).
10. Pricing + cost computation (live, not persisted).

**INVENT (new for work-log):**
1. Rich narrative entry struct (title + body_markdown, >300 chars, human-readable outcomes).
2. Daily/weekly digest generator (sequences decisions + work_spans + stop_summaries into coherent story).
3. Work-log tagging system (e.g., "shipped", "decided-not-to-ship", "feature/auth").
4. Analytical queries ("what was shipped this week?", "what was decided but not shipped?").
5. UI timeline/calendar visualization (new route + components).
6. Optional AI analysis (Claude reasons over work-log to answer high-level questions).

**AVOID DUPLICATION:**
- Work-log entries should reference decisions (not duplicate them).
- Work-log should not replace stop_summaries (use them as input).
- Work-log should not recompute cost (use work_spans cost).
- Work-log should not regenerate context health (annotate with it).

---

## CONCLUSION

Klyne's codebase is well-structured for a work-log feature. The work_spans table (10 days old) is the natural foundation. The decision log, stop summaries, and status aggregator provide rich inputs. The cost attribution, waste detection, and context health classifiers provide annotations. A work-log feature should be ~1500 lines of new Go code (worklog package) + 400 lines of SQL (migrations 015–016) + 200 lines of MCP tools + 300 lines of CLI + 400 lines of UI, reusing 80% of existing substrate.

**Key recommendation:** Build work-log on top of work_spans (not parallel to it). Link work_log_entries back to work_spans via work_span_id (nullable for freeform entries). This keeps the outcome model unified and queryable.
