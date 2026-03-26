-- Invalidate all existing refresh tokens.
-- Tokens were stored in plaintext; the application now stores SHA-256 hashes.
-- Users will need to re-authenticate (tokens have 30-day expiry anyway).
UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE revoked_at IS NULL;
