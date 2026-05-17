-- 010_runbook_dismissals.sql
--
-- Runbook-proposal dismissals. When `klyne runbooks` proposes a
-- recurring command sequence and the user (via CLI or via MCP)
-- rejects it, we record the normalized signature here so the
-- detector never re-proposes the same shape.
--
-- Design:
--   * signature is the normalized N-gram joined by ASCII " ; "
--     so duplicates collapse byte-for-byte. The proposer hashes
--     it for display id but stores the raw signature for diffing.
--   * project_path scopes the dismissal — a sequence dismissed
--     in repo A can still be proposed in repo B because the
--     user's intent there may be different.
--   * No ts-DESC index needed; the detector reads with WHERE
--     project_path = ? which alone is selective enough.

CREATE TABLE IF NOT EXISTS runbook_dismissals (
    signature    TEXT NOT NULL,                  -- normalized N-gram
    project_path TEXT NOT NULL DEFAULT '',       -- absolute project path
    ts           INTEGER NOT NULL,               -- epoch-ms of dismissal
    reason       TEXT NOT NULL DEFAULT '',       -- optional free-form note
    PRIMARY KEY (signature, project_path)
);

CREATE INDEX IF NOT EXISTS idx_runbook_dismissals_project
    ON runbook_dismissals (project_path);
