-- Migration 010: Namespace Clusters for Physical Isolation
-- Creates tables to manage per-namespace RQLite and Olric clusters
-- Each namespace gets its own 3-node cluster for complete isolation

BEGIN;

-- Extend namespaces table with cluster status tracking
-- Note: SQLite doesn't support ADD COLUMN IF NOT EXISTS, so we handle this carefully
-- These columns track the provisioning state of the namespace's dedicated cluster

-- First check if columns exist, if not add them
-- cluster_status: 'none', 'provisioning', 'ready', 'degraded', 'failed', 'deprovisioning'

-- Create a new namespaces table with additional columns if needed
CREATE TABLE IF NOT EXISTS namespaces_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    cluster_status TEXT DEFAULT 'none',
    cluster_created_at TIMESTAMP,
    cluster_ready_at TIMESTAMP
);

-- Copy data from old table if it exists and new columns don't
INSERT OR IGNORE INTO namespaces_new (id, name, created_at, cluster_status)
SELECT id, name, created_at, 'none' FROM namespaces WHERE NOT EXISTS (
    SELECT 1 FROM pragma_table_info('namespaces') WHERE name = 'cluster_status'
);

-- If the column already exists, this migration was partially applied - skip the table swap
-- We'll use a different approach: just ensure the new tables exist

-- Namespace clusters registry
-- One record per namespace that has a dedicated cluster
CREATE TABLE IF NOT EXISTS namespace_clusters (
    id TEXT PRIMARY KEY,                        -- UUID
    namespace_id INTEGER NOT NULL UNIQUE,       -- FK to namespaces
    namespace_name TEXT NOT NULL,               -- Cached for easier lookups
    status TEXT NOT NULL DEFAULT 'provisioning', -- provisioning, ready, degraded, failed, deprovisioning

    -- Cluster configuration
    rqlite_node_count INTEGER NOT NULL DEFAULT 3,
    olric_node_count INTEGER NOT NULL DEFAULT 3,
    gateway_node_count INTEGER NOT NULL DEFAULT 3,

    -- Provisioning metadata
    provisioned_by TEXT NOT NULL,               -- Wallet address that triggered provisioning
    provisioned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready_at TIMESTAMP,
    last_health_check TIMESTAMP,

    -- Error tracking
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,

    FOREIGN KEY (namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_namespace_clusters_status ON namespace_clusters(status);
CREATE INDEX IF NOT EXISTS idx_namespace_clusters_namespace ON namespace_clusters(namespace_id);
CREATE INDEX IF NOT EXISTS idx_namespace_clusters_name ON namespace_clusters(namespace_name);

-- Namespace cluster nodes
-- Tracks which physical nodes host services for each namespace cluster
CREATE TABLE IF NOT EXISTS namespace_cluster_nodes (
    id TEXT PRIMARY KEY,                        -- UUID
    namespace_cluster_id TEXT NOT NULL,         -- FK to namespace_clusters
    node_id TEXT NOT NULL,                      -- FK to dns_nodes (physical node)

    -- Role in the cluster
    -- Each node can have multiple roles (rqlite + olric + gateway)
    role TEXT NOT NULL,                         -- 'rqlite_leader', 'rqlite_follower', 'olric', 'gateway'

    -- Service ports (allocated from reserved range 10000-10099)
    rqlite_http_port INTEGER,                   -- Port for RQLite HTTP API
    rqlite_raft_port INTEGER,                   -- Port for RQLite Raft consensus
    olric_http_port INTEGER,                    -- Port for Olric HTTP API
    olric_memberlist_port INTEGER,              -- Port for Olric memberlist gossip
    gateway_http_port INTEGER,                  -- Port for Gateway HTTP

    -- Service status
    status TEXT NOT NULL DEFAULT 'pending',     -- pending, starting, running, stopped, failed
    process_pid INTEGER,                        -- PID of running process (for local management)
    last_heartbeat TIMESTAMP,
    error_message TEXT,

    -- Join addresses for cluster formation
    rqlite_join_address TEXT,                   -- Address to join RQLite cluster
    olric_peers TEXT,                           -- JSON array of Olric peer addresses

    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(namespace_cluster_id, node_id, role),
    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cluster_nodes_cluster ON namespace_cluster_nodes(namespace_cluster_id);
CREATE INDEX IF NOT EXISTS idx_cluster_nodes_node ON namespace_cluster_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_cluster_nodes_status ON namespace_cluster_nodes(status);
CREATE INDEX IF NOT EXISTS idx_cluster_nodes_role ON namespace_cluster_nodes(role);

-- Namespace port allocations
-- Manages the reserved port range (10000-10099) for namespace services
-- Each namespace instance on a node gets a block of 5 consecutive ports
CREATE TABLE IF NOT EXISTS namespace_port_allocations (
    id TEXT PRIMARY KEY,                        -- UUID
    node_id TEXT NOT NULL,                      -- Physical node ID
    namespace_cluster_id TEXT NOT NULL,         -- Namespace cluster this allocation belongs to

    -- Port block (5 consecutive ports)
    port_start INTEGER NOT NULL,                -- Start of port block (e.g., 10000)
    port_end INTEGER NOT NULL,                  -- End of port block (e.g., 10004)

    -- Individual port assignments within the block
    rqlite_http_port INTEGER NOT NULL,          -- port_start + 0
    rqlite_raft_port INTEGER NOT NULL,          -- port_start + 1
    olric_http_port INTEGER NOT NULL,           -- port_start + 2
    olric_memberlist_port INTEGER NOT NULL,     -- port_start + 3
    gateway_http_port INTEGER NOT NULL,         -- port_start + 4

    allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Prevent overlapping allocations on same node
    UNIQUE(node_id, port_start),
    -- One allocation per namespace per node
    UNIQUE(namespace_cluster_id, node_id),
    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ns_port_alloc_node ON namespace_port_allocations(node_id);
CREATE INDEX IF NOT EXISTS idx_ns_port_alloc_cluster ON namespace_port_allocations(namespace_cluster_id);

-- Namespace cluster events
-- Audit log for cluster provisioning and lifecycle events
CREATE TABLE IF NOT EXISTS namespace_cluster_events (
    id TEXT PRIMARY KEY,                        -- UUID
    namespace_cluster_id TEXT NOT NULL,
    event_type TEXT NOT NULL,                   -- Event types listed below
    node_id TEXT,                               -- Optional: specific node this event relates to
    message TEXT,
    metadata TEXT,                              -- JSON for additional event data
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

-- Event types:
-- 'provisioning_started' - Cluster provisioning began
-- 'nodes_selected' - 3 nodes were selected for the cluster
-- 'ports_allocated' - Ports allocated on a node
-- 'rqlite_started' - RQLite instance started on a node
-- 'rqlite_joined' - RQLite instance joined the cluster
-- 'rqlite_leader_elected' - RQLite leader election completed
-- 'olric_started' - Olric instance started on a node
-- 'olric_joined' - Olric instance joined memberlist
-- 'gateway_started' - Gateway instance started on a node
-- 'dns_created' - DNS records created for namespace
-- 'cluster_ready' - All services ready, cluster is operational
-- 'cluster_degraded' - One or more nodes are unhealthy
-- 'cluster_failed' - Cluster failed to provision or operate
-- 'node_failed' - Specific node became unhealthy
-- 'node_recovered' - Node recovered from failure
-- 'deprovisioning_started' - Cluster deprovisioning began
-- 'deprovisioned' - Cluster fully deprovisioned

CREATE INDEX IF NOT EXISTS idx_cluster_events_cluster ON namespace_cluster_events(namespace_cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cluster_events_type ON namespace_cluster_events(event_type);

-- Global deployment registry
-- Prevents duplicate deployment subdomains across all namespaces
-- Since deployments now use {name}-{random}.{domain}, we track used subdomains globally
CREATE TABLE IF NOT EXISTS global_deployment_subdomains (
    subdomain TEXT PRIMARY KEY,                 -- Full subdomain (e.g., 'myapp-f3o4if')
    namespace TEXT NOT NULL,                    -- Owner namespace
    deployment_id TEXT NOT NULL,                -- FK to deployments (in namespace cluster)
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- No FK to deployments since deployments are in namespace-specific clusters
    UNIQUE(subdomain)
);

CREATE INDEX IF NOT EXISTS idx_global_subdomains_namespace ON global_deployment_subdomains(namespace);
CREATE INDEX IF NOT EXISTS idx_global_subdomains_deployment ON global_deployment_subdomains(deployment_id);

-- Mark migration as applied
INSERT OR IGNORE INTO schema_migrations(version) VALUES (10);

COMMIT;
