-- =============================================================================
-- 081_dns_nodes_node_version.sql
--
-- The release each node runs, as the node says it on registering and on every
-- heartbeat.
--
-- A verifier of the inter-node stamps (coordination, ACME, node-api) accepts
-- the older, unnonced forms only while some node of the cluster may still sign
-- nothing else: a stripped-header replay of a nonced request would otherwise
-- pass on the older form for as long as the code accepts it. Whether any node
-- may is read from the lowest version in this column (pkg/auth, legacy floor).
--
-- '' is a node that has not said (a build from before this column), and counts
-- as older than every release.
--
-- Cluster registry only.
-- =============================================================================

ALTER TABLE dns_nodes ADD COLUMN node_version TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO schema_migrations(version) VALUES (81);
