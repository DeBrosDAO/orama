-- =============================================================================
-- 075_webrtc_admission_generation.sql
--
-- A generation on every WebRTC admission (bugboard #726).
--
-- A kick is judged on an SFU against a join ticket by two gateways' clocks, and
-- a clock cannot tell a ticket issued under the admission a kick revoked from
-- one issued under the admission that replaced it a moment later, so a user
-- admitted again right after a kick was refused as kicked for ten seconds.
-- Every admit now takes the next generation of its (namespace, room, user), a
-- count that only grows; the join ticket carries the generation it was issued
-- on and a kick carries the generation it revoked, and the SFU compares the two
-- numbers where both exist.
--
-- Rows admitted before this migration have generation 0, which reads as "no
-- generation": the SFU judges such a ticket or kick by the clocks, as before.
--
-- The runner treats a repeated ADD COLUMN as applied, so a re-run changes
-- nothing.
-- =============================================================================

ALTER TABLE webrtc_admissions ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (75);
