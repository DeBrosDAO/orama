-- A namespace delete runs the teardown on the node that took the request. If
-- that node dies, or the delete is cut off, the cluster stays in
-- 'deprovisioning' with nothing carrying the teardown on. deprovisioning_at is
-- when a teardown last claimed the cluster, stamped by the registry's own clock:
-- a cluster whose claim is older than the longest a live teardown runs is
-- abandoned, and any node's tenant reconciler takes it over with a guarded
-- UPDATE of this column (ClusterManager.resumeStaleDeprovisioning).
--
-- Rows already in 'deprovisioning' are left NULL, and so is a cluster marked by a
-- node still on the previous release (a rolling upgrade): its delete may be
-- running. The sweep stamps a NULL row with the registry's now and judges it
-- only after a whole window, so a delete that is still running is not taken over.

-- The runner treats a repeated ADD COLUMN as applied, so a re-run changes
-- nothing.

ALTER TABLE namespace_clusters ADD COLUMN deprovisioning_at TIMESTAMP;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (72);
