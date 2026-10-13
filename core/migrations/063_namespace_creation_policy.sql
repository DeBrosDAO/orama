-- Who may create a namespace, and how many one wallet may own.
--
-- POST /v1/namespaces used to accept any signed-in wallet. On a private
-- cluster that is a capacity leak: a wallet that can reach the gateway can
-- ask for as many namespace clusters as the per-wallet cap allows.
--
-- namespace_creation is operators, allowlist or open.
--   operators  — the caller's wallet is in operators. This is what a cluster
--                with no stored row enforces, so a brand-new registry is
--                closed without anyone having to remember to close it.
--   allowlist  — the wallet is in namespace_creators.
--   open       — any signed-in wallet, which is today's behaviour.
--
-- An upgrade must not take that away from a cluster that already has data
-- (AnChat, stagenet). Those get a stored 'open'. A registry whose only
-- namespace is the 'default' row migration 001 seeds, and which has no
-- operator and no node, is a new cluster: no row is written, and the
-- gateway reads a missing row as operators.
--
-- The per-wallet cap stays 10 in code when max_namespaces_per_wallet is
-- absent. An operator stores a different integer to change it. This
-- migration does not write that row.
--
-- namespace_creators has the same shape as operators. An empty allowlist
-- denies everyone, which is a valid locked state; it is not the operator
-- list, so removing the last creator does not lock the cluster out of its
-- own admin API.
--
-- Idempotent: the seed inserts only when the key is absent, so a later
-- choice survives a retried apply. Both tables are cluster-registry state
-- (schema_placement.go). On a namespace RQLite the statements are skipped
-- with the other cluster-only tables, and the extra guard below is what
-- keeps a manual run against a namespace database from planting an 'open'
-- row that the empty-table drop would then refuse to remove.

CREATE TABLE IF NOT EXISTS cluster_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS namespace_creators (
    wallet   TEXT PRIMARY KEY,
    added_by TEXT NOT NULL,
    added_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO cluster_settings (key, value, updated_by)
SELECT 'namespace_creation', 'open', 'migration:063'
 WHERE NOT EXISTS (
       SELECT 1 FROM cluster_settings WHERE key = 'namespace_creation'
 )
   AND NOT EXISTS (
       SELECT 1 FROM sqlite_master
        WHERE type = 'table' AND name = 'orama_schema_migrations'
   )
   AND (
       EXISTS (SELECT 1 FROM namespaces WHERE name <> 'default')
       OR EXISTS (SELECT 1 FROM operators)
       OR EXISTS (SELECT 1 FROM dns_nodes)
   );

INSERT OR IGNORE INTO schema_migrations(version) VALUES (63);
