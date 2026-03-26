-- Migration 009: Update DNS Records to Support Multiple Records per FQDN
-- This allows round-robin A records and multiple NS records for the same domain

BEGIN;

-- SQLite doesn't support DROP CONSTRAINT, so we recreate the table
-- First, create the new table structure
CREATE TABLE IF NOT EXISTS dns_records_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fqdn TEXT NOT NULL,                   -- Fully qualified domain name (e.g., myapp.node-7prvNa.orama.network)
    record_type TEXT NOT NULL DEFAULT 'A',-- DNS record type: A, AAAA, CNAME, TXT, NS, SOA
    value TEXT NOT NULL,                  -- IP address or target value
    ttl INTEGER NOT NULL DEFAULT 300,     -- Time to live in seconds
    priority INTEGER DEFAULT 0,           -- Priority for MX/SRV records, or weight for round-robin
    namespace TEXT NOT NULL DEFAULT 'system', -- Namespace that owns this record
    deployment_id TEXT,                   -- Optional: deployment that created this record
    node_id TEXT,                         -- Optional: specific node ID for node-specific routing
    is_active BOOLEAN NOT NULL DEFAULT TRUE,-- Enable/disable without deleting
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL DEFAULT 'system', -- Wallet address or 'system' for auto-created records
    UNIQUE(fqdn, record_type, value)      -- Allow multiple records of same type for same FQDN, but not duplicates
);

-- Copy existing data if the old table exists
INSERT OR IGNORE INTO dns_records_new (id, fqdn, record_type, value, ttl, namespace, deployment_id, node_id, is_active, created_at, updated_at, created_by)
SELECT id, fqdn, record_type, value, ttl, namespace, deployment_id, node_id, is_active, created_at, updated_at, created_by
FROM dns_records WHERE 1=1;

-- Drop old table and rename new one
DROP TABLE IF EXISTS dns_records;
ALTER TABLE dns_records_new RENAME TO dns_records;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_dns_records_fqdn ON dns_records(fqdn);
CREATE INDEX IF NOT EXISTS idx_dns_records_fqdn_type ON dns_records(fqdn, record_type);
CREATE INDEX IF NOT EXISTS idx_dns_records_namespace ON dns_records(namespace);
CREATE INDEX IF NOT EXISTS idx_dns_records_deployment ON dns_records(deployment_id);
CREATE INDEX IF NOT EXISTS idx_dns_records_node_id ON dns_records(node_id);
CREATE INDEX IF NOT EXISTS idx_dns_records_active ON dns_records(is_active);

-- Mark migration as applied
INSERT OR IGNORE INTO schema_migrations(version) VALUES (9);

COMMIT;
