package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is what the server advertises during the MCP initialize
// handshake. Wired through to mcp.Implementation.Version. Bump this
// when tool semantics change so MCP hosts can detect upgrades.
//
// v0.3.0 — slice 4: Codex parity for all four tools and live MCP
// prompts surfaced under Claude Code's `/klyne:*` slash menu
// (older builds: `/mcp__klyne__*`, retired).
// v0.4.0 — slice 5: search_messages tool + /klyne:search prompt
// for cross-session full-text search.
// v0.5.0 — slice 6 (Serena-inspired): bootstrap session brief +
// memory CRUD (update_memory, delete_memory, list_memories).
// v0.6.0 — slice 7 (bootstrap integration loop): per-session
// fetch tools (get_session, summarize_session) + bootstrap now
// surfaces Claude Code's on-disk auto-memory under a distinct
// section beside klyne's SQLite memory store.
const version = "v0.6.0"

// New constructs the klyne MCP server with every v1 tool
// registered. The returned server is ready for Run.
//
// Tool surface (slice 2):
//
//	list_sessions             — enumerate Claude Code sessions in the
//	                            cwd's project, with previews; the AI
//	                            calls this to disambiguate before
//	                            other tools when multiple sessions
//	                            exist.
//	get_context_health        — classify a session's context health
//	                            and report the top bloat sources.
//	generate_handoff          — produce a deterministic Markdown
//	                            handoff prompt the user can paste into
//	                            a fresh session.
//	get_pre_compact_context   — recover the messages immediately
//	                            preceding the last /compact event.
//
// Codex coverage for these tools is the next slice — Codex transcripts
// use a different schema and a different storage model.
func New() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "klyne",
		Version: version,
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_sessions",
		Description: `Enumerate Claude Code sessions in the current working directory's project.

Returns one row per session with its id, first user-message preview, last-modified timestamp, and an "is_active" flag (modified within the last 30 seconds). Use this when you need to disambiguate before calling other tools, especially when the user has multiple parallel Claude Code sessions in the same project.`,
	}, HandleListSessions)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_context_health",
		Description: `Classify the context health of a Claude Code session and report the top bloat sources.

Returns a verdict (healthy / drifting / risky / rescue_now), a recommended action (continue / compact / start_fresh), a one-sentence reason, the cache-aware context-fill percentage, and the top tool-output sources contributing to bloat.

When session_id is omitted, the tool tries to pick a unique session in the cwd's project. If multiple parallel sessions exist and none is uniquely "active," the response is marked ambiguous and the candidate list is returned — call again with an explicit session_id from that list.`,
	}, HandleGetContextHealth)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "generate_handoff",
		Description: `Produce a deterministic Markdown handoff prompt that the user can paste into a fresh Claude Code session to continue the same task.

The handoff contains: project path, recent task topic, files touched, commands run, recent failures, and the last few user/assistant exchanges. Generated entirely from the JSONL transcript — no AI provider needed. Same disambiguation behaviour as get_context_health.`,
	}, HandleGenerateHandoff)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_messages",
		Description: `Full-text search across every Claude Code and Codex session klyne has indexed.

Use this when the user asks "where did we talk about X?" or "find that conversation about Y" — anything that needs cross-session lookup. Returns hits with session_id (so you can drill in via get_pre_compact_context or generate_handoff), project_path, role, snippet (FTS-highlighted), and timestamp.

Sort defaults to "recent" (newest first); pass sort:"relevance" for BM25 best-match. Optional project_path argument filters hits to one repository.

REQUIRES the klyne daemon to be running (the FTS index lives in SQLite). When unreachable the tool returns daemon_down=true with a clear restart hint instead of hanging.`,
	}, HandleSearchMessages)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_session",
		Description: `Fetch one session's metadata and ordered messages directly from klyne's SQLite store.

Use when you already know the session_id (typically from bootstrap, list_sessions, or search_messages) and need the actual content of the conversation. Pure SQLite read — no JSONL access, no daemon required.

Inputs: session_id (required), optional limit (default 100, max 1000), optional before (epoch-ms cursor for backward pagination), optional since (epoch-ms lower bound), optional order ("asc" default | "desc").

NOTE: since is filtered client-side AFTER limit rows are fetched — combine with a generous limit or page via before when count matters.

Returns: session metadata + the messages slice. Use this instead of falling back to bash + jq over the raw JSONL — klyne is the source of truth.`,
	}, HandleGetSession)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "summarize_session",
		Description: `Synthesise one session's timeline from klyne's own data — stop-hook summaries, the rolling session summary, linked decisions, and files touched — into one Markdown block.

Use after bootstrap when the user asks "what was I working on in session X?" / "summarize session Y for me". Pure SQLite synthesis — no AI call, no JSONL re-scan. Prefer this over generate_handoff for known-session-id digestion (generate_handoff is for the CURRENT session and re-scans the JSONL).

Inputs: session_id (required). Returns: structured fields plus a verbatim-renderable markdown body covering session metadata, stop-summary timeline, files touched, linked decisions, and the latest rolling summary.`,
	}, HandleSummarizeSession)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_pre_compact_context",
		Description: `Recover the messages immediately preceding the last /compact event in a session.

When Claude Code runs /compact, the original turns are replaced by a summary the model itself produced — the AI loses access to the original wording and intent. This tool reads the JSONL transcript directly and returns the raw messages from before the boundary, so the AI can recover decisions, file paths, and reasoning that the compact summary may have flattened. Same disambiguation behaviour as get_context_health.

For Codex sessions, the recovery uses the embedded payload.replacement_history that Codex writes inside its compacted event. Trigger and pre_tokens are not exposed for Codex (it does not record either).`,
	}, HandleGetPreCompactContext)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_token_timeline",
		Description: `Return the per-assistant-turn token usage for the active session over the last 5 hours (or window_hours if provided).

Returns one row per qualifying assistant turn with timestamp, effective input (TokensIn - CachedReadTokens), total input, cached read/write, and output tokens — ready to power either an inline ASCII sparkline (slash prompt /klyne:tokens) or a real line chart in the web cockpit.

When the user has configured a plan tier (klyne config set plan <tier>), the response also reports total uncached input as a percentage of that plan's 5-hour cap. Same disambiguation behaviour as get_context_health.`,
	}, HandleGetTokenTimeline)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "bootstrap",
		Description: `Day-1 session briefing: recent sessions, project + global memories, and the latest session's context-health verdict, all in one call.

Call at session start when you have no prior context for this project, or when the user asks "what was I working on?" / "where did I leave off?". Pure JSONL + SQLite reads — no AI calls. Render the response's markdown field verbatim.`,
	}, HandleBootstrap)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "recap_project",
		Description: `Return the visible worklog entries for a project in the last N days, mixing Claude AND Codex sessions in one timeline.

Use when the user asks "what did I do in project X this week?" / "what did I discuss with Codex about Y?" / "what shipped lately?". Pulls from klyne's worklog Memory layer (stop_summaries with recap_visible=1). Each entry is tagged with its source CLI so the agent can answer cross-tool questions.

Inputs: project_path (required), since_days (default 7), topic (optional substring filter on recap_topic). Returns: entries sorted newest-first, capped at 50.`,
	}, HandleRecapProject)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "user_recap",
		Description: `Aggregate visible worklog entries across ALL projects and ALL CLIs.

Use when the user asks "what did I do this week?" / "what's my AI productivity?" / "summarize the past N days across every project". Returns total count, breakdown by CLI (claude vs codex), breakdown by project, and the top-importance entries.

Inputs: since_days (default 7), group_by ("cli" | "project" | "" for both). Pure SQLite read.`,
	}, HandleUserRecap)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "propose_reflection",
		Description: `Return the pending worklog entries that a Reflection synthesis should consider for a project, plus the trigger reason ("importance-sum N ≥ T" | "weekly cron" | "user-invoked").

Use when the user invokes /klyne:reflect or when you proactively decide to synthesize. The markdown field is ready to render verbatim; the entries field is the structured form for downstream calls to record_reflection.

Pure SQLite read — no AI call. The AI host (you) performs the synthesis and then calls record_reflection to persist the result.`,
	}, HandleProposeReflection)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "record_reflection",
		Description: `Persist a synthesized DAILY reflection for a project (tier=1, "Daily reflection — YYYY-MM-DD"). Enforces the citation invariant — every insight must cite at least one entry session_id from the propose_reflection output.

Call this AFTER bucketing propose_reflection's entries by date and synthesizing per-day insights. Call it ONCE per distinct date — a single /klyne:reflect run may invoke it N times to catch up across N days.

Inputs:
  - project_path (required) absolute project path
  - day (strongly recommended) YYYY-MM-DD in UTC, the calendar day this reflection covers. Omitting it files the reflection under today's UTC date, which is wrong for multi-day catch-up.
  - insights ([{text, evidence: [session_id, ...]}, ...])`,
	}, HandleRecordReflection)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "code_review_context",
		Description: `Surface the optional code-review-graph enrichment for a repository.

Reads <project_root>/.code-review-graph/summary.json (produced by the upstream tirth8205/code-review-graph project) and returns the high-risk files, recent review blockers, and frequent reviewer handles. Gracefully no-ops with detected=false when the directory is absent — most repos won't have the upstream tool installed.

Useful before starting a refactor or code review to learn which files the project has historically struggled with and who to ping.`,
	}, HandleCodeReviewContext)

	// --- decisions log -----------------------------------------------
	// Mini persistent-memory surface: the AI (or the user via the CLI)
	// pins short notes that survive across sessions. Written to klyne's
	// SQLite store; survives daemon restarts and session deletion.
	mcp.AddTool(srv, &mcp.Tool{
		Name: "record_decision",
		Description: `Persist a short, immutable decision so it survives across sessions.

Use this when the user states a load-bearing choice that you want to remember in a future Claude Code or Codex run — "we picked Postgres over SQLite because the team already runs PG", "drop /api/v1 — v2 was rolled out 2026-04-12", etc.

Inputs: text (required), optional project_path (defaults to cwd), optional session_id, optional tags. Returns the decision id so you can echo it back. Free-form text; keep it under ~300 chars for terminal readability.`,
	}, HandleRecordDecision)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_decisions",
		Description: `List recently recorded decisions, scoped by default to the current project.

Use when the user asks "what did we decide about X?" or when you start a new task and want to recall prior choices. Returns up to `+"`limit`"+` rows sorted by recency. Pass all_projects=true to list across every project klyne has touched.`,
	}, HandleListDecisions)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_decisions",
		Description: `Substring-search recorded decisions by text. Case-insensitive.

Use this when the user asks "did we decide anything about Y?" — returns the rows whose text contains the query. Scopes to the current project by default; pass all_projects=true for a cross-project search.`,
	}, HandleSearchDecisions)

	// --- memory facade -----------------------------------------------
	// User-facing verbs over the same decisions table. Fire these when
	// the user uses the trigger phrases:
	//   "klyne remember this …"           → remember (scope=project)
	//   "klyne remember this globally …"  → remember (scope=global)
	//   "refer klyne …" / "check klyne …" → recall (returns project ∪ global)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "remember",
		Description: `Persist a memory (decision, runbook, or note) so it survives across sessions.

Fire this tool when the user says one of:
  "klyne remember this …"             → scope=project (default)
  "klyne remember … for this project" → scope=project
  "klyne remember this globally …"    → scope=global
  "klyne remember … everywhere"       → scope=global

Inputs: text (required), scope ("project"|"global", default "project"), optional project_path / cwd / tags / session_id. Multi-line text and runbooks (e.g. "Steps: 1. …, 2. …") are explicitly supported. Use tags like "runbook", "decision", "secrets", "infra", "deploy" so recall can filter cleanly.

Scoping rules:
  * scope=global  → memory applies to every project; stored with project_path=""
  * scope=project → memory applies only to the named project; project_path is taken from input or falls back to cwd

Returns the new memory's id. Confirm the id and scope back to the user.`,
	}, HandleRememberMemory)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "recall",
		Description: `Recall all memories relevant to the current project — BOTH project-scoped AND global — in one call.

Fire this tool BEFORE acting on operational requests (secrets, deploys, migrations, "add … for service X", etc.) and whenever the user says "refer klyne …" / "check klyne …" / "what does klyne remember about …".

Returns two labelled lists:
  * project_memories — memories whose project_path matches the resolved project
  * global_memories  — memories with project_path="" (apply everywhere)

If a memory looks like a runbook (multi-line with numbered steps), follow it verbatim with variables substituted from the user's request. Confirm the substitution out loud before executing.

Optional filters: query (substring), tag (single tag like "runbook"), project_path / cwd override.`,
	}, HandleRecallMemory)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_memory",
		Description: `Edit an existing memory (text and/or tags) by id without changing scope, project_path, or session_id.

Fire this tool when the user says "klyne update memory <id> …" / "klyne edit that memory …" / "klyne retag this memory …" and you already know the id (typically returned by a prior remember or list_memories call).

Inputs: id (required). At least one of text (new body — must be non-empty when provided) or tags (new full tag set; pass an empty array to clear all tags) must be supplied. Omitted fields are left untouched.

Returns the patched id. If the id is unknown the call errors with "memory <id> not found" so you can tell the user to call list_memories first.`,
	}, HandleUpdateMemory)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_memory",
		Description: `Delete one memory permanently by id.

Fire this tool when the user says "klyne delete memory <id>" / "klyne forget that <id>" / "klyne remove the runbook with id <id>". Confirm the id back to the user BEFORE calling — deletes are immediate and not undoable.

Inputs: id (required). Returns the deleted id. If the id is unknown the call errors with "memory <id> not found" so you can ask the user to call list_memories first.`,
	}, HandleDeleteMemory)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_memories",
		Description: `Enumerate memories without applying a query filter — the explicit "show me everything klyne remembers" surface.

Fire this tool when the user says "klyne list memories" / "klyne what do you remember?" / "klyne show me my runbooks". Use it BEFORE update_memory or delete_memory so you can surface ids the user can pick from.

Returns two labelled lists (project_memories and global_memories), newest first. Each row carries a derived ` + "`name`" + ` field (first non-empty line of text, ≤ 60 chars, with ` + "`…`" + ` when truncated) so you can present them Serena-style as a named list. Scope: "all" (default — both lists) | "project" | "global". Optional tag filter applies to both lists. Default limit 50 per scope, max 500.`,
	}, HandleListMemories)

	// --- runbook proposer -------------------------------------------
	// Pattern → runbook detector. Walks recent sessions for the
	// resolved project, indexes Bash command sequences that recur
	// across multiple sessions, and offers them as candidate
	// memories. The user (or the AI on their behalf) accepts a
	// candidate via accept_runbook or rejects it via dismiss_runbook.
	mcp.AddTool(srv, &mcp.Tool{
		Name: "propose_runbooks",
		Description: `Surface recurring Bash command sequences in this project as candidate runbooks.

Walks the user's recent Claude/Codex sessions in this project, extracts the shell commands run between consecutive user prompts, normalises them (paths, UUIDs, IPs, timestamps replaced with placeholders), and indexes N-grams that recur across MULTIPLE sessions.

Returns ranked candidates with their occurrence count, distinct-session count, last-seen timestamp, suggested name, and a stable signature. Each candidate is a workflow the user has done at least 3 times across 2+ sessions — strong evidence they'll do it again.

Fire this tool when:
  * The user asks "what should I save as a runbook?" / "what runbooks do you recommend?"
  * You notice the user is about to manually re-run a sequence you've seen them run before.
  * The user wants to clean up repetitive shell workflows.

Returns a markdown rendering plus structured rows. The agent should show the markdown verbatim, then ask before accepting any candidate.`,
	}, HandleProposeRunbooks)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "accept_runbook",
		Description: `Persist a proposed runbook candidate as a project-scoped memory.

Fire this tool ONLY after the user confirms a specific candidate from propose_runbooks. Inputs: the candidate's signature (required), optional name override, optional pre-rendered body, optional extra tags. Writes a row to the same store as remember/record_decision, tagged with "runbook" and "klyne-proposed" so recall can find it again.

Returns the new memory_id. Confirm back to the user.`,
	}, HandleAcceptRunbook)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "dismiss_runbook",
		Description: `Mark a proposed runbook signature so propose_runbooks never re-surfaces it.

Fire when the user explicitly says "no" / "not useful" / "stop suggesting this" to a candidate. Default scope is the current project; pass scope="global" to suppress everywhere. Optional reason is stored for later inspection.`,
	}, HandleDismissRunbook)

	// --- status snapshot --------------------------------------------
	// Portable Markdown summary of klyne's installation state for
	// the current project (or every project on this machine). Useful
	// for weekly review, teammate handoff, or "is the daemon
	// actually doing its job?" introspection.
	mcp.AddTool(srv, &mcp.Tool{
		Name: "status_snapshot",
		Description: `Generate a portable Markdown snapshot of klyne's installation state.

Aggregates the last N hours (default 168 = 7 days) of klyne data: sessions ingested, total messages and tokens, top projects by token volume, recent sessions, /compact events, memory counts, and stop-hook session summaries. Different from generate_handoff (per-task) and bootstrap (Day-1 brief) — this is the per-installation "weekly review" view.

Fire when the user asks "what has klyne been doing?", "what's the state of my klyne install?", "how much have I spent this week?", "give me a klyne weekly review", or similar. Pass all_projects=true for a machine-wide view; otherwise the snapshot scopes to the current project's cwd.

Returns structured rollups plus a markdown body suitable for verbatim display.`,
	}, HandleStatusSnapshot)

	// MCP prompts intentionally NOT registered here. Each tool above
	// already has a paired static slash command under
	// ~/.claude/commands/klyne/<name>.md (installed by
	// InstallSlashCommands). Registering an MCP prompt with the same
	// name caused Claude Code to show two entries in the `/` menu:
	// `/klyne:<name>` (static) and `/klyne:<name> (MCP)` (live).
	// Users saw the same command twice with no idea which to pick.
	// Tools remain auto-discoverable to the AI via the mcp__klyne__*
	// surface — only the duplicate slash-prompt surface is removed.

	return srv
}

// Run starts the MCP server over stdio (the standard transport for
// Claude Code subprocess MCP servers). Blocks until ctx is cancelled
// or the host disconnects.
//
// IMPORTANT: stdout is the JSON-RPC channel — anything written there
// corrupts the protocol. The cobra subcommand explicitly directs all
// log output to stderr; tools must never write to os.Stdout directly.
func Run(ctx context.Context) error {
	return New().Run(ctx, &mcp.StdioTransport{})
}
