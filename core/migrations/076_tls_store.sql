-- =============================================================================
-- 076_tls_store.sql
--
-- The cluster's shared certificate store (bugboard #751).
--
-- Every node's Caddy used to keep its own certificates on its own disk, so
-- every node obtained its own copy of the same `*.<base>` and `<base>`
-- certificates. Let's Encrypt issues at most five certificates per week for
-- one set of names, so a five-node cluster spent the week's allowance in one
-- install, and the next join, reinstall or rebuild got none. Caddy now keeps
-- them here, through its caddy.storage.orama module: the first node to need a
-- certificate takes the lock, obtains it and stores it, and every other node
-- loads it. One certificate per cluster, renewed once.
--
-- tls_store is CertMagic's key/value storage. Every value is sealed by Caddy
-- with a key derived from the cluster secret before it reaches the gateway, so
-- these rows, their snapshots and backups never hold a private key in the
-- clear. tls_locks is CertMagic's distributed lock: a holder and a lease the
-- holder keeps extending while it works.
--
-- Cluster registry only.
-- =============================================================================

CREATE TABLE IF NOT EXISTS tls_store (
    key              TEXT PRIMARY KEY,
    value            TEXT NOT NULL,
    size             INTEGER NOT NULL,
    modified_unix_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tls_locks (
    name            TEXT PRIMARY KEY,
    holder          TEXT NOT NULL,
    expires_unix_ms INTEGER NOT NULL
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (76);
