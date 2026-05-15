-- 012_shield_snapshots.sql
--
-- Compact Shield snapshot log. One row is written by the UserPromptSubmit
-- advisor at fill ≥70%, capturing enough context for the PreCompact hook
-- to inject scoped intelligence back after a klyne-blocked compact.
--
-- Design:
--   * decisions_json  — JSON array of recent decision summaries.
--   * open_files_json — JSON array of open file paths at snapshot time.
--   * turns_json      — JSON array of last-N turn summaries.
--   * tool_chain_id   — in-flight tool chain id at snapshot time (may be "").
--   * pre_tokens      — token count at snapshot time (from hook payload).
--   * fill_pct        — context fill 0..100 at snapshot time.
--   * blocked         — 1 when this snapshot triggered a block decision.
--   * block_reason    — "shield" | "fold" | "" when no block.
--   * session_id      — Claude Code session id.

CREATE TABLE IF NOT EXISTS shield_snapshots (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,               -- epoch-ms
    session_id      TEXT    NOT NULL DEFAULT '',
    decisions_json  TEXT    NOT NULL DEFAULT '[]',
    open_files_json TEXT    NOT NULL DEFAULT '[]',
    turns_json      TEXT    NOT NULL DEFAULT '[]',
    tool_chain_id   TEXT    NOT NULL DEFAULT '',
    pre_tokens      INTEGER NOT NULL DEFAULT 0,
    fill_pct        REAL    NOT NULL DEFAULT 0,
    blocked         INTEGER NOT NULL DEFAULT 0,     -- bool: 1=blocked
    block_reason    TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_shield_snapshots_ts ON shield_snapshots(ts DESC);
CREATE INDEX IF NOT EXISTS idx_shield_snapshots_session ON shield_snapshots(session_id, ts DESC);
