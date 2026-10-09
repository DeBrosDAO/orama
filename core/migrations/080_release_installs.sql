-- =============================================================================
-- 080_release_installs.sql
--
-- Where each node of a cluster is in an automatic release rollout.
--
-- A cluster that sets auto_update to auto (cluster_settings) has each node
-- install the newest verified release of its channel, one node at a time. A
-- node records here what happened to it, so that
--
--   - the next node knows whose turn it is: the nodes that have an 'installed'
--     row for a version have it; the rollout plan (followers first, the leader
--     last, nameservers spaced) names the first that does not;
--   - a release that failed on one node is a bad release for all of them: any
--     'failed' row for a version stops every node from installing it. A newer
--     release supersedes it.
--
-- Mutual exclusion between nodes is not here; it is a row in cluster_locks.
--
-- Cluster registry only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS release_installs (
    version     TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    state       TEXT NOT NULL,
    detail      TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (version, node_id)
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (80);
