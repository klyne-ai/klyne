-- 017_git_dashboard.sql — AI productivity dashboard persistence (spec §5).
--
-- Two tables ship here. Only data that is *unreconstructable later* is
-- persisted as raw facts; live commit/branch/ship-state is computed at
-- render from git (the source of truth) and is NOT normalized here.
--
-- dashboard_cache is created here but memoization is a perf follow-up —
-- the prototype computes the report live each request.

-- git_session_snapshots — point-in-time branch/HEAD/ahead-behind/dirty at
-- session end. Essential and unreconstructable (the only way to ever know
-- "AI task done but uncommitted at 11:30"). Written by the session-end
-- hook (internal/hooks/sessionend.go, cmd/klyne/session_end.go).
CREATE TABLE IF NOT EXISTS git_session_snapshots (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id       TEXT,
    project_path     TEXT    NOT NULL,
    repo_name        TEXT    NOT NULL,
    worktree_path    TEXT,
    branch           TEXT,
    head_sha         TEXT,
    ahead_count      INTEGER NOT NULL DEFAULT 0,
    behind_count     INTEGER NOT NULL DEFAULT 0,
    dirty_file_count INTEGER NOT NULL DEFAULT 0,
    dirty_files_json TEXT    NOT NULL DEFAULT '[]',
    captured_at      TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_gss_project_time
    ON git_session_snapshots(project_path, captured_at);

-- dashboard_cache — memoized fused+narrated record, keyed by a hash of
-- the invalidation inputs (spec §6.7). Essential for AI cost/latency in
-- production; PROTOTYPE STUB (spec §11): not populated by the prototype
-- (live compute each request).
CREATE TABLE IF NOT EXISTS dashboard_cache (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    day          TEXT    NOT NULL,
    repo_name    TEXT    NOT NULL,
    branch       TEXT,
    cache_key    TEXT    NOT NULL,
    payload_json TEXT    NOT NULL,
    model        TEXT,
    generated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dashcache_key
    ON dashboard_cache(cache_key);
