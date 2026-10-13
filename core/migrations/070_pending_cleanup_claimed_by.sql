-- Who holds the lease on a pending cleanup (see 069).
--
-- A gateway released its claim by row id alone. When its lease lapsed in the
-- middle of a replay and another gateway claimed the row, the first gateway's
-- release erased the second one's claim, and a third could then replay the row
-- alongside it. The claim now records its holder (the node id and a random
-- token per claim) in claimed_by, and a release only clears a claim that is
-- still its own.
--
-- claimed_by is NULL while a row is not claimed. The runner treats a repeated
-- ADD COLUMN as applied, so a re-run changes nothing.

ALTER TABLE namespace_pending_cleanup ADD COLUMN claimed_by TEXT;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (70);
