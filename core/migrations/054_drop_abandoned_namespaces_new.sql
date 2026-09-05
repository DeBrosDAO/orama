-- Migration 054: remove the abandoned table migration 010 left behind.
--
-- 010 started a table rebuild — create `namespaces_new`, copy into it, swap it
-- over `namespaces` — and then decided not to swap, because the column it was
-- adding might already be there. It kept the half-built table.
--
-- So every database in the fleet, cluster and namespace alike, has carried an
-- empty `namespaces_new` ever since. Nothing has ever read it. It surfaced when
-- the table-placement guard asked which database each of the platform's tables
-- belongs in and there was no answer for this one, which is the guard working:
-- a table nobody can place is usually a table nobody wants.
--
-- Dropping it is safe in a way a rebuild is not: it holds a copy of rows that
-- are still in `namespaces`, and nothing selects from it.

DROP TABLE IF EXISTS namespaces_new;
