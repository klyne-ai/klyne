-- 016_worklog_reflections.sql — Reflection layer (Generative Agents pattern).
CREATE TABLE IF NOT EXISTS worklog_reflections (
    id              TEXT    PRIMARY KEY,
    ts              INTEGER NOT NULL,
    project_path    TEXT    NOT NULL DEFAULT '',
    tier            INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    body_md         TEXT    NOT NULL,
    evidence_entry_ids_json    TEXT NOT NULL DEFAULT '[]',
    evidence_reflection_ids_json TEXT NOT NULL DEFAULT '[]',
    importance      INTEGER NOT NULL DEFAULT 5,
    summary_source  TEXT    NOT NULL DEFAULT 'ai',
    state           TEXT    NOT NULL DEFAULT 'proposed',
    state_changed_at INTEGER NOT NULL,
    CHECK (length(evidence_entry_ids_json) > 2)  -- enforces citation invariant: '[]' is rejected
);
CREATE INDEX IF NOT EXISTS idx_worklog_reflections_project_ts
    ON worklog_reflections (project_path, ts DESC);
CREATE INDEX IF NOT EXISTS idx_worklog_reflections_tier
    ON worklog_reflections (tier);
