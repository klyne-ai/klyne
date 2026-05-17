-- 011_stop_summaries.sql
--
-- Stop-hook session summaries. Written by `klyne session-end`
-- (registered as a Claude Code Stop hook by `klyne mcp install`)
-- so a SessionStart in the same project can recall what the
-- previous session actually did — without any AI call.
--
-- Why a separate table from session_summaries:
--   * session_summaries has an FK to sessions(id). The Stop hook
--     fires before the daemon ingestor may have created that row,
--     so a FK constraint would race the writes.
--   * session_summaries is reserved for the daemon's AI summarizer
--     (model='gemini-2.5-flash-lite' etc). The Stop hook is purely
--     deterministic. Keeping the surfaces separate avoids cross-
--     contamination of the data model and the source-of-truth.
--
-- Design:
--   * project_path is the resolved cwd at session end; required so
--     bootstrap / recall can scope by project without a join.
--   * one row per (session_id, ts). The hook fires at most once per
--     session-end by Claude Code's contract, but re-runs with the
--     same session_id are allowed — composite key lets us keep a
--     short history if the user reopens and re-closes.
--   * summary is opaque markdown body; consumers render verbatim.

CREATE TABLE IF NOT EXISTS stop_summaries (
    session_id   TEXT    NOT NULL,
    ts           INTEGER NOT NULL,
    project_path TEXT    NOT NULL DEFAULT '',
    cli          TEXT    NOT NULL DEFAULT 'claude',
    summary      TEXT    NOT NULL,
    last_user    TEXT    NOT NULL DEFAULT '',
    last_bash    TEXT    NOT NULL DEFAULT '',
    files_json   TEXT    NOT NULL DEFAULT '[]',
    PRIMARY KEY (session_id, ts)
);

CREATE INDEX IF NOT EXISTS idx_stop_summaries_project_ts
    ON stop_summaries (project_path, ts DESC);

CREATE INDEX IF NOT EXISTS idx_stop_summaries_ts
    ON stop_summaries (ts DESC);
