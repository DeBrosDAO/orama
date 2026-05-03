-- =============================================================================
-- 023_push_devices.sql
--
-- Per-namespace, per-user push notification device registry.
--
-- token_encrypted is AES-256-GCM ciphertext (prefix 'enc:') derived via
-- pkg/secrets. Tokens are sensitive — they let the holder spam a user's
-- device — so they are never returned via any API or written to logs.
--
-- provider matches a registered push.PushProvider name:
--   'ntfy', 'expo', 'apns', 'fcm' (future), ...
-- =============================================================================

CREATE TABLE IF NOT EXISTS push_devices (
    id              TEXT PRIMARY KEY,
    namespace       TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    device_id       TEXT NOT NULL,
    provider        TEXT NOT NULL,
    token_encrypted TEXT NOT NULL,
    platform        TEXT,
    app_version     TEXT,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    last_seen       INTEGER,
    UNIQUE(namespace, user_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_push_devices_user
    ON push_devices(namespace, user_id);

CREATE INDEX IF NOT EXISTS idx_push_devices_provider
    ON push_devices(provider);
