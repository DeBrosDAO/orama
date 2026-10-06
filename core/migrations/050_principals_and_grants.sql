-- Who may do what in a namespace, as data rather than as a boolean.
--
-- Authorization was one row in namespace_ownership(namespace_id, owner_type,
-- owner_id). A row meant "owner", the absence of one meant "refused", and there
-- was nothing in between: a second person on a team could be given full control
-- of the namespace or nothing at all. There was no way to say "this wallet may
-- deploy but not mint keys", no way to name a service account as anything other
-- than an owner, and no way to hand a grant out with an expiry.
--
-- Two tables replace it. `principals` is who — a wallet, or a service account
-- (an API key). `grants` is what they may do there, as a named role, optionally
-- narrowed to a resource and optionally expiring. The invariant that used to
-- live in a partial index on namespace_ownership moves with it: exactly one
-- live `owner` grant per namespace.
--
-- Expand only: namespace_ownership is left in place for this release.
--
-- A rolling upgrade runs this migration while most gateways are still 0.122.x,
-- and those read and write namespace_ownership on every ownership check,
-- namespace claim and key mint. Dropping it here failed all of them for the
-- whole window. The table stays, unread by this release, and is contracted in
-- the next one: that migration re-runs the backfill below — idempotent, it
-- picks up only rows a 0.122.x gateway wrote during the window — and then
-- drops the table. It is dropped rather than left behind as a view, because a
-- database that replays the chain from the beginning creates that name as a
-- table in 002_core.sql and then indexes it, and a view cannot be indexed.
--
-- Until then the two do not converge by themselves: an owner a 0.122.x gateway
-- records exists only in namespace_ownership (this release sees the namespace
-- as unclaimed until the contract migration backfills it), and one this
-- release records exists only in grants. Keep the window to the rollout.
--
-- Idempotent: the tables are guarded, and the backfill inserts only rows that
-- have no match already.

BEGIN;

CREATE TABLE IF NOT EXISTS principals (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    -- 'wallet' or 'service_account'. Only the two the platform can currently
    -- authenticate: adding a value is a one-line change, and an enum entry
    -- nothing writes reads as a capability that exists.
    type         TEXT NOT NULL,
    -- The normalised wallet address, or an API key's stored hash. Whatever the
    -- authentication layer resolves the caller to, spelled the same way.
    identifier   TEXT NOT NULL,
    display_name TEXT,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by   TEXT,
    disabled_at  TIMESTAMP,
    UNIQUE(type, identifier)
);

CREATE TABLE IF NOT EXISTS grants (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    principal_id INTEGER NOT NULL,
    namespace_id INTEGER NOT NULL,
    -- 'owner', 'admin', 'runtime' or 'reader'. See pkg/gateway/auth/grants.go
    -- for why 'developer' is not here yet.
    role         TEXT NOT NULL,
    -- A selector narrowing the role to part of the namespace, e.g.
    -- 'storage:avatars/*'. NULL is the whole role. Stored and validated; the
    -- data plane does not enforce selectors yet, so a grant carrying one
    -- authorises nothing until it does.
    resource     TEXT,
    expires_at   TIMESTAMP,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by   TEXT,
    revoked_at   TIMESTAMP,
    FOREIGN KEY(principal_id) REFERENCES principals(id) ON DELETE CASCADE,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);

-- One live owner per namespace, enforced by the database rather than by a check
-- the code remembers to make. This is migration 043's index, moved: two
-- concurrent first-claims cannot both win, and the loser learns it lost.
CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_one_owner
    ON grants(namespace_id)
 WHERE role = 'owner' AND revoked_at IS NULL;

-- One live grant of a given shape per principal per namespace. Without it,
-- granting the same role twice leaves two rows and revoking one looks like it
-- worked.
CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_one_live
    ON grants(principal_id, namespace_id, role, COALESCE(resource, ''))
 WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_grants_namespace ON grants(namespace_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_grants_principal ON grants(principal_id) WHERE revoked_at IS NULL;

-- Backfill: every existing ownership row becomes a principal and a grant.
--
-- namespace_ownership is created by 002_core.sql and not dropped by this
-- release (see above). A database that ran a pre-release build of this
-- migration, which did drop it, may replay this one without it; the guarded
-- create (002_core.sql's shape) makes that replay read nothing instead of
-- failing, and puts the table back for the 0.122.x gateways still reading it.
CREATE TABLE IF NOT EXISTS namespace_ownership (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    owner_type    TEXT NOT NULL,
    owner_id      TEXT NOT NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace_id, owner_type, owner_id)
);
--
-- Wallets become owners. Migration 043 already collapsed co-owners to the
-- earliest row and the partial index above would refuse a second anyway, but
-- the MIN(id) filter is here so this is correct against a database that somehow
-- has more, rather than failing halfway through.
INSERT OR IGNORE INTO principals(type, identifier, created_by)
SELECT 'wallet', LOWER(owner_id), 'migration 050'
  FROM namespace_ownership
 WHERE owner_type = 'wallet';

INSERT OR IGNORE INTO principals(type, identifier, created_by)
SELECT 'service_account', owner_id, 'migration 050'
  FROM namespace_ownership
 WHERE owner_type = 'api_key';

INSERT INTO grants(principal_id, namespace_id, role, created_at, created_by)
SELECT p.id, o.namespace_id, 'owner', o.created_at, 'migration 050'
  FROM namespace_ownership AS o
  JOIN principals AS p
    ON p.type = 'wallet' AND p.identifier = LOWER(o.owner_id)
 WHERE o.owner_type = 'wallet'
   AND o.id = (SELECT MIN(first.id) FROM namespace_ownership AS first
                WHERE first.namespace_id = o.namespace_id AND first.owner_type = 'wallet')
   AND NOT EXISTS (SELECT 1 FROM grants AS g
                    WHERE g.namespace_id = o.namespace_id AND g.role = 'owner' AND g.revoked_at IS NULL);

-- A service account gets the role its key's grant set already implied: a key
-- holding 'admin' was a control-plane credential, anything else was a runtime
-- one. The key's own scopes column stays authoritative for what it may reach —
-- the grant records that it belongs to the namespace at all, which is the job
-- the ownership row was doing.
INSERT INTO grants(principal_id, namespace_id, role, created_at, created_by)
SELECT p.id, o.namespace_id,
       CASE WHEN EXISTS (SELECT 1 FROM api_keys AS k
                          WHERE k.key = o.owner_id
                            AND k.namespace_id = o.namespace_id
                            AND k.revoked_at IS NULL
                            AND (',' || REPLACE(k.scopes, ' ', '') || ',') LIKE '%,admin,%')
            THEN 'admin' ELSE 'runtime' END,
       o.created_at, 'migration 050'
  FROM namespace_ownership AS o
  JOIN principals AS p
    ON p.type = 'service_account' AND p.identifier = o.owner_id
 WHERE o.owner_type = 'api_key'
   AND NOT EXISTS (SELECT 1 FROM grants AS g
                    WHERE g.principal_id = p.id AND g.namespace_id = o.namespace_id AND g.revoked_at IS NULL);

COMMIT;
