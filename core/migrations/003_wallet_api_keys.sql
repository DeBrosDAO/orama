-- Orama Gateway - Wallet to API Key linkage (Phase 3)
-- Ensures one API key per (namespace, wallet) and enables lookup

BEGIN;

CREATE TABLE IF NOT EXISTS wallet_api_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    wallet        TEXT NOT NULL,
    api_key_id    INTEGER NOT NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace_id, wallet),
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE,
    FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_wallet_api_keys_ns ON wallet_api_keys(namespace_id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (3);

COMMIT;
