-- =============================================================================
-- 034_api_key_revoked.sql
--
-- Scoped API keys (bugboard #148): soft-revocation for api_keys.
--
-- The `scopes` column already exists (001_initial.sql, previously unused). This
-- migration adds `revoked_at` so a key can be killed without deleting the row —
-- the gateway's key lookup filters `revoked_at IS NULL`, so a revoked key
-- resolves to "invalid API key" (bounded by the 60s middleware cache TTL) while
-- the audit trail (who/when/what scopes) survives.
--
-- This is what makes the cutover safe: after issuing fresh scoped keys and
-- updating every consumer, the operator sweep-revokes all legacy (NULL-scope)
-- omnipotent keys that shipped in released app bundles, in one UPDATE, with no
-- data loss and full reversibility.
-- =============================================================================

ALTER TABLE api_keys ADD COLUMN revoked_at TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_api_keys_revoked_at ON api_keys(revoked_at);
