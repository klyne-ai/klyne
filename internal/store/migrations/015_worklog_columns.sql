-- 015_worklog_columns.sql — Memory layer of the cross-AI worklog.
--
-- Extends stop_summaries (migration 011) with the columns the
-- worklog writer, recap MCP tools, weekly Markdown export, and
-- the Reflection synthesizer all read. Per the worklog plan
-- (docs/research/worklog/plans/2026-05-17-worklog-end-to-end.md
-- §Task 1) we deliberately re-use stop_summaries instead of
-- introducing a parallel worklog_entries table — see plan
-- §"No new top-level worklog_entries table" for the rationale.
--
-- Column purposes:
--   * recap_visible     1 = surfaced by recap / bootstrap; 0 = suppressed by rules
--   * recap_topic       free-form topic tag for grouping in recap output
--   * ai_drafted_summary  optional AI-rewritten body (deterministic body stays in summary)
--   * draft_state       lifecycle: 'proposed' | 'accepted' | 'dismissed'
--   * signature         dedup key (touched-files + last_bash hash) for suppression
--   * importance        1-10, set at write time via deterministic heuristics
--   * last_accessed_at  epoch-ms; powers recency decay in retrieval
--
-- The two new indexes back the two hottest read paths:
--   * recap_project / bootstrap inject — filter by project + visible, order by ts
--   * suppression check — equality lookup by signature

ALTER TABLE stop_summaries ADD COLUMN recap_visible INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stop_summaries ADD COLUMN recap_topic TEXT;
ALTER TABLE stop_summaries ADD COLUMN ai_drafted_summary TEXT;
ALTER TABLE stop_summaries ADD COLUMN draft_state TEXT NOT NULL DEFAULT 'proposed';
ALTER TABLE stop_summaries ADD COLUMN signature TEXT;
ALTER TABLE stop_summaries ADD COLUMN importance INTEGER NOT NULL DEFAULT 5;
ALTER TABLE stop_summaries ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_stop_summaries_visible
    ON stop_summaries (project_path, recap_visible, ts DESC);
CREATE INDEX IF NOT EXISTS idx_stop_summaries_signature
    ON stop_summaries (signature);
