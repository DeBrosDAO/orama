-- Which incarnation of a namespace a pending cleanup was owed for, and whether
-- it also owed the namespace's tenant data.
--
-- A teardown that failed is replayed every sweep until the node accepts it. The
-- row was keyed (namespace, node, action) only, so a namespace deleted and then
-- created again, and placed on that same node before the replay succeeded, had
-- the replay tear down the NEW namespace and delete its data. The replay now
-- drops the row instead when the registry shows the namespace is on that node
-- again, and compares cluster_id to tell one incarnation from the next.
--
-- cluster_id is '' on rows written before this migration, and on the stop-*
-- actions, which are not destructive and are not checked. purge_data is 1 when
-- the teardown was the delete of the namespace, which also removes its SQLite
-- databases and deployment directories. The runner treats a repeated ADD COLUMN
-- as applied, so a re-run changes nothing.

ALTER TABLE namespace_pending_cleanup ADD COLUMN cluster_id TEXT NOT NULL DEFAULT '';
ALTER TABLE namespace_pending_cleanup ADD COLUMN purge_data INTEGER NOT NULL DEFAULT 0;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (66);
