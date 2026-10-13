-- The credential a node's agent requires from this gateway.
--
-- The OramaOS agent listened on every interface and accepted commands with no
-- authentication at all: restarting any service on any node took one POST from
-- anywhere that could route to it. The agent mints a token for the gateway at
-- enrollment now, requires it on every request, and binds only the overlay
-- address.
--
-- The gateway has to present that token, so it cannot be hashed. It is stored
-- encrypted with a key derived from the cluster secret (HKDF purpose
-- "node-agent-token"), the same treatment the TURN shared secrets get: a
-- registry snapshot yields a blob rather than the ability to command every
-- node.
--
-- Nodes enrolled before this have no token. They keep running; they cannot be
-- commanded until they are re-enrolled, which is the right way round.

ALTER TABLE wireguard_peers ADD COLUMN agent_token TEXT;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (45);
