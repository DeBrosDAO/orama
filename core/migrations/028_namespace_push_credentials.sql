-- =============================================================================
-- 028_namespace_push_credentials.sql
--
-- Per-namespace, per-provider push credentials. Generic schema so any
-- future provider (apns, fcm, sms, …) plugs in with zero migration —
-- the credentials_json BLOB is an opaque AES-256-GCM ciphertext owned
-- by the provider package; this table knows nothing about the schema
-- inside.
--
-- Feature #72 (full-privacy push: APNs-direct + self-hosted ntfy).
--
-- Why a separate table from 026 (namespace_push_config)?
--   * 026 holds delivery PREFERENCES (ntfy_base_url, etc.) — non-secret
--     toggles a tenant flips often.
--   * 028 holds CREDENTIALS (Apple p8 key, ntfy auth token, future FCM
--     service-account JSON) — sensitive material with a different
--     access pattern (less-frequently updated, always encrypted).
--   Splitting keeps the audit story clean and lets us add per-provider
--   credentials without bloating 026's columns each time.
--
-- Encryption: credentials_json is AES-256-GCM ciphertext via pkg/secrets
-- with HKDF purpose string "namespace-push-credentials". The blob holds
-- a provider-specific JSON document (see each provider package for its
-- own schema and Validator).
-- =============================================================================

CREATE TABLE IF NOT EXISTS namespace_push_credentials (
    namespace          TEXT NOT NULL,
    provider           TEXT NOT NULL,      -- "apns" | "ntfy" | "expo" | future
    credentials_json   TEXT NOT NULL,      -- enc:<base64(AES-256-GCM ciphertext)>
    updated_at         INTEGER NOT NULL,   -- unix seconds
    updated_by         TEXT,               -- audit: wallet/operator id
    PRIMARY KEY (namespace, provider)
);
