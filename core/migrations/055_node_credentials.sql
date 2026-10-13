-- Migration 055: a node's own key.
--
-- A node authenticates to the cluster with a value derived from the cluster
-- secret, which every node holds. That proves membership, not identity: any
-- node can sign for any other, and a decommissioned node's disk is a working
-- credential for the whole fleet until the cluster secret is rotated — which
-- invalidates every token in the cluster at once, so in practice it is not.
--
-- A node now holds an Ed25519 private key it generated itself and that never
-- leaves the machine. This table is the public half, and the cluster verifies
-- against it. The private key is not derivable from anything shared, so one
-- node's compromise is one node.
--
-- There is no trust-on-first-use window. Enrolling a key is authenticated by
-- the node's libp2p identity, whose public half its peer id carries, so the
-- cluster can check who is enrolling having been told nothing in advance: the
-- only machine that can put a key here for node X is the one holding X's
-- identity. Nothing derived from the cluster secret is accepted.
--
-- Revoking is not deleting. A removed node keeps its row with `revoked_at` set,
-- so it verifies nothing and cannot enrol again with a key of its choosing.
-- Re-admitting a machine happens at the join, which needs an operator-minted
-- single-use invite: the join clears the row, and the machine then enrols a
-- fresh key with its own identity. Without that, removing a node would be a
-- one-way door and a rebuilt machine could never register again.

CREATE TABLE IF NOT EXISTS node_credentials (
    -- The node's libp2p peer id: the same identifier `dns_nodes` and
    -- `wireguard_peers` are keyed on, so there is one name for a node.
    node_id     TEXT PRIMARY KEY,

    -- The Ed25519 public key, base64 standard encoding of the 32 raw bytes.
    -- Public by definition; this table holds nothing that can impersonate a
    -- node, which is the property a shared secret could not have.
    public_key  TEXT NOT NULL,

    enrolled_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Set when the node is retired. A row with this set verifies nothing and
    -- cannot be re-enrolled; it is a tombstone, not a disabled row that a
    -- later insert would quietly replace.
    revoked_at  TIMESTAMP
);

-- "Which nodes can still sign" is the question every stamped request asks.
CREATE INDEX IF NOT EXISTS idx_node_credentials_live ON node_credentials(node_id, revoked_at);
