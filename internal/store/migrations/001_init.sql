-- 001_init.sql — initial schema for sessions, messages, threads.
--
-- W0-frozen contract. Spec §7. Changes require a `contract-change` PR.
--
-- Storage convention: every timestamp column is epoch-milliseconds (INTEGER).
-- The PRAGMAs (journal_mode=WAL, foreign_keys=ON, busy_timeout=5000, ...)
-- are applied per-connection by the store layer (W1) — see spec §5.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER NOT NULL PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT    NOT NULL PRIMARY KEY, -- UUID from CLI
    cli          TEXT    NOT NULL,             -- 'claude' | 'codex'
    project_path TEXT    NOT NULL,
    encoded_cwd  TEXT,
    started_at   INTEGER NOT NULL,             -- epoch-ms
    last_msg_at  INTEGER NOT NULL,             -- epoch-ms
    msg_count    INTEGER NOT NULL DEFAULT 0,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    cost_usd     REAL    NOT NULL DEFAULT 0,
    model        TEXT,
    status       TEXT,                         -- 'active' | 'idle' | 'compacted'
    raw_path     TEXT    NOT NULL              -- absolute path to source JSONL
);

CREATE INDEX IF NOT EXISTS idx_sessions_project_last
    ON sessions (project_path, last_msg_at DESC);

CREATE INDEX IF NOT EXISTS idx_sessions_cli_last
    ON sessions (cli, last_msg_at DESC);

CREATE TABLE IF NOT EXISTS messages (
    id          TEXT    NOT NULL PRIMARY KEY,
    session_id  TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    parent_uuid TEXT,
    role        TEXT    NOT NULL,              -- 'user'|'assistant'|'tool'|'system'
    content     TEXT    NOT NULL,
    tool_name   TEXT,
    tokens_in   INTEGER,
    tokens_out  INTEGER,
    cost_usd    REAL,
    model       TEXT,
    ts          INTEGER NOT NULL               -- epoch-ms
);

CREATE INDEX IF NOT EXISTS idx_messages_session_ts
    ON messages (session_id, ts);

CREATE INDEX IF NOT EXISTS idx_messages_ts
    ON messages (ts);

-- Threads (v1.1 surface; declared in v1 to avoid migration churn — spec §7).
CREATE TABLE IF NOT EXISTS threads (
    id           TEXT    NOT NULL PRIMARY KEY,
    title        TEXT,
    project_path TEXT,
    first_ts     INTEGER,
    last_ts      INTEGER
);

CREATE TABLE IF NOT EXISTS thread_sessions (
    thread_id  TEXT NOT NULL REFERENCES threads(id)  ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    PRIMARY KEY (thread_id, session_id)
);
