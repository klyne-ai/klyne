# klyne positioning: competitor comparison and launch gaps

Review date: 2026-05-07

## Short positioning

klyne should not be positioned as a generic "AI usage dashboard." That
market is already crowded. The sharper wedge is:

> Local mission control for Claude Code and Codex power users: see every active
> coding session, recover old context, resume from the right project, and know
> when to compact before a long thread burns through your quota.

This makes klyne a workflow/recovery tool first, with analytics as support.

## Competitor comparison

| Tool | Primary job | Supported tools | Current strengths | Where klyne can win | Risk for klyne |
|---|---|---:|---|---|---|
| klyne | Local cockpit for Claude Code + Codex sessions, search, resume, compact recovery, token/context savings. | 2 today: Claude Code, Codex. | Local-first Go daemon, reads existing JSONL logs, session/project views, live SSE updates, FTS search, copy-safe resume command, cockpit (1 tile per running session), `/compact` detection + rolling AI summaries, live OAuth/JSONL-snapshot rate-limit badge, per-session context-fill + cost-per-turn indicator with compact/restart savings deltas, on-demand AI break advisor (continue / compact / start fresh). | Own the "I have 10 parallel AI coding terminals and need to recover/resume safely" problem. Make `/compact` timing and recovery the killer feature, with cost-forensics (per-turn waterfall, tool-bloat scorecard) as the second moat. | Launch README still says pre-alpha; no release/tag; CI/checks not launch-green; fewer connectors than competitors; cost-forensics differentiators not yet shipped. |
| [Agentlytics](https://agentlytics.io/) | Unified analytics dashboard for AI coding agents. | Claims 16 editors/agents including Cursor, Windsurf, Antigravity, Claude Code, VS Code, Zed, OpenCode, Codex, Gemini CLI, Copilot CLI, Goose, Kiro, Command Code. | One-command `npx agentlytics`, broad connector coverage, local dashboard, costs, session browser, projects, compare page, subscriptions/rate-limit overview, team relay/MCP sharing. | Avoid competing head-on on "number of connectors" or generic analytics. Focus on live operational cockpit, resume, compact decisioning, and recovery flows. | Very direct competitor for dashboards, search, projects, costs, and local-first story. Already around the 500-star benchmark. |
| [OpenUsage](https://github.com/robinebers/openusage) | Menu-bar subscription/quota tracker for AI coding tools. | Broad provider list: Claude, Codex, Cursor, Copilot, Gemini, Windsurf, Kiro, OpenCode, Antigravity, and more. | Polished desktop utility, release downloads, auto-updates, plugin-based provider model, local HTTP API, quota/progress display. | klyne is not just "how much quota is left"; it should show what work is happening, which session to resume, and how to recover context. | If klyne's launch copy says "track usage/quota," OpenUsage looks more mature and broader. |
| [CliDeck](https://docs.clideck.dev/) | Browser terminal dashboard for running multiple AI coding agents. | Claude Code, Codex, Gemini CLI, OpenCode, custom agents/shells. | Real PTY panels, live status, notifications, session resume, mobile remote, roles, autopilot routing, plugin API. | klyne can stay read-only and safer: no terminal wrapping, no workflow interception, no sending messages through the app. Stronger for post-hoc history, search, recovery, and context cost visibility. | CliDeck owns the "run many agents from one browser tab" message. klyne must not sound like a weaker terminal dashboard. |

## Feature matrix

| Feature / user problem | klyne now/planned | Agentlytics | OpenUsage | CliDeck |
|---|---|---|---|---|
| Local-first, no cloud account | Yes | Yes | Yes | Yes |
| Reads existing Claude/Codex sessions without changing workflow | Yes | Yes | Partial: usage/quota focus | No/partial: wraps new live PTY sessions |
| Broad connector coverage | Weak: Claude + Codex only | Strong | Strong | Medium |
| Project-level session organization | Yes, in UI work | Yes | No, not core | Yes |
| Full-text search across coding conversations | Yes | Yes | No, not core | Search/filter, but terminal-session oriented |
| Live active-session cockpit | Yes, differentiator | Analytics-oriented | No | Yes, but for sessions it runs/wraps |
| Copy-safe resume command from original project path | Yes | Unknown/not core | No | Yes |
| `/compact` detection + rolling AI summary | Yes, shipped | Not core | No | No |
| Context-fill bar + next-turn quota burn (per session) | Yes, shipped | Quota/cost analytics, less workflow-specific | Strong quota tracking, but global not per-session | Telemetry/status, not compact economics |
| Compact-vs-restart savings delta (concrete % of 5h limit) | Yes, shipped | No | No | No |
| AI recommendation: continue vs compact vs start fresh | Yes, shipped | Not core | No | Autopilot routing, different use case |
| Live OAuth-driven 5h/7d utilization (Claude) | Yes, shipped | Cost analytics | Yes, core | Status/telemetry |
| Per-turn cost waterfall + spike forensics | Yes, shipped (`klyne tokens`) | No | No | No |
| Tool-call bloat scorecard ("which tool ate your context") | Yes, shipped (`get_context_health` bloat) | No | No | No |
| Budget projection + soft alerts | Yes, shipped (proactive advisor hook) | Cost analytics | Quota glance, not projection | No |
| Team sharing / MCP over history | Not currently | Yes | Local HTTP API, not team history | Plugin/API focus |
| Menu-bar / tray quota glance | No | No | Yes, core | No |
| Real terminal panels / send messages | No, intentionally read-only | No | No | Yes, core |
| Mobile remote | No | No | No | Yes |
| Plugin ecosystem | No | Connector breadth exists | Provider plugin architecture | Plugin API |
| Demo mode/sample dataset | Missing | Appears easy via one-command scan only | N/A | Docs/product screenshots |
| Release binaries / easy install | Missing | `npx` | Desktop releases | `npm install -g` |

## Real user problems klyne should cover

These are real problems observed from the repo, current competitor messaging,
and public community posts about Claude/Codex usage. They are inside
klyne's natural scope.

| User problem | Why it matters | klyne answer | Current gap |
|---|---|---|---|
| "I have many Claude/Codex sessions and cannot tell what is active." | Heavy users run parallel terminals and lose track of which agent is still working or waiting. | Cockpit view with live/idle tiles, SSE updates, latest preview, project/branch grouping. | Needs polish, screenshots, and stress testing with many simultaneous sessions. |
| "I found an old session but cannot resume it correctly." | `claude --resume <id>` can fail or resume wrong context if run from the wrong directory. | Copy a full resume command with `cd '<project_path>' && claude/codex --resume <id>`. | Needs strong README/demo callout because this is a high-value practical fix. |
| "I do not know when a long session is wasting quota." | Long sessions resend large context and silently burn 5-hour caps or spend. | Context-fill bar + next-turn quota projection + compact/restart savings deltas — see [docs/features/token-savings.md](../features/token-savings.md). | Needs validation on real Claude/Codex logs and clear fallback when calibration is unavailable (already returns `-1` sentinel). |
| "I am scared to `/compact` because I may lose important context." | Users delay compaction until the thread becomes slow or rate-limited. | Compact CTA with concrete savings delta + Restore-context bundle (rolling AI summary + paste-ready resume command) + AI break advisor. | Need end-to-end compact recovery demo and explicit explanation of what is preserved (planned: pre/post-compact semantic diff). |
| "I finished a task but keep using the same thread out of habit." | Continuing stale context makes future turns expensive and lower quality. | AI break advisor: continue / compact / start fresh with one-sentence reason — see [docs/features/token-savings.md](../features/token-savings.md). | Shipped click-only with 10-min server cache; needs onboarding copy for users without an AI provider configured. |
| "Why did this session cost so much?" | After a long thread the user sees a $-figure in the cost summary but cannot tell which turn drove it. | Per-turn cost waterfall on the session detail page + spike detector (turns >2× session avg flagged inline). | Planned. Pure aggregation over existing per-message cost rows; no new ingestion. |
| "What is eating my context window?" | Big tool outputs (file reads, bash output) silently bloat context and inflate every subsequent turn. | Per-session tool-call bloat scorecard: which tools returned the most input tokens, top files re-read, cache-vs-fresh ratio. | Planned. `tool_calls`/`tool_results` rows already stored; UI + aggregation only. |
| "Am I trending toward a costly month?" | Cost summary shows today's spend, but no projection or alert. | Budget pane on the dashboard: rolling 7d/30d trend, projected month-end spend, optional desktop notification when today is N× the average. | Planned. Reuses existing cost rows. |
| "I remember a decision from last week but not which AI session had it." | Session history is spread across projects and CLIs. | FTS search over messages with session/project drill-down. | Needs search snippets, filters, and maybe saved/recent searches for launch polish. |
| "I want to understand AI usage per project, not just per account." | Project-level cost/activity maps to engineering work better than raw global quota. | Project dashboard, per-project sessions, messages, tokens, model mix. | Add a server-side `/projects` endpoint or make current client grouping robust at scale. |
| "Claude and Codex histories are separate, but my work is one project." | Users switch tools in the same repo and lose continuity. | Cross-CLI project view and search. | Current connector count is fine for this wedge; do not over-expand before launch. |
| "I need proof this is local and safe." | Users are cautious about tools reading coding transcripts. | Read-only local daemon, local SQLite, no cloud account. | Missing dedicated `SECURITY.md` / privacy page and source-file read guarantees in README. |
| "I want to try it before giving it my real logs." | Launch visitors bounce if they cannot see value quickly. | Sample JSONL fixtures exist in `examples/sample-jsonl`. | Missing demo mode command and polished seeded demo dataset. |

## Recently shipped (May 2026)

These items were "planned/core" in earlier drafts of this doc and are now in
`main`. Marketing copy and screenshots should reflect them as shipped.

- **Per-session token-savings indicator** — context-fill bar, next-turn cost
  as % of 5h limit, compact/restart savings deltas, calibrated from
  `/api/oauth/usage` (Claude) or the Codex JSONL `token_count` snapshot.
  See [docs/features/token-savings.md](../features/token-savings.md).
- **AI break advisor** — on-demand `start_fresh` / `compact` / `continue`
  verdict from a Haiku-class model on the last 10 messages, server-cached
  10 min per session.
- **Cockpit simplification** — one tile per running session (was: one tile
  per `(session_id, git_branch, cwd)` tuple). Branch is now informational
  metadata on the tile instead of a separator.
- **Multi-CLI cost parity** — cached read/write tokens tracked separately
  for both Claude and Codex; cost engine handles per-token-class pricing.
- **Project view** — multi-CLI tabs with per-session badges so a project
  with both Claude and Codex sessions shows both.

## Missing launch-critical features in klyne scope

Priority order for a credible public launch:

1. ~~**Launch README**~~ **SHIPPED.** README rewritten with real examples, install steps, and screenshots.

2. **Demo mode** — still missing. `klyne demo` / `klyne start --demo` with bundled sample JSONL.

3. **Release/install path** — still missing. Need GitHub Releases with macOS/Linux binaries + `go install`.

4. ~~**Privacy/security documentation**~~ **SHIPPED.** See `docs/SECURITY.md` — every claim cites code paths.

5. ~~**Context savings proof**~~ **SHIPPED.** `klyne tokens` + proactive advisor + `docs/proof/`.

6. ~~**Compact recovery story**~~ **SHIPPED.** Pre-compact recovery, handoff, and advisor all live.

7. **Project rollup API**  
   Add a backend `/projects` endpoint instead of relying only on client-side
   grouping. This improves startup performance and makes the project view more
   credible for users with hundreds or thousands of sessions.

8. **Export session**  
   Add Markdown/JSON export from a session or project. This solves a real
   archival/share problem and fits the read-only local-history scope.

9. **Connector status diagnostics**  
   `klyne doctor` should clearly say which roots were found, how many files
   were parsed, which sessions failed to parse, and what to do next.

10. **Polished empty/error states**  
    Users with no Codex logs, no Claude logs, missing permissions, or unknown
    model pricing need specific next steps instead of blank dashboards.

## Post-launch differentiators (the second moat)

These are the features that turn klyne from "another usage dashboard" into
"the cost-forensics tool for AI coding." None of the three competitors above
ship them today; together they make the cost-visibility wedge defensible.

1. ~~**Per-session cost waterfall + spike detector**~~ **SHIPPED.** `klyne tokens` renders per-turn timeline with cached/uncached split. `/klyne:tokens` slash command in Claude Code.

2. ~~**Tool-call bloat scorecard**~~ **SHIPPED.** `get_context_health` returns top-5 bloat sources with per-file Read/Edit attribution. `/klyne:health` slash command.

3. ~~**Budget projection + soft alerts**~~ **SHIPPED.** Proactive advisor hook with 4 deterministic triggers (stale-context, acceleration, 5-hour-window, hard ceiling). Fires inline in Claude Code.

4. **Compaction trust signal**
   When `/compact` runs, retain the pre-image and run a small AI semantic
   diff: "you preserved decisions A and B; you lost the file-listing for
   directory X (low signal)." Removes the #1 reason users delay
   compaction. Builds directly on the rolling-summary infrastructure
   already shipped.

5. **AI-tagged session topics + filters**
   On insert (or batch-backfill) tag each session with a short topic label
   ("refactor cost engine", "fix login flow"). Surface as a filter on the
   project / search views. Reuses the existing `TaskTitle` selector.

These are listed in priority order — #1 and #2 are the shortest path to
"klyne shows you something nothing else does."

## Good later features, but not needed for first launch

- More connectors: Gemini CLI, OpenCode, Cursor, Windsurf.
- Menu-bar/tray quota widget.
- MCP server over local history.
- Team relay/sharing.
- Plugin API for third-party parsers.
- Mobile remote.
- Agent routing/autopilot.

These are valid directions, but they create direct competition with stronger
incumbents. For the first 500-star goal, the better path is to own the
Claude/Codex recovery + compact-savings workflow extremely well.

## Sources checked

- klyne local repo: README, UI routes/components, feature docs, API/contracts.
- [Agentlytics public site](https://agentlytics.io/)
- [Agentlytics GitHub](https://github.com/f/agentlytics)
- [OpenUsage GitHub](https://github.com/robinebers/openusage)
- [CliDeck docs](https://docs.clideck.dev/)
- [CliDeck GitHub](https://github.com/rustykuntz/clideck)
