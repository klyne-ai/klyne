-- 020_reflection_stop_summary_cursor.sql — iterative reflection (docs/features/iterative-reflection.md).
--
-- Each /klyne:reflect run covers only the stop_summaries that arrived
-- since the previous run for the same (project_path, day). This column
-- carries the cursor on each reflection row: "I covered stop_summaries
-- with ts <= this value." The next run reads MAX(stop_summary_cursor_ts)
-- for the day and asks for summaries strictly after that point.
--
-- NULL means the row was written before the cursor was introduced — it
-- is treated as "covers everything up through the row's own ts."

ALTER TABLE worklog_reflections ADD COLUMN stop_summary_cursor_ts INTEGER;
