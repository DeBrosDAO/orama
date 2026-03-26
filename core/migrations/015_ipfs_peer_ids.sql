-- Store IPFS peer IDs alongside WireGuard peers for automatic swarm discovery
-- Each node registers its IPFS peer ID so other nodes can connect via ipfs swarm connect
ALTER TABLE wireguard_peers ADD COLUMN ipfs_peer_id TEXT DEFAULT '';
