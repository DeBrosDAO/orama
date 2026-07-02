-- 032_refresh_token_reuse_grace.sql
--
-- Bugboard #125: bounded, single-use reuse grace for rotated refresh tokens.
--
-- Refresh-token rotation is single-use: a successful /v1/auth/refresh revokes
-- the presented token and issues a new one. If the rotation RESPONSE is lost
-- in transit (e.g. a reconnect storm during a gateway roll), the client is
-- left holding a just-revoked token and its retry dead-ends in a 401 -> SIWE.
-- On a VoIP-woken locked screen SIWE is impossible, so the call dies.
--
-- grace_used_at lets the gateway accept a just-rotated token ONE more time
-- within a short window (RFC 9700 §4.13.2 reuse grace) and mint a fresh
-- session, while the single-use CAS on this column prevents a stolen token
-- from being replayed repeatedly. NULL = grace not yet consumed.
--
-- Additive ALTER (rolling-upgrade safe): older gateways ignore the column;
-- newer ones read it back NULL for pre-existing rows, which is the correct
-- "grace available" default.

ALTER TABLE refresh_tokens ADD COLUMN grace_used_at TIMESTAMP;
