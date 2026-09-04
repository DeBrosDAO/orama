-- One wallet owns a namespace, and no key carries an implicit admin grant.
--
-- Ownership was a side effect of minting a key: GetOrCreateAPIKey inserted the
-- caller into namespace_ownership unconditionally, so any wallet that signed a
-- fresh nonce and named an existing namespace in the body of /v1/auth/verify
-- became a co-owner of it. The ownership row then satisfied the namespace gate,
-- which marked the caller a confirmed owner, which granted a wallet JWT admin.
-- The key it handed back was minted with no scopes column at all, and an empty
-- scopes column was read as admin, so the same request produced a second admin
-- credential that outlived the session.
--
-- The code refuses to write a second wallet owner from here on. This brings the
-- existing rows in line with that rule and makes it an invariant the database
-- enforces, so two concurrent first-logins cannot both win.
--
-- Idempotent: on a second apply every namespace already has one wallet owner
-- and every live key already has an explicit scope set, so nothing moves.

BEGIN;

-- 1. Collapse co-owners to the wallet that got there first.
--
-- The earliest row is the closest thing to the namespace's creator that
-- survives: every later wallet row is a login that should have been refused.
-- Dropping them takes back an authority those wallets should never have had —
-- which is the point of the fix, not a side effect of it.
DELETE FROM namespace_ownership
 WHERE owner_type = 'wallet'
   AND id > (
       SELECT MIN(first.id) FROM namespace_ownership AS first
        WHERE first.namespace_id = namespace_ownership.namespace_id
          AND first.owner_type = 'wallet'
   );

-- 2. Make it an invariant rather than a convention. A partial unique index on
-- the namespace alone (the table's own UNIQUE covers owner_id too, so it never
-- stopped a second *different* wallet) means two concurrent inserts cannot both
-- succeed, and the loser learns it lost.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ns_one_wallet_owner
    ON namespace_ownership(namespace_id)
 WHERE owner_type = 'wallet';

-- 3. Write down the grant that was being inferred.
--
-- An empty scopes column meant "minted before scoping existed", which the read
-- path turned into admin. That grandfather rule is being removed — an empty
-- scope set denies from here on — so every key that is relying on it gets the
-- grant it already had, explicitly. Access does not change; it becomes
-- auditable, and a key minted with no scopes stops being an admin key by
-- default.
--
-- Revoked keys are left alone: they authenticate nothing, and rewriting them
-- would destroy the record of what they could do.
UPDATE api_keys
   SET scopes = 'admin'
 WHERE revoked_at IS NULL
   AND (scopes IS NULL OR TRIM(scopes) = '');

INSERT OR IGNORE INTO schema_migrations(version) VALUES (43);

COMMIT;
