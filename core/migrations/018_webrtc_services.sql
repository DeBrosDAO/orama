-- Migration 018: WebRTC Services (SFU + TURN) for Namespace Clusters
-- Adds per-namespace WebRTC configuration, room tracking, and port allocation
-- WebRTC is opt-in: enabled via `orama namespace enable webrtc`

BEGIN;

-- Per-namespace WebRTC configuration
-- One row per namespace that has WebRTC enabled
CREATE TABLE IF NOT EXISTS namespace_webrtc_config (
    id TEXT PRIMARY KEY,                        -- UUID
    namespace_cluster_id TEXT NOT NULL UNIQUE,   -- FK to namespace_clusters
    namespace_name TEXT NOT NULL,               -- Cached for easier lookups
    enabled INTEGER NOT NULL DEFAULT 1,         -- 1 = enabled, 0 = disabled

    -- TURN authentication
    turn_shared_secret TEXT NOT NULL,           -- HMAC-SHA1 shared secret (base64, 32 bytes)
    turn_credential_ttl INTEGER NOT NULL DEFAULT 600, -- Credential TTL in seconds (default: 10 min)

    -- Service topology
    sfu_node_count INTEGER NOT NULL DEFAULT 3,  -- SFU instances (all 3 nodes)
    turn_node_count INTEGER NOT NULL DEFAULT 2, -- TURN instances (2 of 3 nodes for HA)

    -- Metadata
    enabled_by TEXT NOT NULL,                   -- Wallet address that enabled WebRTC
    enabled_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disabled_at TIMESTAMP,

    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_webrtc_config_namespace ON namespace_webrtc_config(namespace_name);
CREATE INDEX IF NOT EXISTS idx_webrtc_config_cluster ON namespace_webrtc_config(namespace_cluster_id);

-- WebRTC room tracking
-- Tracks active rooms and their SFU node affinity
CREATE TABLE IF NOT EXISTS webrtc_rooms (
    id TEXT PRIMARY KEY,                        -- UUID
    namespace_cluster_id TEXT NOT NULL,         -- FK to namespace_clusters
    namespace_name TEXT NOT NULL,               -- Cached for easier lookups
    room_id TEXT NOT NULL,                      -- Application-defined room identifier

    -- SFU affinity
    sfu_node_id TEXT NOT NULL,                  -- Node hosting this room's SFU
    sfu_internal_ip TEXT NOT NULL,              -- WireGuard IP of SFU node
    sfu_signaling_port INTEGER NOT NULL,        -- SFU WebSocket signaling port

    -- Room state
    participant_count INTEGER NOT NULL DEFAULT 0,
    max_participants INTEGER NOT NULL DEFAULT 100,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Prevent duplicate rooms within a namespace
    UNIQUE(namespace_cluster_id, room_id),
    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_webrtc_rooms_namespace ON webrtc_rooms(namespace_name);
CREATE INDEX IF NOT EXISTS idx_webrtc_rooms_node ON webrtc_rooms(sfu_node_id);
CREATE INDEX IF NOT EXISTS idx_webrtc_rooms_activity ON webrtc_rooms(last_activity);

-- WebRTC port allocations
-- Separate from namespace_port_allocations to avoid breaking existing port blocks
-- Each namespace gets SFU + TURN ports on each node where those services run
CREATE TABLE IF NOT EXISTS webrtc_port_allocations (
    id TEXT PRIMARY KEY,                        -- UUID
    node_id TEXT NOT NULL,                      -- Physical node ID
    namespace_cluster_id TEXT NOT NULL,         -- FK to namespace_clusters
    service_type TEXT NOT NULL,                 -- 'sfu' or 'turn'

    -- SFU ports (when service_type = 'sfu')
    sfu_signaling_port INTEGER,                -- WebSocket signaling port
    sfu_media_port_start INTEGER,              -- Start of RTP media port range
    sfu_media_port_end INTEGER,                -- End of RTP media port range

    -- TURN ports (when service_type = 'turn')
    turn_listen_port INTEGER,                  -- TURN listener port (3478)
    turn_tls_port INTEGER,                     -- TURN TLS port (443/UDP)
    turn_relay_port_start INTEGER,             -- Start of relay port range
    turn_relay_port_end INTEGER,               -- End of relay port range

    allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Prevent overlapping allocations
    UNIQUE(node_id, namespace_cluster_id, service_type),
    FOREIGN KEY (namespace_cluster_id) REFERENCES namespace_clusters(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_webrtc_ports_node ON webrtc_port_allocations(node_id);
CREATE INDEX IF NOT EXISTS idx_webrtc_ports_cluster ON webrtc_port_allocations(namespace_cluster_id);
CREATE INDEX IF NOT EXISTS idx_webrtc_ports_type ON webrtc_port_allocations(service_type);

-- Mark migration as applied
INSERT OR IGNORE INTO schema_migrations(version) VALUES (18);

COMMIT;
