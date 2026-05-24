-- 021_daily_productivity_snapshot.sql — deterministic per-day productivity
-- snapshots that back the dashboard's past-day rendering.
--
-- Without snapshots the dashboard live-recomputed git scans + session
-- attribution on every request, so reloading at 4:01pm vs 4:00pm could
-- show different numbers (commits landed in between, FETCH_HEAD changed,
-- a new session opened). Past days never change in reality — once a day
-- is in the past the user expects "yesterday" to read the same forever.
--
-- One row per (project_path, day). day is local-zone YYYY-MM-DD to match
-- the productivity handler's bucketing (productivity.go's defStart uses
-- now.Location()). payload_json is the full single-day
-- internal/productivity.Report payload — the same shape the API returns
-- for a one-day window — so aggregation over a range is a pure read-loop
-- with no live scan needed.
--
-- source distinguishes:
--   'reflection' — written by worklog.recordReflection when a daily
--                  reflection lands. AUTHORITATIVE: the user explicitly
--                  closed the book on this day.
--   'live'       — written by the API handler when it had to recompute
--                  a past day on the fly because no reflection row
--                  existed yet. Lazy backfill; will be overwritten by
--                  a later 'reflection' upsert.
--
-- total_active_minutes is denormalised from the JSON so range queries
-- can sort / filter without parsing every payload.

CREATE TABLE IF NOT EXISTS daily_productivity_snapshot (
    project_path         TEXT    NOT NULL,
    day                  TEXT    NOT NULL,                 -- local YYYY-MM-DD
    payload_json         TEXT    NOT NULL,
    total_active_minutes INTEGER NOT NULL DEFAULT 0,
    source               TEXT    NOT NULL DEFAULT 'live',  -- 'reflection' | 'live'
    created_at           INTEGER NOT NULL,                 -- ms since epoch
    updated_at           INTEGER NOT NULL,                 -- ms since epoch
    PRIMARY KEY (project_path, day)
);

-- Range queries pull every project's snapshot for one day at a time, so
-- a (day) index on top of the PK speeds the per-day fan-out in the
-- handler's aggregate path.
CREATE INDEX IF NOT EXISTS idx_dps_day ON daily_productivity_snapshot(day);
