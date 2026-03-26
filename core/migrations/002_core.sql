-- Orama Gateway - Core schema (Phase 2)
-- Adds apps, nonces, subscriptions, refresh_tokens, audit_events, namespace_ownership
-- SQLite/RQLite dialect

BEGIN;

-- Apps registered within a namespace (optional public key for attestation)
CREATE TABLE IF NOT EXISTS apps (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    app_id        TEXT NOT NULL,
    name          TEXT,
    public_key    TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace_id, app_id),
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_apps_namespace ON apps(namespace_id);

-- Wallet nonces for challenge-response auth
CREATE TABLE IF NOT EXISTS nonces (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    wallet        TEXT NOT NULL,
    nonce         TEXT NOT NULL,
    purpose       TEXT,
    expires_at    TIMESTAMP,
    used_at       TIMESTAMP,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace_id, wallet, nonce),
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_nonces_wallet ON nonces(wallet);
CREATE INDEX IF NOT EXISTS idx_nonces_expires ON nonces(expires_at);

-- Subscriptions to topics or channels for callbacks/notifications
CREATE TABLE IF NOT EXISTS subscriptions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    app_id        INTEGER,
    topic         TEXT NOT NULL,
    endpoint      TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE,
    FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_subscriptions_ns ON subscriptions(namespace_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_topic ON subscriptions(topic);

-- Opaque refresh tokens for JWT
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    subject       TEXT NOT NULL,
    token         TEXT NOT NULL UNIQUE,
    audience      TEXT,
    expires_at    TIMESTAMP,
    revoked_at    TIMESTAMP,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_refresh_subject ON refresh_tokens(subject);
CREATE INDEX IF NOT EXISTS idx_refresh_expires ON refresh_tokens(expires_at);

-- Audit events for security and observability
CREATE TABLE IF NOT EXISTS audit_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    actor         TEXT,
    action        TEXT NOT NULL,
    resource      TEXT,
    ip            TEXT,
    metadata      TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_audit_ns_time ON audit_events(namespace_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events(action);

-- Namespace ownership mapping (who controls a namespace)
CREATE TABLE IF NOT EXISTS namespace_ownership (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    owner_type    TEXT NOT NULL, -- e.g., 'wallet', 'api_key'
    owner_id      TEXT NOT NULL, -- e.g., wallet address or api key string
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace_id, owner_type, owner_id),
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ns_owner_ns ON namespace_ownership(namespace_id);

-- Optional marker (ignored by runner)
INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);

COMMIT;
