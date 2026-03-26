-- Migration 006: Per-Namespace SQLite Databases
-- This migration creates infrastructure for isolated SQLite databases per namespace

BEGIN;

-- Namespace SQLite databases registry
CREATE TABLE IF NOT EXISTS namespace_sqlite_databases (
    id TEXT PRIMARY KEY,                    -- UUID
    namespace TEXT NOT NULL,                -- Namespace that owns this database
    database_name TEXT NOT NULL,            -- Database name (unique per namespace)
    home_node_id TEXT NOT NULL,             -- Node ID where database file resides
    file_path TEXT NOT NULL,                -- Absolute path on home node
    size_bytes BIGINT DEFAULT 0,            -- Current database size
    backup_cid TEXT,                        -- Latest backup CID in IPFS
    last_backup_at TIMESTAMP,               -- Last backup timestamp
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL,               -- Wallet address that created the database

    UNIQUE(namespace, database_name)
);

-- Indexes for database lookups
CREATE INDEX IF NOT EXISTS idx_sqlite_databases_namespace ON namespace_sqlite_databases(namespace);
CREATE INDEX IF NOT EXISTS idx_sqlite_databases_home_node ON namespace_sqlite_databases(home_node_id);
CREATE INDEX IF NOT EXISTS idx_sqlite_databases_name ON namespace_sqlite_databases(namespace, database_name);

-- SQLite database backups history
CREATE TABLE IF NOT EXISTS namespace_sqlite_backups (
    id TEXT PRIMARY KEY,                    -- UUID
    database_id TEXT NOT NULL,              -- References namespace_sqlite_databases.id
    backup_cid TEXT NOT NULL,               -- IPFS CID of backup file
    size_bytes BIGINT NOT NULL,             -- Backup file size
    backup_type TEXT NOT NULL,              -- 'manual', 'scheduled', 'migration'
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL,

    FOREIGN KEY (database_id) REFERENCES namespace_sqlite_databases(id) ON DELETE CASCADE
);

-- Index for backup history queries
CREATE INDEX IF NOT EXISTS idx_sqlite_backups_database ON namespace_sqlite_backups(database_id, created_at DESC);

-- Namespace quotas for resource management (future use)
CREATE TABLE IF NOT EXISTS namespace_quotas (
    namespace TEXT PRIMARY KEY,

    -- Storage quotas
    max_sqlite_databases INTEGER DEFAULT 10,        -- Max SQLite databases per namespace
    max_storage_bytes BIGINT DEFAULT 5368709120,   -- 5GB default
    max_ipfs_pins INTEGER DEFAULT 1000,             -- Max pinned IPFS objects

    -- Compute quotas
    max_deployments INTEGER DEFAULT 20,             -- Max concurrent deployments
    max_cpu_percent INTEGER DEFAULT 200,            -- Total CPU quota (2 cores)
    max_memory_mb INTEGER DEFAULT 2048,             -- Total memory quota

    -- Rate limits
    max_rqlite_queries_per_minute INTEGER DEFAULT 1000,
    max_olric_ops_per_minute INTEGER DEFAULT 10000,

    -- Current usage (updated periodically)
    current_storage_bytes BIGINT DEFAULT 0,
    current_deployments INTEGER DEFAULT 0,
    current_sqlite_databases INTEGER DEFAULT 0,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Mark migration as applied
INSERT OR IGNORE INTO schema_migrations(version) VALUES (6);

COMMIT;
