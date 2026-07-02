-- =============================================================================
-- 033_push_devices_token_fp.sql
--
-- Token-exclusive push registration (bugboard #981).
--
-- token_fp is a deterministic keyed fingerprint of the *plaintext* push token
-- (HMAC-SHA256 over the token, key derived from the cluster secret — see
-- pkg/push.RqliteDeviceStore.tokenFingerprint). token_encrypted uses a random
-- AES-GCM nonce and therefore cannot be matched in SQL, so the fingerprint is
-- what lets the gateway evict any OTHER row carrying the SAME physical token
-- when a device re-registers it under a new owner. The result: one physical
-- APNs/VoIP token maps to at most one active push_devices row, which is the
-- durable cross-account-isolation fix (a push to the wrong account can no
-- longer reach a device whose token now belongs to a different owner).
--
-- The column is nullable and lazily populated: rows written before this
-- migration get a fingerprint the next time they register, or via the one-time
-- best-effort backfill in NewRqliteDeviceStore/BackfillTokenFP. Eviction is
-- ALWAYS namespace-scoped — a physical device legitimately used with two
-- different namespaces (apps) keeps an independent row per namespace.
-- =============================================================================

ALTER TABLE push_devices ADD COLUMN token_fp TEXT;

CREATE INDEX IF NOT EXISTS idx_push_devices_token_fp
    ON push_devices(namespace, token_fp);
