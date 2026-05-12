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
const version = "v0.4.0"

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

	// Live MCP prompts. Each prompt parallels one of the tools above
	// and surfaces in Claude Code's slash menu as /klyne:<name>
	// (older Claude Code builds used /mcp__klyne__<name>; that form
	// has been retired). Handlers run server-side and return the
	// result as injected user-message content — no AI roundtrip
	// needed for the fetch.
	srv.AddPrompt(&mcp.Prompt{
		Name:        "health",
		Title:       "Context health",
		Description: "Classify the current session's context health and list the top bloat sources. Defaults to the session in the current working directory.",
		Arguments: []*mcp.PromptArgument{
			{Name: "cwd", Description: "Override the working directory used to resolve the session."},
		},
	}, PromptHealthHandler)

	srv.AddPrompt(&mcp.Prompt{
		Name:        "sessions",
		Title:       "List sessions",
		Description: "List every Claude Code and Codex session under the current working directory's project.",
		Arguments: []*mcp.PromptArgument{
			{Name: "cwd", Description: "Override the working directory used to find sessions."},
		},
	}, PromptSessionsHandler)

	srv.AddPrompt(&mcp.Prompt{
		Name:        "handoff",
		Title:       "Generate handoff",
		Description: "Render a deterministic Markdown handoff prompt the user can paste into a fresh session to continue without losing context.",
		Arguments: []*mcp.PromptArgument{
			{Name: "cwd", Description: "Override the working directory used to resolve the session."},
		},
	}, PromptHandoffHandler)

	srv.AddPrompt(&mcp.Prompt{
		Name:        "search",
		Title:       "Search messages",
		Description: "Full-text search across every indexed Claude + Codex session. Pass `query` (required) and optionally `limit`, `sort` (recent | relevance), or `project_path`.",
		Arguments: []*mcp.PromptArgument{
			{Name: "query", Description: "Full-text search query (required)", Required: true},
			{Name: "limit", Description: "Max hits to return (default 10, max 200)"},
			{Name: "sort", Description: "recent (default) | relevance"},
			{Name: "project_path", Description: "Optional client-side filter restricting hits to one repository"},
		},
	}, PromptSearchHandler)

	srv.AddPrompt(&mcp.Prompt{
		Name:        "precompact",
		Title:       "Recover pre-compact context",
		Description: "Recover the conversation that was lost to the last /compact event (Claude) or replacement_history (Codex).",
		Arguments: []*mcp.PromptArgument{
			{Name: "cwd", Description: "Override the working directory used to resolve the session."},
		},
	}, PromptPreCompactHandler)

	// /klyne:tokens — registered with NO arguments. Claude Code's
	// slash UI waits for argument input when an MCP prompt declares
	// optional args, which made the v1 surface fail to fire on
	// bare press-enter. The handler still resolves cwd from the
	// process env, so the no-arg form just works. Users who want a
	// custom window override that via the underlying MCP tool
	// (get_token_timeline) or the `klyne tokens` CLI subcommand.
	srv.AddPrompt(&mcp.Prompt{
		Name:        "tokens",
		Title:       "Token usage timeline",
		Description: "Show the per-turn token usage for the active session: cumulative input tokens, % of model context window, ASCII sparkline. Defaults to the last 5 hours.",
	}, PromptTokenTimelineHandler)

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
