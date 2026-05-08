-- 007_zero_costs.sql
--
-- The cost engine has been removed from the writer hot path. Existing rows
-- still carry historical USD aggregates from when the engine ran on every
-- message; those numbers are misleading on a flat-subscription plan and the
-- UI no longer renders them. Zero them out so the API surface reads 0
-- consistently.
--
-- The cost_usd columns are kept (rather than dropped) so the W0-frozen
-- contract stays binary-compatible — DTOs still serialise the field, just
-- always as 0 going forward.

UPDATE messages  SET cost_usd = 0 WHERE cost_usd <> 0;
UPDATE sessions  SET cost_usd = 0 WHERE cost_usd <> 0;
