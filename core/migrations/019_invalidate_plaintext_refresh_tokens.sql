-- Invalidate refresh tokens that were stored in plaintext.
-- Tokens were stored as-is; the application now stores SHA-256 hashes.
-- Users holding a plaintext token re-authenticate (30-day expiry anyway).
--
-- The WHERE clause narrows this to tokens that are ACTUALLY plaintext, and that
-- is load-bearing rather than tidiness. This was
-- `WHERE revoked_at IS NULL` alone, which revokes every live token every time it
-- runs — and it runs again whenever a previous apply died before recording the
-- version, or on any node that reached it with a stale view of what had been
-- applied. The effect is a silent fleet-wide logout of everyone who signed in
-- since the first run. A stored hash is exactly 64 lowercase hex characters, so
-- anything else is the plaintext this migration exists to invalidate, and a
-- re-run matches nothing.
UPDATE refresh_tokens
   SET revoked_at = datetime('now')
 WHERE revoked_at IS NULL
   AND (length(token) != 64 OR lower(token) GLOB '*[^0-9a-f]*');
