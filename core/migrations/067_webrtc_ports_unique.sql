-- Two WebRTC allocations on one node can no longer hold the same port.
--
-- webrtc_port_allocations was unique only by (node_id, namespace_cluster_id,
-- service_type), so nothing but the allocator's own read of the table kept two
-- namespaces off the same SFU signaling port, media range or TURN relay range.
-- When that read missed a row, the second namespace was given ports the first
-- one's running unit still held, and its unit crash-looped on "address already
-- in use" (found live on stagenet: a new namespace got 30000 / 20000-20499, the
-- block of another namespace's SFU on the same node).
--
-- The allocator now treats a violation of these indexes as a lost race and picks
-- the next free value; any other writer gets the constraint error. The indexes
-- are partial because an SFU row stores 0 for the TURN columns and a TURN row
-- stores 0 for the SFU ones.
--
-- Creating an index fails if the table already holds a duplicate. That is the
-- point: a registry that holds two allocations of one port has to be looked at,
-- not migrated past.

CREATE UNIQUE INDEX IF NOT EXISTS idx_webrtc_ports_sfu_signaling
    ON webrtc_port_allocations(node_id, sfu_signaling_port)
    WHERE service_type = 'sfu' AND sfu_signaling_port > 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_webrtc_ports_sfu_media
    ON webrtc_port_allocations(node_id, sfu_media_port_start)
    WHERE service_type = 'sfu' AND sfu_media_port_start > 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_webrtc_ports_turn_relay
    ON webrtc_port_allocations(node_id, turn_relay_port_start)
    WHERE service_type = 'turn' AND turn_relay_port_start > 0;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (67);
