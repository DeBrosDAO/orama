-- Migration 005: DNS Records for CoreDNS Integration
-- This migration creates tables for managing DNS records with RQLite backend for CoreDNS

BEGIN;

-- DNS records table for dynamic DNS management
CREATE TABLE IF NOT EXISTS dns_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fqdn TEXT NOT NULL UNIQUE,              -- Fully qualified domain name (e.g., myapp.node-7prvNa.orama.network)
    record_type TEXT NOT NULL DEFAULT 'A',  -- DNS record type: A, AAAA, CNAME, TXT
    value TEXT NOT NULL,                    -- IP address or target value
    ttl INTEGER NOT NULL DEFAULT 300,       -- Time to live in seconds
    namespace TEXT NOT NULL,                -- Namespace that owns this record
    deployment_id TEXT,                     -- Optional: deployment that created this record
    node_id TEXT,                           -- Optional: specific node ID for node-specific routing
    is_active BOOLEAN NOT NULL DEFAULT TRUE,-- Enable/disable without deleting
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL                -- Wallet address or 'system' for auto-created records
);

-- Indexes for fast DNS lookups
CREATE INDEX IF NOT EXISTS idx_dns_records_fqdn ON dns_records(fqdn);
CREATE INDEX IF NOT EXISTS idx_dns_records_namespace ON dns_records(namespace);
CREATE INDEX IF NOT EXISTS idx_dns_records_deployment ON dns_records(deployment_id);
CREATE INDEX IF NOT EXISTS idx_dns_records_node_id ON dns_records(node_id);
CREATE INDEX IF NOT EXISTS idx_dns_records_active ON dns_records(is_active);

-- DNS nodes registry for tracking active nodes
CREATE TABLE IF NOT EXISTS dns_nodes (
    id TEXT PRIMARY KEY,                    -- Node ID (e.g., node-7prvNa)
    ip_address TEXT NOT NULL,               -- Public IP address
    internal_ip TEXT,                       -- Private IP for cluster communication
    region TEXT,                            -- Geographic region
    status TEXT NOT NULL DEFAULT 'active',  -- active, draining, offline
    last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    capabilities TEXT,                      -- JSON: ["wasm", "ipfs", "cache"]
    metadata TEXT,                          -- JSON: additional node info
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for node health monitoring
CREATE INDEX IF NOT EXISTS idx_dns_nodes_status ON dns_nodes(status);
CREATE INDEX IF NOT EXISTS idx_dns_nodes_last_seen ON dns_nodes(last_seen);

-- Reserved domains table to prevent subdomain collisions
CREATE TABLE IF NOT EXISTS reserved_domains (
    domain TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    reserved_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Seed reserved domains
INSERT INTO reserved_domains (domain, reason) VALUES
    ('api.orama.network', 'API gateway endpoint'),
    ('www.orama.network', 'Marketing website'),
    ('admin.orama.network', 'Admin panel'),
    ('ns1.orama.network', 'Nameserver 1'),
    ('ns2.orama.network', 'Nameserver 2'),
    ('ns3.orama.network', 'Nameserver 3'),
    ('ns4.orama.network', 'Nameserver 4'),
    ('mail.orama.network', 'Email service'),
    ('cdn.orama.network', 'Content delivery'),
    ('docs.orama.network', 'Documentation'),
    ('status.orama.network', 'Status page')
ON CONFLICT(domain) DO NOTHING;

-- Mark migration as applied
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (5);

COMMIT;
