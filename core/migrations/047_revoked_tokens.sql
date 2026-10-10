-- Migration 047: a place to record tokens that must stop being accepted.
--
-- Revoking an API key did nothing to the JWTs already exchanged from it. The
-- key stopped authenticating, but every token minted from it went on working
-- until it expired — up to fifteen minutes of full access after an operator
-- had revoked the credential and been told it was done. There was also no way
-- to end a single session at all: `logout` dropped the refresh token and left
-- the access token valid.
--
-- A row here denies either one token, by its `jti`, or every token issued to a
-- subject before a moment in time. The second form is what key revocation
-- writes: one row covers every outstanding token from that key without having
-- to have recorded each one, and a token minted afterwards is a new grant that
-- the row deliberately does not cover.
--
-- `expires_at` is when the last token a row could deny has itself expired.
-- After that the row denies nothing and is deleted; the table stays the size of
-- the revocations still in flight rather than growing forever.
BEGIN;

CREATE TABLE IF NOT EXISTS revoked_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Exactly one of these is set. jti denies one token; subject denies every
    -- token issued to that subject before issued_before.
    jti TEXT,
    subject TEXT,

    -- Tokens with iat strictly before this are denied. Set to the moment of
    -- revocation for a subject; 0 for a jti, where the token is named outright.
    issued_before INTEGER NOT NULL DEFAULT 0,

    -- Unix seconds. Past this the row denies nothing and is pruned.
    expires_at INTEGER NOT NULL,

    reason TEXT,
    revoked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- The read path loads every live row on refresh, so the index that matters is
-- the one the prune uses.
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires ON revoked_tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_jti ON revoked_tokens(jti);
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_subject ON revoked_tokens(subject);

COMMIT;
