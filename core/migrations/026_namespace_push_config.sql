-- =============================================================================
-- 026_namespace_push_config.sql
--
-- Per-namespace push notification provider configuration. Tenants set their
-- own ntfy / expo credentials via PUT /v1/push/config without operator
-- involvement (bug #220 follow-up — self-service tenant config).
--
-- Sensitive credentials (auth tokens) are AES-256-GCM ciphertext via
-- pkg/secrets, prefix 'enc:'. Non-secret URLs (ntfy_base_url) stored
-- plaintext — they leak no security material.
--
-- The gateway YAML config remains as a global fallback / default. A row
-- in this table OVERRIDES the YAML for that namespace; absence falls back.
-- =============================================================================

CREATE TABLE IF NOT EXISTS namespace_push_config (
    namespace                   TEXT PRIMARY KEY,
    -- ntfy provider config (URL is non-secret; auth token is)
    ntfy_base_url               TEXT,
    ntfy_auth_token_encrypted   TEXT,
    -- expo provider config (the access token IS sensitive)
    expo_access_token_encrypted TEXT,
    -- Audit metadata: who set this, and when (last update wins).
    updated_at                  INTEGER NOT NULL,
    updated_by                  TEXT
);
