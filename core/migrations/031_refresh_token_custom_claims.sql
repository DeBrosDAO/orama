-- =============================================================================
-- 031_refresh_token_custom_claims.sql
--
-- Carry the additive JWT custom claims (e.g. the namespace's account_id from
-- the auth-claims-provider hook, bugboard #548/#920) ALONGSIDE the refresh
-- token, so a rotated access token keeps the same claims without re-invoking
-- the namespace's claims-provider function on every 15-min refresh (the
-- refresh path is the latency-critical VoIP-wake path, bugboard #125).
--
-- Resolved once at /v1/auth/verify mint time, stored here, and replayed +
-- propagated across each rotation. NULL/absent = no custom claims (the default
-- for every namespace without a claims-provider) → fully backward compatible.
-- =============================================================================

ALTER TABLE refresh_tokens ADD COLUMN custom_claims TEXT;
