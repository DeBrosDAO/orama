-- WireGuard mesh peer tracking
CREATE TABLE IF NOT EXISTS wireguard_peers (
    node_id TEXT PRIMARY KEY,
    wg_ip TEXT NOT NULL UNIQUE,
    public_key TEXT NOT NULL UNIQUE,
    public_ip TEXT NOT NULL,
    wg_port INTEGER DEFAULT 51820,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
