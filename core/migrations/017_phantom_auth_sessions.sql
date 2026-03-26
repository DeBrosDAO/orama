-- Migration 017: Phantom auth sessions for QR code + deep link authentication
-- Stores session state for the CLI-to-phone relay pattern via the gateway

BEGIN;

CREATE TABLE IF NOT EXISTS phantom_auth_sessions (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    wallet TEXT,
    api_key TEXT,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_phantom_sessions_status ON phantom_auth_sessions(status);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (17);

COMMIT;
