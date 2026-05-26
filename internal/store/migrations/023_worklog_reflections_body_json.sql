-- 023_worklog_reflections_body_json.sql — typed What-was-done payload.
--
-- The dashboard's "What was done" panel moves from a flat-bullet body_md
-- rendering to a typed two-tier card. The Tier 2 detail list is stored
-- canonically as JSON so:
--   * Tier 1 (the headline row) is computed deterministically in Go
--     over the merged details across every reflection row for a
--     (project, day) — no LLM at render, no drift across views.
--   * The composer (internal/productivity.ComposeWWD) and the writer
--     (the /klyne:reflect MCP slash command via record_reflection) share
--     ONE schema documented in docs/plan/2026-05-26-wwd-typed-cards.md
--     §1.1.
--
-- body_json is NULLable: legacy rows written before this migration —
-- and the prose fallback path the MCP tool keeps for back-compat — keep
-- body_md only. New typed rows carry BOTH body_json (canonical) and
-- body_md (a deterministic markdown rendering of the JSON, for grep /
-- legacy readers).

ALTER TABLE worklog_reflections ADD COLUMN body_json TEXT;
