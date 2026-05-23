# Klyne Dashboard Redesign

_Started 2026-05-23 04:52 UTC_

---

## User

klyne — Project Description

  klyne is a local-first, cross-CLI productivity layer for software engineers who code with AI agents. It plugs into Claude Code
   and OpenAI's Codex CLI, captures every coding session automatically, and turns the raw JSONL transcripts those tools already
  write to disk into a unified worklog, an analytics dashboard, a weekly reflection workflow, and a runbook memory — all running
   as a single Go daemon bound to 127.0.0.1, with a SQLite store under ~/.klyne/ and a Svelte dashboard at
  http://127.0.0.1:7878. No cloud, no telemetry, no API keys.
What it does
  The product is built around five jobs:
  1. Captures every AI session automatically. No SDK, no editor plugin. klyne reads ~/.claude/projects/*.jsonl and
  ~/.codex/sessions/*.jsonl as the CLIs write them.
  2. Lets the AI summarise its own work, inline. Claude Code's SessionStart and UserPromptSubmit hooks inject one instruction
  into your prompt: "end your reply with KLYNE_SUMMARY: <≤100 words> or KLYNE_SUMMARY: skip." The Stop hook regex-extracts that
  line, tags it (commit, PR, decision, migration, security, error-resolved), assigns an importance score 1–10, and writes one
  row to SQLite. Marginal cost: ~130 tokens per turn, billed to the subscription you already pay for. Zero AI subprocesses are
  spawned by the daemon itself — that promise is structural, enforced by tests.
  3. Unifies Claude + Codex in one timeline, one rollup, one weekly reflection. Cross-CLI by design.
  4. Surfaces hidden cost — e.g., subagent token spend invisible to a parent session's cost_usd, attributed back to the parent.
  5. Rescues you when context is lost — /klyne:precompact reads back messages buried by /compact, /klyne:handoff generates a
  deterministic handoff before a fresh chat, and a proactive in-session advisor warns you about stale context, acceleration, or
  the 5-hour cap.

  Who it's for

  The primary audience is the individual engineer who codes with AI day-to-day — Claude Code or Codex CLI users running multiple
   parallel agentic sessions, especially senior engineers and tech leads who need to answer "what did I ship this week, and at
  what cost?" for standups, weekly reviews, or budget reporting. Secondary audiences include AI-first teams that want
  per-developer dashboards without cloud aggregation (team-mode is opt-in on roadmap v0.4), privacy-sensitive engineers who
  refuse to send transcripts to third-party SaaS, and tooling maintainers in the broader productivity-tracker space who can
  adopt klyne's KLYNE_SUMMARY contract to plug in other coding agents (Cursor, Continue, Aider). The license is MIT.



And I want you to redesign this project. If you see the productivity page, it's good, but all the other pages do not look fine. It is currently just looking like someone has made some local project and published it. It should give it a feel that it is a planned project. The design should have the planned structure of it, the data which we are showing, and the navigation part. Everything should be easy to see and look like a professional dashboard. If someone is using it and someone wants to come and see it, that is the design I am not saying we need some fancy design, because it is a product, so information is the first thing. The way to present and navigate between the different pages showing different information should be clear here.

## Assistant

_[tool: list_files]_

