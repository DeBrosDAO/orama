-- =============================================================================
-- 074_namespace_sign_in_policy.sql
--
-- Who may sign in to a namespace: only wallets holding a grant ('members', what
-- every namespace has had until now) or also wallets that hold none ('open').
--
-- An application's public end users each sign in with their own wallet and are
-- invited by nobody; an owner who wants them in says so here, and they then
-- sign in as grantless end users that get no key and no grant. It lives on the
-- row that already holds a namespace's device policy, in the cluster registry
-- beside it, and is read where sessions are issued and refreshed.
--
-- A namespace with no row is 'members'. A row written for the device policy
-- before this migration reads 'members' too, and a row created by setting the
-- sign-in policy alone starts with the device policy 'optional'.
--
-- The runner treats a repeated ADD COLUMN as applied, so a re-run changes
-- nothing.
-- =============================================================================

ALTER TABLE namespace_session_policy ADD COLUMN sign_in TEXT NOT NULL DEFAULT 'members' CHECK (sign_in IN ('members','open'));

INSERT OR IGNORE INTO schema_migrations(version) VALUES (74);
