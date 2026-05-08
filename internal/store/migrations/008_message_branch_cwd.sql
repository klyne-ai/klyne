-- 008_message_branch_cwd.sql
--
-- Per-message git branch + cwd. Used by the cockpit page to disambiguate
-- parallel `claude --resume <id>` invocations that share a sessionId in
-- Claude Code's JSONL — those lines do NOT carry a process/tty marker, so
-- we lean on (gitBranch, cwd) as the next-best discriminator. Most
-- engineers naturally split parallel work across worktrees / subdirs, so
-- this catches the common case.
--
-- Old rows (pre-migration) have empty strings for both columns and will
-- collapse into one tile per session_id, which matches the prior behavior.

ALTER TABLE messages ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN cwd        TEXT NOT NULL DEFAULT '';
