-- Deployment replicas: tracks which nodes host replicas of each deployment
CREATE TABLE IF NOT EXISTS deployment_replicas (
    deployment_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    port INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (deployment_id, node_id),
    FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_deployment_replicas_node ON deployment_replicas(node_id);
CREATE INDEX IF NOT EXISTS idx_deployment_replicas_status ON deployment_replicas(deployment_id, status);
