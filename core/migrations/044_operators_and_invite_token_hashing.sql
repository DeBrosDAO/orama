-- Who may operate the cluster, and invite tokens that are not readable at rest.
--
-- /v1/operator/* had no scope entry and no ownership entry, so it fell through
-- to "any valid credential". `orama node invite` mints a cluster invite token,
-- and /v1/internal/join hands an invite token holder the cluster secret, the
-- swarm key, the API-key HMAC secret, the RQLite password, the
-- secrets-encryption key and the TURN secret — in cleartext. The JWT signing
-- key is derived from the cluster secret, so that is identity forgery across
-- the whole network, reachable from a key extracted out of an app bundle.
--
-- Being an operator is a fact about a wallet now, written here, rather than a
-- side effect of holding any credential at all.

BEGIN;

-- 1. The operator list.
--
-- added_by records who let this wallet in, so the list can be audited rather
-- than just read. 'genesis:dns_nodes' marks the wallets seeded below.
CREATE TABLE IF NOT EXISTS operators (
    wallet     TEXT PRIMARY KEY,
    added_by   TEXT NOT NULL,
    added_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 2. Seed it from the wallets that already operate this cluster.
--
-- `orama node install --operator-wallet` writes that address into the node's
-- config, and the node writes it to dns_nodes on every heartbeat. It is the
-- only record of who provisioned each node, so it is the genesis seed.
--
-- A cluster installed without --operator-wallet seeds nothing, and nobody can
-- reach /v1/operator/* until a row is inserted here on a node. That is the
-- correct failure: an empty allowlist denies, it does not fall back.
INSERT OR IGNORE INTO operators (wallet, added_by)
SELECT DISTINCT LOWER(TRIM(operator_wallet)), 'genesis:dns_nodes'
  FROM dns_nodes
 WHERE operator_wallet IS NOT NULL
   AND TRIM(operator_wallet) <> '';

-- 3. Invite tokens stop being readable at rest.
--
-- The token column was the raw token, as the primary key. Anyone who could
-- read the registry — a disk snapshot, a raw rqlite query, the export endpoint
-- — could join the cluster and be handed every secret in it. The column holds
-- "sha256:<hex>" from here on, and the gateway hashes what a caller presents
-- before looking it up.
--
-- Existing rows cannot be converted: SQLite has no hash function, so there is
-- no way to compute the hash of a token from inside a migration. They are
-- deleted instead. An invite token lives an hour by default and seven days at
-- most, so the cost is that an unconsumed invite has to be re-minted; the
-- alternative is leaving the plaintext this migration exists to remove.
--
-- The predicate is what makes this re-runnable: after the first apply every
-- row is a hash, and a second apply deletes nothing.
DELETE FROM invite_tokens WHERE token NOT LIKE 'sha256:%';

INSERT OR IGNORE INTO schema_migrations(version) VALUES (44);

COMMIT;
