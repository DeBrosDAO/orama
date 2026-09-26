-- API keys stop being permanent.
--
-- `api_keys` had no expiry column at all. A key minted once was a bearer token
-- that worked until somebody remembered to revoke it.
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
--
-- Expand only. This release adds the columns in place and constrains nothing:
-- a rolling upgrade runs it while most gateways are still 0.122.x, which insert
-- keys with neither an expiry nor (on the wallet path) a grant set. The table
-- rebuild that made expires_at and scopes NOT NULL failed every one of those
-- inserts for the whole window. A key a 0.122.x gateway mints in the window has
-- a NULL expires_at, which this release's lookup (`expires_at > now`) does not
-- match: it works on 0.122.x gateways and on none of this release's, and
-- stops at the end of the rollout.
--
-- The cutoff. Only keys that existed when this migration first ran get the
-- 90-day window: api_keys_expiry_cutoff records the highest key id at that moment,
-- first thing, and every backfill below is bounded by it. A replay — a runner
-- that died before recording the version, re-run after 0.122.x gateways had
-- minted more — would otherwise hand those window keys an expiry and turn
-- them into live keys on this release, with the grant set 0.122.x left NULL.
-- The next release contracts: it REVOKES every key above the cutoff that still
-- has no expiry (it never backfills one), then rebuilds the table with
-- expires_at and scopes NOT NULL, as 009_dns_records_multi.sql rebuilds
-- dns_records.
--
-- Replay-safe: the cutoff is recorded once (INSERT OR IGNORE), an ADD COLUMN
-- that already happened is "duplicate column name", which the migration runner
-- treats as applied, and the UPDATEs touch only pre-cutoff rows still missing a
-- value.

BEGIN;

CREATE TABLE IF NOT EXISTS api_keys_expiry_cutoff (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    max_key_id   INTEGER NOT NULL,
    recorded_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO api_keys_expiry_cutoff (id, max_key_id)
SELECT 1, COALESCE(MAX(id), 0) FROM api_keys;

ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMP;
ALTER TABLE api_keys ADD COLUMN rotated_from INTEGER;
ALTER TABLE api_keys ADD COLUMN principal_id INTEGER;

UPDATE api_keys
   SET expires_at = datetime('now', '+90 days')
 WHERE expires_at IS NULL
   AND id <= (SELECT max_key_id FROM api_keys_expiry_cutoff WHERE id = 1);

UPDATE api_keys
   SET principal_id = (SELECT p.id FROM principals p
                        WHERE p.type = 'service_account' AND p.identifier = api_keys.key)
 WHERE principal_id IS NULL
   AND id <= (SELECT max_key_id FROM api_keys_expiry_cutoff WHERE id = 1);

CREATE INDEX IF NOT EXISTS idx_api_keys_expiry ON api_keys(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_rotated_from ON api_keys(rotated_from);

COMMIT;
