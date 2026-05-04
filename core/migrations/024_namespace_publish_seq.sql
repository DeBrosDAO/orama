-- =============================================================================
-- 024_namespace_publish_seq.sql
--
-- Per-namespace monotonically-increasing sequence number assigned by
-- exec_and_publish (plan 08). The seq is included in the wake-up payload so
-- subscribers can detect "I'm behind, retry" gaps caused by cross-node
-- replication lag between the leader's commit and the gossipsub message.
--
-- The row is upserted in the same atomic batch as the user's writes, so the
-- assigned seq exactly mirrors the commit number. See plan:
--   core/plans/platform/08_EXEC_AND_PUBLISH.md
-- =============================================================================

CREATE TABLE IF NOT EXISTS namespace_publish_seq (
    namespace  TEXT PRIMARY KEY,
    next_seq   BIGINT NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL
);
