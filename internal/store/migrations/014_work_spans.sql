-- 014_work_spans.sql
--
-- Work-span attribution table. One row per outcome unit — a sequence of
-- messages between a session start (or last close event) and a git commit,
-- a GitHub PR creation (v1), or an open/exploration bucket (session end
-- without a close event).
--
-- Design:
--   * commit_sha      — git SHA that closed this span; empty for exploration.
--   * pr_number       — PR number that closed this span (v1); 0 for v0.
--   * exploration_id  — autogen identifier for spans that never reached a commit.
--   * bucket          — "commit" | "exploration" (pr_merge added in v1).
--   * project_path    — working directory for this span (from messages.cwd).
--   * git_branch      — git branch for this span (from messages.git_branch).
--   * session_ids_json — JSON array of session IDs whose messages contributed.
--   * decision_ids_json — JSON array of decision IDs attached during the span window.
--   * waste_classes_json — JSON array of detected waste class strings.
--   * waste_meta_json — JSON object with per-class metadata (e.g. WASTE_LOOP hash + count).
--   * tokens_fresh    — fresh (non-cached) prompt tokens consumed in this span.
--   * tokens_cache_read — cache-read tokens consumed in this span.
--   * tokens_cache_write — cache-write tokens consumed in this span.
--   * tokens_out      — completion tokens consumed in this span.
--   * msg_count       — number of messages contributing to this span.
--   * opened_at       — epoch-ms of the first message in the span.
--   * closed_at       — epoch-ms of the close event (commit ts or last-msg ts).
--
-- Cost USD is NOT stored — it is a display-layer computation applied at
-- query time using internal/cost/pricing.go rates (per migration 007
-- design intent: cost_usd removed from the hot path for flat-plan users).

CREATE TABLE IF NOT EXISTS work_spans (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    commit_sha          TEXT    NOT NULL DEFAULT '',
    pr_number           INTEGER NOT NULL DEFAULT 0,
    exploration_id      TEXT    NOT NULL DEFAULT '',
    bucket              TEXT    NOT NULL,               -- "commit" | "exploration"
    project_path        TEXT    NOT NULL DEFAULT '',
    git_branch          TEXT    NOT NULL DEFAULT '',
    session_ids_json    TEXT    NOT NULL DEFAULT '[]',
    decision_ids_json   TEXT    NOT NULL DEFAULT '[]',
    waste_classes_json  TEXT    NOT NULL DEFAULT '[]',
    waste_meta_json     TEXT    NOT NULL DEFAULT '{}',
    tokens_fresh        INTEGER NOT NULL DEFAULT 0,
    tokens_cache_read   INTEGER NOT NULL DEFAULT 0,
    tokens_cache_write  INTEGER NOT NULL DEFAULT 0,
    tokens_out          INTEGER NOT NULL DEFAULT 0,
    msg_count           INTEGER NOT NULL DEFAULT 0,
    opened_at           INTEGER NOT NULL,               -- epoch-ms
    closed_at           INTEGER NOT NULL                -- epoch-ms
);

-- digest query: spans in a project/time window, ordered chronologically.
CREATE INDEX IF NOT EXISTS idx_work_spans_project_opened
    ON work_spans(project_path, opened_at DESC);

-- attribution lookup: find span for a given commit.
CREATE INDEX IF NOT EXISTS idx_work_spans_commit_sha
    ON work_spans(commit_sha)
    WHERE commit_sha != '';

-- v1 PR-close lookup (column exists from v0 schema).
CREATE INDEX IF NOT EXISTS idx_work_spans_pr_number
    ON work_spans(pr_number)
    WHERE pr_number != 0;
