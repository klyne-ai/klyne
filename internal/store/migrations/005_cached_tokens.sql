-- 005_cached_tokens.sql — track cached prompt-token subsets so the cost
-- engine can apply differentiated rates (fresh vs cache_read vs
-- cache_write).  Without these columns, Claude sessions undercount
-- prompt tokens (the parser dropped the cached portions) and Codex
-- sessions overbill (cache hits were billed at the full prompt rate).
--
-- Semantics:
--   tokens_in = fresh + cached_read + cached_write
--   fresh     = tokens_in - cached_read_tokens - cached_write_tokens
--
-- For Codex, cached_write_tokens is always 0 (OpenAI does not expose a
-- comparable value).  The session-level columns mirror the message-level
-- columns and are accumulated by InsertMessage in the same transaction.
--
-- SQLite supports ADD COLUMN with a NOT NULL DEFAULT 0 without a full
-- table rewrite, so this migration is fast even on large databases.

ALTER TABLE messages ADD COLUMN cached_read_tokens  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN cached_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN cached_read_tokens  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN cached_write_tokens INTEGER NOT NULL DEFAULT 0;
