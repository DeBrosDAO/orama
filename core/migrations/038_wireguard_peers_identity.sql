-- Give wireguard_peers a real identity and a confirmation flag.
--
-- node_id was written as a synthetic "node-<wgip>" by the join handler, so it
-- matched no dns_nodes.id and never has. Every consumer that wanted to know
-- which machine a row belonged to had to join on wg_ip instead, and the
-- membership reconciler cannot key on an id that means nothing.
--
-- confirmed_at separates "a join handshake wrote this row" from "the node
-- actually came up". A join that failed after the row was written left a ghost
-- every survivor added to wg0 every 60 seconds for ever, with nothing able to
-- tell it apart from a live peer.
--
-- Rolling-upgrade window: the node's WireGuard self-registration writes
-- confirmed_at, and it runs outside the quorum gate (raft runs over the mesh,
-- so gating the mesh repair on consensus would make the repair conditional on
-- the thing it repairs). On the FIRST node upgraded, that write can therefore
-- fire before this migration has been applied by anyone, and fails with
-- "no such column". It is transient and self-correcting: the node's existing
-- row is untouched and still confirmed, and the sync loop's next tick — after
-- rqlite-cluster has applied this migration — writes it. No node is severed,
-- because a row is only ever dropped for being unconfirmed when it ALSO
-- matches no dns_nodes row and is older than the 30-minute join grace.

BEGIN;

ALTER TABLE wireguard_peers ADD COLUMN confirmed_at TIMESTAMP;

-- Existing rows are confirmed: they belong to nodes that are running now, and
-- marking them unconfirmed would have the reconciler delete the live mesh.
UPDATE wireguard_peers SET confirmed_at = CURRENT_TIMESTAMP WHERE confirmed_at IS NULL;

-- Backfill the real peer id where dns_nodes knows it. Rows whose overlay
-- address matches no node keep their synthetic id; the reconciler reports
-- those rather than guessing.
UPDATE wireguard_peers
   SET node_id = (SELECT n.id FROM dns_nodes n WHERE n.internal_ip = wireguard_peers.wg_ip)
 WHERE EXISTS (SELECT 1 FROM dns_nodes n WHERE n.internal_ip = wireguard_peers.wg_ip)
   AND node_id LIKE 'node-%';

INSERT OR IGNORE INTO schema_migrations(version) VALUES (38);

COMMIT;
