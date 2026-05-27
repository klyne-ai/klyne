-- 024_klyne_llm_usage.sql — per-run token usage for the two klyne-spawned
-- `claude` subprocesses (productivity-sync, reflect).
--
-- WHY: users want transparency that klyne's own LLM footprint is a small
-- slice of their daily Claude usage, NOT the bulk. The productivity
-- dashboard renders a tile reading "Klyne: <total_tokens> · <share>% of
-- today's Claude usage" with the denominator coming from a SUM over
-- messages.ts for the local day. Tokens only — no USD anywhere by design.
--
-- One row per subprocess invocation; aggregation is done on read so we
-- never lose the per-run breakdown by operation (productivity_sync vs
-- reflect) or by service.
--
-- Source of the token figures: claude CLI's `--output-format=json` emits
-- a single JSON object whose `usage` block carries input / output /
-- cache_creation_input / cache_read_input tokens. The daemon parses that
-- object in the subprocess handler and inserts one row here.

CREATE TABLE IF NOT EXISTS klyne_llm_usage (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    project_path        TEXT    NOT NULL,
    day                 TEXT    NOT NULL,                  -- local YYYY-MM-DD
    operation           TEXT    NOT NULL,                  -- 'productivity_sync' | 'reflect'
    model               TEXT    NOT NULL DEFAULT '',
    input_tokens        INTEGER NOT NULL DEFAULT 0,
    output_tokens       INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens  INTEGER NOT NULL DEFAULT 0,
    duration_ms         INTEGER NOT NULL DEFAULT 0,
    num_turns           INTEGER NOT NULL DEFAULT 0,
    session_id          TEXT    NOT NULL DEFAULT '',
    status              TEXT    NOT NULL DEFAULT 'ok',     -- 'ok' | 'error' | 'timeout'
    created_at          INTEGER NOT NULL                   -- ms since epoch (UTC)
);

CREATE INDEX IF NOT EXISTS idx_klyne_llm_usage_day
    ON klyne_llm_usage (day);

CREATE INDEX IF NOT EXISTS idx_klyne_llm_usage_project_day
    ON klyne_llm_usage (project_path, day);
