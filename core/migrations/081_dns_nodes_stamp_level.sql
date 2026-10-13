-- =============================================================================
-- 081_dns_nodes_stamp_level.sql
--
-- The inter-node stamp protocol each node signs, as the node says it on
-- registering and on every heartbeat.
--
-- A verifier of the inter-node stamps (coordination, ACME, node-api) accepts
-- the older, unnonced forms only while some node of the cluster may still sign
-- nothing else: a stripped-header replay of a nonced request would otherwise
-- pass on the older form for as long as the code accepts it. Whether any node
-- may is read from this column (pkg/auth, legacy floor), not inferred from
-- release numbers, which are not comparable across the network's version lines.
--
--   stamp_level     0 = the older stamps only (the default, and a node that never
--                   reported), 1 = also the v3 coordination stamp and the nonced
--                   ACME and node-api stamps (auth.StampLevel*).
--   stamp_level_at  the datetime('now') the level was written with. A node
--                   running the capability writes it in the same statement as
--                   last_seen, so it equals last_seen. A build from before this
--                   column refreshes last_seen without touching either column,
--                   so after a rollback last_seen moves on and this stays behind:
--                   the floor counts a level only while stamp_level_at >=
--                   last_seen, and a rolled-back node is legacy again.
--
-- Cluster registry only.
-- =============================================================================

ALTER TABLE dns_nodes ADD COLUMN stamp_level INTEGER NOT NULL DEFAULT 0;
ALTER TABLE dns_nodes ADD COLUMN stamp_level_at TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_migrations(version) VALUES (81);
