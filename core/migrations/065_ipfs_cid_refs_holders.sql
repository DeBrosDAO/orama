-- How many in-flight registrations stand behind an ipfs_cid_refs row.
--
-- A row is one (cid, namespace, kind), so two requests that register the same
-- content in one namespace share it. When the first fails and takes its
-- registration back, deleting the row would delete the one the second depends
-- on. Each registration now adds one to holders and a failed one subtracts one;
-- the row goes only when it reaches zero. A release (an unpin, a delete) still
-- removes the row outright: the namespace lets go of the CID.
--
-- Existing rows count as one holder. The runner treats a repeated ADD COLUMN
-- as applied, so a re-run changes nothing.

ALTER TABLE ipfs_cid_refs ADD COLUMN holders INTEGER NOT NULL DEFAULT 1;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (65);
