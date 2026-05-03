-- =============================================================================
-- 021_pubsub_trigger_patterns.sql
--
-- Add `topic_pattern` column alongside the existing `topic` column to
-- function_pubsub_triggers. The new column may contain SQLite GLOB
-- patterns (e.g. "presence:*") in addition to exact topic names.
--
-- This is intentionally ADDITIVE rather than a column rename to remain
-- safe under rolling upgrades:
--   - Old binaries continue reading `topic` and keep working.
--   - New binaries read `topic_pattern` (which is back-filled from
--     `topic` for existing rows) and write BOTH columns.
-- A future migration can DROP COLUMN topic once every node is on the
-- new release.
-- =============================================================================

ALTER TABLE function_pubsub_triggers
    ADD COLUMN topic_pattern TEXT NOT NULL DEFAULT '';

UPDATE function_pubsub_triggers
SET topic_pattern = topic
WHERE topic_pattern = '';

CREATE INDEX IF NOT EXISTS idx_function_pubsub_triggers_function
    ON function_pubsub_triggers(function_id);

CREATE INDEX IF NOT EXISTS idx_function_pubsub_triggers_enabled
    ON function_pubsub_triggers(enabled);
