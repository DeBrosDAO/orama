-- Null leftover plaintext API keys in phantom_auth_sessions and drop expired rows.
-- The status endpoint is unauthenticated; completed rows previously served the
-- key forever. Application code now nulls after first read.

BEGIN;

UPDATE phantom_auth_sessions SET api_key = NULL WHERE api_key IS NOT NULL AND api_key != '';
DELETE FROM phantom_auth_sessions WHERE expires_at < datetime('now');

INSERT OR IGNORE INTO schema_migrations(version) VALUES (36);

COMMIT;
