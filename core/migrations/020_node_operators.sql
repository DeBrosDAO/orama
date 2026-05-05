-- Add operator wallet tracking to nodes.
-- operator_wallet links nodes to the wallet that provisioned them.

ALTER TABLE dns_nodes ADD COLUMN operator_wallet TEXT;
ALTER TABLE dns_nodes ADD COLUMN environment TEXT DEFAULT 'production';
ALTER TABLE dns_nodes ADD COLUMN ssh_user TEXT DEFAULT 'root';
ALTER TABLE dns_nodes ADD COLUMN role TEXT DEFAULT 'node';

CREATE INDEX IF NOT EXISTS idx_dns_nodes_operator ON dns_nodes(operator_wallet);
CREATE INDEX IF NOT EXISTS idx_dns_nodes_environment ON dns_nodes(environment);

ALTER TABLE wireguard_peers ADD COLUMN operator_wallet TEXT;

ALTER TABLE invite_tokens ADD COLUMN operator_wallet TEXT;
