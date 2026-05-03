-- =============================================================================
-- 022_aggregation_windows.sql
--
-- Add per-trigger aggregation parameters to function_pubsub_triggers.
--
-- aggregation_window_ms = 0 means "no aggregation, invoke once per event"
--   (the existing behaviour). Any positive value enables buffering of events
--   in-memory on the dispatching node; the function is invoked once per
--   window with a batched payload.
--
-- aggregation_max_batch_size caps the per-window batch. When the buffer
--   reaches this size, the dispatcher flushes immediately even if the
--   window timer hasn't fired yet.
-- =============================================================================

ALTER TABLE function_pubsub_triggers
    ADD COLUMN aggregation_window_ms INTEGER NOT NULL DEFAULT 0;

ALTER TABLE function_pubsub_triggers
    ADD COLUMN aggregation_max_batch_size INTEGER NOT NULL DEFAULT 100;
