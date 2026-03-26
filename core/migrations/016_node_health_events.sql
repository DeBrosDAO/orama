-- Migration 016: Node health events for failure detection
-- Tracks peer-to-peer health observations for quorum-based dead node detection

BEGIN;

CREATE TABLE IF NOT EXISTS node_health_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    observer_id TEXT NOT NULL,          -- node that detected the failure
    target_id TEXT NOT NULL,            -- node that is suspect/dead
    status TEXT NOT NULL,               -- 'suspect', 'dead', 'recovered'
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_nhe_target_status ON node_health_events(target_id, status);
CREATE INDEX IF NOT EXISTS idx_nhe_created_at ON node_health_events(created_at);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (16);

COMMIT;
