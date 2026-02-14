-- Orama Gateway - Initial database schema (SQLite/RQLite dialect)
-- This file scaffolds core tables used by the HTTP gateway for auth, observability, and namespacing.
-- Apply via your migration tooling or manual execution in RQLite.

BEGIN;

-- Tracks applied migrations (optional if your runner manages this separately)
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Namespaces (tenant/app isolation)
CREATE TABLE IF NOT EXISTS namespaces (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- API keys (basic authentication/authorization scaffold)
CREATE TABLE IF NOT EXISTS api_keys (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    key            TEXT NOT NULL UNIQUE,
    name           TEXT,
    namespace_id   INTEGER NOT NULL,
    scopes         TEXT, -- comma-separated or JSON array; refine later
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at   TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_keys_namespace ON api_keys(namespace_id);

-- Request logs (simple observability; expand with more fields later)
CREATE TABLE IF NOT EXISTS request_logs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    status_code   INTEGER NOT NULL,
    bytes_out     INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    ip            TEXT,
    api_key_id    INTEGER,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_request_logs_api_key ON request_logs(api_key_id);
CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at);

-- Seed a default namespace for development convenience
INSERT OR IGNORE INTO namespaces(name) VALUES ('default');

-- Mark this migration as applied (optional)
INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);

COMMIT;
