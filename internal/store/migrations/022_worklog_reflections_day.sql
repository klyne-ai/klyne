-- 022_worklog_reflections_day.sql — store the COVERED day, not the write day.
--
-- Until this migration, the day a reflection covered was only embedded in
-- the title ("Daily reflection — YYYY-MM-DD") and the id prefix. The
-- productivity dashboard's per-day reflection lookup
-- (ListReflectionsForProjectDay) filtered by
-- `date(ts/1000, 'unixepoch', 'localtime')` — the row's WRITE day. When
-- /klyne:reflect did a catch-up run today for two pending past days, both
-- new rows landed with today's `ts` and surfaced under today's view even
-- though their content reflected on May 24 / May 25.
--
-- Fix: add an explicit `day` column (local YYYY-MM-DD, matching the
-- dashboard's local-zone bucketing) and route both the write path and
-- the per-day reads through it. Existing rows are backfilled from the
-- title's trailing "YYYY-MM-DD" when it parses, and from the legacy
-- ts→local-date when it doesn't (preserving the old behaviour for any
-- row that didn't carry a daily-format title — weekly/quarterly tiers).

ALTER TABLE worklog_reflections ADD COLUMN day TEXT NOT NULL DEFAULT '';

-- Daily-titled rows: pull "YYYY-MM-DD" off the title tail.
UPDATE worklog_reflections
SET day = substr(title, -10)
WHERE day = ''
  AND substr(title, -10) GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]';

-- Anything else (legacy / non-daily): fall back to the row's local write
-- day so the new index is fully populated.
UPDATE worklog_reflections
SET day = date(ts/1000, 'unixepoch', 'localtime')
WHERE day = '';

CREATE INDEX IF NOT EXISTS idx_worklog_reflections_project_day
    ON worklog_reflections (project_path, day);
