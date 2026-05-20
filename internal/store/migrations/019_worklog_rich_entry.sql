-- 019_worklog_rich_entry.sql — Rich worklog entry on each stop_summaries row.
--
-- The dashboard's "What was done" narrative used to be re-generated at
-- reflection time from N truncated 200-rune session summaries, which
-- starved the LLM and produced hallucinations (the same session_id
-- cited as evidence under every bullet, vague topic abstractions).
-- Move generation to session-end time when the full transcript is in
-- memory: a hybrid gate (heuristic admit + LLM tiebreak, additive to
-- the existing internal/worklog/suppress.go `ruleSkipReadOnly` filter)
-- decides whether a turn is worklog-worthy, then a writer produces a
-- structured 15-category JSON entry.
--
-- The Stop hook fires PER TURN (composite key (session_id, ts)), so
-- one stop_summaries row = one rich entry. The reflection consumer
-- aggregates all turns for a day/session at read time.
--
-- Following the migration 015 pattern (re-use stop_summaries instead
-- of a parallel worklog_entries table; one parallel write function;
-- selective ON CONFLICT so a later AI prose pass does not clobber the
-- deterministic body): we add three columns and a NEW write function
-- (UpsertStopSummaryWithEntry, ON CONFLICT only updates these three).
--
-- Columns:
--   * worklog_entry_json     full 15-category WorklogEntryJSON; '{}' default
--   * worklog_gate_verdict   audit trail: why a turn has / lacks an entry
--                            ('' = never processed (equivalent to pending);
--                             'pending' = explicitly enqueued, awaiting worker;
--                             'admitted-heuristic' | 'admitted-llm' |
--                             'skipped-heuristic' | 'skipped-llm' |
--                             'skipped-validator' | 'failed-permanent')
--   * worklog_attempts       worker-attempt counter; on attempts >= MAX
--                            the worker sets verdict='failed-permanent' and stops

ALTER TABLE stop_summaries ADD COLUMN worklog_entry_json   TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE stop_summaries ADD COLUMN worklog_gate_verdict TEXT    NOT NULL DEFAULT '';
ALTER TABLE stop_summaries ADD COLUMN worklog_attempts     INTEGER NOT NULL DEFAULT 0;

-- Index backs the worker queue scan: rows still to process, oldest
-- first, with an attempts-cap filter. Also serves the dashboard-side
-- "entries with admitted verdict" filter when rendering the timeline.
CREATE INDEX IF NOT EXISTS idx_stop_summaries_gate_verdict
    ON stop_summaries (worklog_gate_verdict, worklog_attempts, ts DESC);
