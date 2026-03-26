-- Migration 011: DNS Nameservers Table
-- Maps NS hostnames (ns1, ns2, ns3) to specific node IDs and IPs
-- Provides stable NS assignment that survives restarts and re-seeding

BEGIN;

CREATE TABLE IF NOT EXISTS dns_nameservers (
    hostname TEXT PRIMARY KEY,         -- e.g., "ns1", "ns2", "ns3"
    node_id TEXT NOT NULL,             -- Peer ID of the assigned node
    ip_address TEXT NOT NULL,          -- IP address of the assigned node
    domain TEXT NOT NULL,              -- Base domain (e.g., "dbrs.space")
    assigned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(node_id, domain)            -- A node can only hold one NS slot per domain
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (11);

COMMIT;
