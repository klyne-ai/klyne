-- 018_github_pr_cache.sql — productivity dashboard merged-PR cache.
--
-- The dashboard's "PRs merged" tile is a Layer-2 GitHub enrichment: the
-- API handler shells out to `gh pr list` (internal/api/handlers/
-- productivity_github.go). To avoid hitting GitHub on every dashboard
-- load, each (repo, window) result is cached here with its fetch time.
-- The handler re-fetches only when the row is older than the configured
-- TTL (default 2h, overridable via KLYNE_PR_CACHE_TTL_MIN); on a failed
-- refresh the stale row is still served rather than nothing.
--
-- This is a pure cache — safe to delete; it is rebuilt on next load.
CREATE TABLE IF NOT EXISTS github_pr_cache (
    slug         TEXT    NOT NULL,            -- GitHub "owner/name"
    window_key   TEXT    NOT NULL,            -- merged-range key, e.g. "2026-05-18..2026-05-21"
    payload_json TEXT    NOT NULL DEFAULT '[]', -- JSON array of productivity.MergedPR
    fetched_at   INTEGER NOT NULL,            -- epoch-ms of the gh fetch
    PRIMARY KEY (slug, window_key)
);
