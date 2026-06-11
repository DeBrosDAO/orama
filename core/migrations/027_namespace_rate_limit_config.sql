-- =============================================================================
-- 027_namespace_rate_limit_config.sql
--
-- Per-namespace gateway rate-limit overrides. Tenants self-serve their own
-- (requests_per_minute, burst) via PUT /v1/namespace/rate-limit without
-- operator involvement (feature #69, same pattern as bug #220's push config).
--
-- A row in this table OVERRIDES the gateway's YAML default for the named
-- namespace. Absence falls back to the YAML default. Operators retain a
-- ceiling: PUT requests that exceed the gateway's `MaxRequestsPerMinute` /
-- `MaxBurst` settings are rejected before reaching this table — tenants
-- cannot raise their own quota past the configured cap.
--
-- All fields are non-secret; no encryption.
-- =============================================================================

CREATE TABLE IF NOT EXISTS namespace_rate_limit_config (
    namespace             TEXT PRIMARY KEY,
    requests_per_minute   INTEGER NOT NULL,
    burst                 INTEGER NOT NULL,
    -- Audit metadata: who set this, and when (last update wins).
    updated_at            INTEGER NOT NULL,
    updated_by            TEXT
);
