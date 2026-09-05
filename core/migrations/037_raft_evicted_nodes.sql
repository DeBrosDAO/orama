-- Tombstones for raft members that were removed on purpose.
--
-- Nothing distinguished "this node was deliberately taken out of the cluster"
-- from "this node is temporarily missing from /nodes". recoverOrphanedNodes
-- re-adds every discovery peer absent from the raft configuration, and
-- discovery only forgets a peer after an inactivity window, so an operator's
-- removal was undone within five minutes.
--
-- A row here says the removal was intentional. Membership paths skip a
-- tombstoned node; a node that legitimately returns clears its own tombstone by
-- joining, so the record is a veto on automatic re-adding, never on an
-- explicit rejoin.

BEGIN;

CREATE TABLE IF NOT EXISTS raft_evicted_nodes (
    node_id     TEXT PRIMARY KEY,          -- raft node id, as it appeared in /nodes
    raft_addr   TEXT NOT NULL DEFAULT '',  -- raft advertise address at eviction time
    peer_id     TEXT NOT NULL DEFAULT '',  -- libp2p peer id, when known
    reason      TEXT NOT NULL,             -- 'dead-voter', 'decommission', 'operator'
    evicted_by  TEXT NOT NULL DEFAULT '',  -- node that performed the eviction
    evicted_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_raft_evicted_peer ON raft_evicted_nodes(peer_id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (37);

COMMIT;
