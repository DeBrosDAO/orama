-- Migration 049: remove the phantom_auth_sessions table.
--
-- 017 created it for the Phantom browser-session flow: the CLI created a
-- session row, showed a QR code, and polled an unauthenticated status endpoint
-- until a phone completed it. The row carried the minted API key in cleartext
-- so the poll could hand it back, which is what 036 was written to contain.
--
-- The flow is gone. Solana wallets sign the same challenge every other wallet
-- signs, through /v1/auth/challenge and /v1/auth/verify, so nothing reads or
-- writes this table any more and its only remaining property is that it once
-- held credentials.
BEGIN;

DROP TABLE IF EXISTS phantom_auth_sessions;

COMMIT;
