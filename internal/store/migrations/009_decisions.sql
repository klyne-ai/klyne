-- 009_decisions.sql
--
-- Decisions log. A user (or the AI on the user's behalf via the
-- `record_decision` MCP tool) can pin a short note that survives across
-- sessions and is queryable per-project. Use cases:
--
--   * "We picked Postgres over SQLite because the team already runs PG."
--   * "Drop /api/v1; v2 was rolled out 2026-04-12 and v1 had no callers."
--
-- Design:
--   * One row per decision; immutable once written (no UPDATE path).
--     Users delete-then-add if they need to amend.
--   * tags_json stores a JSON array of strings so callers can filter
--     without a join table. v1 expects ≤ 5 tags per row.
--   * session_id is optional — most decisions are project-scoped, not
--     session-scoped. When present we still don't FK to sessions because
--     decisions must outlive session deletion.
--   * project_path is required so the LIST view can scope by project
--     using a single deterministic key.

CREATE TABLE IF NOT EXISTS decisions (
    id           TEXT PRIMARY KEY,                -- UUIDv4 emitted by the writer
    ts           INTEGER NOT NULL,                -- epoch-ms of creation
    project_path TEXT    NOT NULL DEFAULT '',     -- absolute path; '' means "global"
    session_id   TEXT    NOT NULL DEFAULT '',     -- optional, NOT FK'd (see header)
    text         TEXT    NOT NULL,                -- human-readable body
    tags_json    TEXT    NOT NULL DEFAULT '[]'    -- JSON array of strings
);

CREATE INDEX IF NOT EXISTS idx_decisions_project_ts ON decisions(project_path, ts DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_ts          ON decisions(ts DESC);
