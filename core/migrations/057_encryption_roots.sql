-- The IKM stored-ciphertext keys are derived from. It used to be the cluster
-- secret, so rotating stored secrets meant rotating IPFS-Cluster's PSK and the
-- mesh bearer at the same time — which is why it never happened.
--
-- Two slots: current, and previous while a rotate is rewriting rows. The IKM
-- is the secret; this table is the cluster-wide copy so every gateway derives
-- the same keys (the same reason function secrets moved off a per-node file).

CREATE TABLE IF NOT EXISTS encryption_roots (
    slot            TEXT PRIMARY KEY,          -- 'current' | 'previous'
    key_id          TEXT NOT NULL,             -- generation, starting at '1'
    ikm             TEXT NOT NULL,             -- 64 hex chars
    write_versioned INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
