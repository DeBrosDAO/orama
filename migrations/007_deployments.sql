-- Migration 007: Deployments System
-- This migration creates the complete schema for managing custom deployments
-- (Static sites, Next.js, Go backends, Node.js backends)

BEGIN;

-- Main deployments table
CREATE TABLE IF NOT EXISTS deployments (
    id TEXT PRIMARY KEY,                    -- UUID
    namespace TEXT NOT NULL,                -- Owner namespace
    name TEXT NOT NULL,                     -- Deployment name (unique per namespace)
    type TEXT NOT NULL,                     -- 'static', 'nextjs', 'nextjs-static', 'go-backend', 'go-wasm', 'nodejs-backend'
    version INTEGER NOT NULL DEFAULT 1,     -- Monotonic version counter
    status TEXT NOT NULL DEFAULT 'deploying', -- 'deploying', 'active', 'failed', 'stopped', 'updating'

    -- Content storage
    content_cid TEXT,                       -- IPFS CID for static content or built assets
    build_cid TEXT,                         -- IPFS CID for build artifacts (Next.js SSR, binaries)

    -- Runtime configuration
    home_node_id TEXT,                      -- Node ID hosting stateful data/processes
    port INTEGER,                           -- Allocated port (NULL for static/WASM)
    subdomain TEXT,                         -- Custom subdomain (e.g., myapp)
    environment TEXT,                       -- JSON: {"KEY": "value", ...}

    -- Resource limits
    memory_limit_mb INTEGER DEFAULT 256,
    cpu_limit_percent INTEGER DEFAULT 50,
    disk_limit_mb INTEGER DEFAULT 1024,

    -- Health & monitoring
    health_check_path TEXT DEFAULT '/health', -- HTTP path for health checks
    health_check_interval INTEGER DEFAULT 30, -- Seconds between health checks
    restart_policy TEXT DEFAULT 'always',   -- 'always', 'on-failure', 'never'
    max_restart_count INTEGER DEFAULT 10,   -- Max restarts before marking as failed

    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deployed_by TEXT NOT NULL,              -- Wallet address or API key

    UNIQUE(namespace, name)
);

-- Indexes for deployment lookups
CREATE INDEX IF NOT EXISTS idx_deployments_namespace ON deployments(namespace);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);
CREATE INDEX IF NOT EXISTS idx_deployments_home_node ON deployments(home_node_id);
CREATE INDEX IF NOT EXISTS idx_deployments_type ON deployments(type);
CREATE INDEX IF NOT EXISTS idx_deployments_subdomain ON deployments(subdomain);

-- Port allocations table (prevents port conflicts)
CREATE TABLE IF NOT EXISTS port_allocations (
    node_id TEXT NOT NULL,
    port INTEGER NOT NULL,
    deployment_id TEXT NOT NULL,
    allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (node_id, port),
    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Index for finding allocated ports by node
CREATE INDEX IF NOT EXISTS idx_port_allocations_node ON port_allocations(node_id, port);
CREATE INDEX IF NOT EXISTS idx_port_allocations_deployment ON port_allocations(deployment_id);

-- Home node assignments (namespace → node mapping)
CREATE TABLE IF NOT EXISTS home_node_assignments (
    namespace TEXT PRIMARY KEY,
    home_node_id TEXT NOT NULL,
    assigned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deployment_count INTEGER DEFAULT 0,     -- Cached count for capacity planning
    total_memory_mb INTEGER DEFAULT 0,      -- Cached total memory usage
    total_cpu_percent INTEGER DEFAULT 0     -- Cached total CPU usage
);

-- Index for querying by node
CREATE INDEX IF NOT EXISTS idx_home_node_by_node ON home_node_assignments(home_node_id);

-- Deployment domains (custom domain mapping)
CREATE TABLE IF NOT EXISTS deployment_domains (
    id TEXT PRIMARY KEY,                    -- UUID
    deployment_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    domain TEXT NOT NULL UNIQUE,            -- Full domain (e.g., myapp.orama.network or custom)
    routing_type TEXT NOT NULL DEFAULT 'balanced', -- 'balanced' or 'node_specific'
    node_id TEXT,                           -- For node_specific routing
    is_custom BOOLEAN DEFAULT FALSE,        -- True for user's own domain
    tls_cert_cid TEXT,                      -- IPFS CID for custom TLS certificate
    verified_at TIMESTAMP,                  -- When custom domain was verified
    verification_token TEXT,                -- TXT record token for domain verification
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Indexes for domain lookups
CREATE INDEX IF NOT EXISTS idx_deployment_domains_deployment ON deployment_domains(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_domains_domain ON deployment_domains(domain);
CREATE INDEX IF NOT EXISTS idx_deployment_domains_namespace ON deployment_domains(namespace);

-- Deployment history (version tracking and rollback)
CREATE TABLE IF NOT EXISTS deployment_history (
    id TEXT PRIMARY KEY,                    -- UUID
    deployment_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    content_cid TEXT,
    build_cid TEXT,
    deployed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deployed_by TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'success', -- 'success', 'failed', 'rolled_back'
    error_message TEXT,
    rollback_from_version INTEGER,          -- If this is a rollback, original version

    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Indexes for history queries
CREATE INDEX IF NOT EXISTS idx_deployment_history_deployment ON deployment_history(deployment_id, version DESC);
CREATE INDEX IF NOT EXISTS idx_deployment_history_status ON deployment_history(status);

-- Deployment environment variables (separate for security)
CREATE TABLE IF NOT EXISTS deployment_env_vars (
    id TEXT PRIMARY KEY,                    -- UUID
    deployment_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,                    -- Encrypted in production
    is_secret BOOLEAN DEFAULT FALSE,        -- True for sensitive values
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(deployment_id, key),
    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Index for env var lookups
CREATE INDEX IF NOT EXISTS idx_deployment_env_vars_deployment ON deployment_env_vars(deployment_id);

-- Deployment events log (audit trail)
CREATE TABLE IF NOT EXISTS deployment_events (
    id TEXT PRIMARY KEY,                    -- UUID
    deployment_id TEXT NOT NULL,
    event_type TEXT NOT NULL,               -- 'created', 'started', 'stopped', 'restarted', 'updated', 'deleted', 'health_check_failed'
    message TEXT,
    metadata TEXT,                          -- JSON: additional context
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT,                        -- Wallet address or 'system'

    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Index for event queries
CREATE INDEX IF NOT EXISTS idx_deployment_events_deployment ON deployment_events(deployment_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployment_events_type ON deployment_events(event_type);

-- Process health checks (for dynamic deployments)
CREATE TABLE IF NOT EXISTS deployment_health_checks (
    id TEXT PRIMARY KEY,                    -- UUID
    deployment_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,                   -- 'healthy', 'unhealthy', 'unknown'
    response_time_ms INTEGER,
    status_code INTEGER,
    error_message TEXT,
    checked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

-- Index for health check queries (keep only recent checks)
CREATE INDEX IF NOT EXISTS idx_health_checks_deployment ON deployment_health_checks(deployment_id, checked_at DESC);

-- Mark migration as applied
INSERT OR IGNORE INTO schema_migrations(version) VALUES (7);

COMMIT;
