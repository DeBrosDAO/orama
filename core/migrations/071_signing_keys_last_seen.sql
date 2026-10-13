-- Migration 071: when a signing key was last known to be in use.
--
-- Every index gateway has a key of its own (one per node) and publishes it
-- unbound. Releases before the credential change published a new one on every
-- restart and never retired the old, so signing_keys holds keys nothing signs
-- with. Telling those from the live key of a peer that has simply not
-- restarted needs a liveness signal; created_at is not one.
--
-- A gateway stamps its own key at publish and on a timer. The row of a key
-- nobody has stamped for longer than the longest token lifetime plus a margin
-- can have no unexpired token left, and the index gateway retires it at start.
-- Existing rows are stamped now, so a peer that has not been upgraded yet has
-- that long to be, and a restart of it un-retires its key.
--
-- The runner treats a repeated ADD COLUMN as applied, so a re-run changes
-- nothing.

ALTER TABLE signing_keys ADD COLUMN last_seen_at TIMESTAMP;

UPDATE signing_keys SET last_seen_at = CURRENT_TIMESTAMP WHERE last_seen_at IS NULL;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (71);
