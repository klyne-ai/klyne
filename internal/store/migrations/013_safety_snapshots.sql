-- 013_safety_snapshots.sql
--
-- Safety-net snapshot log. One row is written for every risky command
-- that klyne's PreToolUse hook intercepts, capturing enough metadata
-- for `klyne restore` to undo the operation.
--
-- Design:
--   * stash_sha    — git stash create SHA for git repos; empty otherwise.
--   * fallback_dir — cp-r destination path for non-git fallback; empty otherwise.
--   * file_count   — best-effort count of captured files (0 when unknown).
--   * pattern_id   — id from policy/risky_commands.json (e.g. "git-reset-hard").
--   * severity     — "medium" | "high" | "critical".
--   * command      — the raw shell command that triggered the match.
--   * cwd          — working directory at hook-fire time.
--   * session_id   — Claude Code session id (from hook payload).

CREATE TABLE IF NOT EXISTS safety_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,               -- epoch-ms
    session_id   TEXT    NOT NULL DEFAULT '',
    cwd          TEXT    NOT NULL DEFAULT '',
    command      TEXT    NOT NULL,
    pattern_id   TEXT    NOT NULL,
    severity     TEXT    NOT NULL,
    stash_sha    TEXT    NOT NULL DEFAULT '',
    fallback_dir TEXT    NOT NULL DEFAULT '',
    file_count   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_safety_snapshots_ts ON safety_snapshots(ts DESC);
CREATE INDEX IF NOT EXISTS idx_safety_snapshots_session ON safety_snapshots(session_id, ts DESC);
