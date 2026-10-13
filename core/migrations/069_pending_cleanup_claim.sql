-- A lease on a pending cleanup, so one gateway replays a row at a time.
--
-- The tenant reconciler runs on every node's gateway, and each of them replayed
-- every owed cleanup: N gateways sent the same teardown, a replay could land
-- after the row was cleared and the name created again on that node, and the
-- attempt counter grew N times as fast as the retries it limits. A gateway now
-- claims a row (an UPDATE that succeeds for one claimer only) before it replays
-- it; the claim lapses at claimed_until, so a gateway that died holding one
-- does not stop the replay for longer than the lease.
--
-- claimed_until is NULL while a row is not claimed. The runner treats a
-- repeated ADD COLUMN as applied, so a re-run changes nothing.

ALTER TABLE namespace_pending_cleanup ADD COLUMN claimed_until TIMESTAMP;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (69);
