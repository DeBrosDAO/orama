-- API keys stop being permanent.
--
-- `api_keys` had no expiry column at all. A key minted once was a bearer token
-- that worked until somebody remembered to revoke it, and `scopes` was nullable
-- — which is how a key with no grant set came to be read as an admin key
-- (migration 043 wrote down the grant that was being inferred, and this makes
-- the column say so).
--
-- SQLite cannot add a NOT NULL constraint to an existing column, so the table
-- is rebuilt. The shape mirrors 009_dns_records_multi.sql, which does the same
-- thing for the same reason, and is replay-safe: on a second pass the `_new`
-- table is recreated empty, filled from the current table, and swapped back.
--
-- New columns:
--   expires_at    when the key stops working, checked on every lookup
--   rotated_from  the key this one replaces, so an overlap is visible
--   principal_id  the key's principal (migration 050), so a key is a member of
--                 the namespace in the same table everything else is
--
-- Existing keys are given 90 days from this migration rather than 90 days from
-- when they were minted. Dating the expiry from creation would expire every key
-- older than three months the moment this runs, which is a fleet-wide outage
-- dressed up as a security improvement.

BEGIN;

CREATE TABLE IF NOT EXISTS api_keys_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    key            TEXT NOT NULL UNIQUE,
    name           TEXT,
    namespace_id   INTEGER NOT NULL,
    -- An empty grant set denies. It is NOT NULL so that a key minted by a path
    -- that forgets to say what it may do fails at the database rather than
    -- becoming whatever the read path infers.
    scopes         TEXT NOT NULL DEFAULT '',
    -- NOT NULL: a key with no expiry is the thing this migration is about.
    expires_at     TIMESTAMP NOT NULL,
    rotated_from   INTEGER,
    principal_id   INTEGER,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at   TIMESTAMP,
    revoked_at     TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO api_keys_new
    (id, key, name, namespace_id, scopes, expires_at, principal_id, created_at, last_used_at, revoked_at)
SELECT k.id, k.key, k.name, k.namespace_id,
       COALESCE(k.scopes, ''),
       datetime('now', '+90 days'),
       (SELECT p.id FROM principals p
         WHERE p.type = 'service_account' AND p.identifier = k.key),
       k.created_at, k.last_used_at, k.revoked_at
  FROM api_keys AS k;

DROP TABLE IF EXISTS api_keys;
ALTER TABLE api_keys_new RENAME TO api_keys;

CREATE INDEX IF NOT EXISTS idx_api_keys_namespace ON api_keys(namespace_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_expiry ON api_keys(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_rotated_from ON api_keys(rotated_from);

COMMIT;
