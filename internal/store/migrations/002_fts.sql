-- 002_fts.sql — FTS5 virtual table over messages.content with BM25 ranking.
--
-- W0-frozen contract. Spec §5 (FTS5 + BM25), §7 (search step). Changes
-- require a `contract-change` PR.
--
-- Notes:
--   * `content='messages'` makes this a contentless FTS table that mirrors
--     the messages table; the rowid columns must align.
--   * UNINDEXED columns (role, session_id, ts) are stored but not tokenized
--     — they let the search query filter by metadata without joining back.
--   * Triggers below keep messages_fts in sync on INSERT / UPDATE / DELETE.

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    role       UNINDEXED,
    session_id UNINDEXED,
    ts         UNINDEXED,
    content='messages',
    content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS messages_ai
AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts (rowid, content, role, session_id, ts)
    VALUES (new.rowid, new.content, new.role, new.session_id, new.ts);
END;

CREATE TRIGGER IF NOT EXISTS messages_ad
AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts (messages_fts, rowid, content, role, session_id, ts)
    VALUES ('delete', old.rowid, old.content, old.role, old.session_id, old.ts);
END;

CREATE TRIGGER IF NOT EXISTS messages_au
AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts (messages_fts, rowid, content, role, session_id, ts)
    VALUES ('delete', old.rowid, old.content, old.role, old.session_id, old.ts);
    INSERT INTO messages_fts (rowid, content, role, session_id, ts)
    VALUES (new.rowid, new.content, new.role, new.session_id, new.ts);
END;
