-- 003_summaries.sql — rolling session summaries + compact event log.
--
-- W0-frozen contract. Spec §7 (summarizer worker), W15 brief (compact
-- recovery). Changes require a `contract-change` PR.

CREATE TABLE IF NOT EXISTS session_summaries (
    session_id TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,           -- monotonically increasing per session
    text       TEXT    NOT NULL,           -- the summary itself (Markdown)
    model      TEXT    NOT NULL,           -- e.g. "gemini-2.5-flash-lite"
    ts         INTEGER NOT NULL,           -- epoch-ms when generated
    PRIMARY KEY (session_id, version)
);

CREATE INDEX IF NOT EXISTS idx_session_summaries_ts
    ON session_summaries (ts);

-- compact_events records each detected /compact event so W15 can offer
-- "Restore context" recovery and so the cost dashboard can show
-- token-count drops over time.
CREATE TABLE IF NOT EXISTS compact_events (
    session_id         TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    ts                 INTEGER NOT NULL,    -- epoch-ms when /compact fired
    before_token_count INTEGER NOT NULL,    -- session token total before compact
    after_token_count  INTEGER NOT NULL,    -- session token total after compact
    PRIMARY KEY (session_id, ts)
);

CREATE INDEX IF NOT EXISTS idx_compact_events_session
    ON compact_events (session_id, ts DESC);
