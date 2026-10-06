-- Where each registry backup went, so one can actually be found again.
--
-- performBackup runs on the leader only and writes to the leader's own disk,
-- keeping three files. Leadership moves, so each node holds a disjoint fragment
-- of the history and no node holds a usable series; `orama node wipe` removes
-- them; and nothing records that a backup was ever taken. There was no credible
-- restore path for the registry — which is the cluster's memory of every
-- namespace, node, DNS record and API key.
--
-- A backup pushed to IPFS survives the machine that made it. This table is the
-- index: without it a CID is unfindable, which is the same as not existing.

BEGIN;

CREATE TABLE IF NOT EXISTS rqlite_backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    taken_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    taken_by TEXT NOT NULL DEFAULT '',
    cid TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    encrypted INTEGER NOT NULL DEFAULT 1,
    UNIQUE (cid)
);

CREATE INDEX IF NOT EXISTS idx_rqlite_backups_taken_at
    ON rqlite_backups (taken_at DESC);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (41);

COMMIT;
