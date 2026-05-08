-- 006_deleted_sessions.sql
--
-- Tombstone table for sessions the user has explicitly deleted from the UI.
-- Connectors re-read the full source JSONL on every daemon restart, so without
-- a persistent tombstone any deletion would be undone on the next start.
--
-- The writer (internal/app/app.go) consults this table before upserting a
-- session/message; matching ids are dropped on the floor.
--
-- A user can resurrect a tombstoned session by removing the row manually
-- (sqlite3 ~/.agentdeck/agentdeck.db 'DELETE FROM deleted_sessions WHERE id=?;')
-- and restarting the daemon — the connector will warm-replay it.

CREATE TABLE IF NOT EXISTS deleted_sessions (
    id          TEXT    PRIMARY KEY,
    deleted_at  INTEGER NOT NULL  -- epoch-ms
) WITHOUT ROWID;
