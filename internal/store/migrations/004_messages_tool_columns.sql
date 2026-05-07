-- 004_messages_tool_columns.sql — add JSON-encoded tool columns to messages.
--
-- Decision: Option A (W1-INTEGRATION-NOTES.md) — JSON-encode ToolCalls and
-- ToolResults slices into text columns.  This keeps the schema single-table
-- and trivially queryable while remaining opaque to FTS (FTS still indexes
-- only messages.content, which is correct — users search message text, not
-- tool-call structure).
--
-- The legacy tool_name column stays for backwards-compat; new code populates
-- tool_calls_json and tool_results_json instead.
--
-- SQLite supports ADD COLUMN with a NOT NULL DEFAULT '' without a full
-- table rewrite, so this migration is fast even on large databases.

ALTER TABLE messages ADD COLUMN tool_calls_json   TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN tool_results_json  TEXT NOT NULL DEFAULT '';
