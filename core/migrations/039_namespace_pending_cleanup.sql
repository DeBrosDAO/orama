-- Remote service stops that failed, so they can be retried.
--
-- stopRQLiteOnNode / stopOlricOnNode / stopGatewayOnNode logged a warning and
-- moved on when the remote node could not be reached. The unit stayed running,
-- holding a port the allocator had already released — and the next namespace
-- allocated that port, found it occupied, and (bugboard #275) joined a FOREIGN
-- raft group serving another namespace's database.
--
-- A stop that failed is therefore not something to log; it is work still owed.
-- The tenant reconciler's coordinator leg replays these every sweep until the
-- node accepts the stop or is pruned from the cluster entirely.

BEGIN;

CREATE TABLE IF NOT EXISTS namespace_pending_cleanup (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    node_id TEXT NOT NULL,
    node_ip TEXT NOT NULL,
    action TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_attempt_at TIMESTAMP,
    UNIQUE (namespace, node_id, action)
);

CREATE INDEX IF NOT EXISTS idx_pending_cleanup_node
    ON namespace_pending_cleanup (node_id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (39);

COMMIT;
