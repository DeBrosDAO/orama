-- A cluster-wide mutex, held through raft.
--
-- rqlite serialises writes through raft, so a conditional UPDATE is a
-- linearizable compare-and-swap and therefore a correct mutex across every node
-- — which is the only kind available here, since the nodes share nothing else.
--
-- The immediate need is migrations. The runner snapshotted the applied set once
-- and ran everything missing from it, so N gateways starting together all ran
-- the same pending migrations against the same database. DDL is guarded by
-- IF NOT EXISTS; DML is not. Migration 019 is
-- `UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE revoked_at IS NULL`
-- — a second node reaching it a minute after the first revokes every refresh
-- token issued in between. A fleet-wide logout, with nothing in the logs.
--
-- expires_at is what makes it self-healing: a holder that dies mid-migration
-- would otherwise block every future start for ever.

BEGIN;

CREATE TABLE IF NOT EXISTS cluster_locks (
    name TEXT PRIMARY KEY,
    holder TEXT NOT NULL DEFAULT '',
    acquired_at TIMESTAMP,
    expires_at TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (40);

COMMIT;
