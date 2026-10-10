# Configuration

> **At a glance.**
>
> - **Generated** from the YAML-tagged Go types that decode each file by `make whitepaper-gen`. Do not edit by hand: the gate fails when this file and the code disagree.


## node.yaml

The node's configuration, written by `orama maint node install` and read by `orama-node`. Decoded by `core/pkg/config:Config`.

| Key | Type | Meaning |
|---|---|---|
| `node` | `config.NodeConfig` |  |
| `node.id` | `string` | Auto-generated if empty |
| `node.listen_addresses` | `[]string` | LibP2P listen addresses |
| `node.data_dir` | `string` | Data directory |
| `node.max_connections` | `int` | Maximum peer connections |
| `node.domain` | `string` | Domain for this node (e.g., node-1.orama.network) |
| `node.public_ip` | `string` | Public address (install --vps-ip, re-recorded by every upgrade); an invite minted here names it |
| `node.ssh_user` | `string` | SSH user for remote management |
| `node.environment` | `string` | Environment name (devnet, testnet, etc.) |
| `node.operator_wallet` | `string` | Operator wallet address |
| `node.role` | `string` | cluster (empty default), global, or both (co-located; needs the netns layout) |
| `database` | `config.DatabaseConfig` |  |
| `database.data_dir` | `string` |  |
| `database.replication_factor` | `int` |  |
| `database.shard_count` | `int` |  |
| `database.max_database_size` | `int64` | In bytes |
| `database.backup_interval` | `time.Duration` |  |
| `database.rqlite_port` | `int` | RQLite-specific configuration |
| `database.rqlite_raft_port` | `int` | RQLite Raft consensus port |
| `database.rqlite_join_address` | `string` | Address to join RQLite cluster |
| `database.node_cert` | `string` | RQLite node-to-node TLS encryption (for inter-node Raft communication) See: https://rqlite.io/docs/guides/security/#encrypting-node-to-node-communication |
| `database.node_key` | `string` | Path to X.509 private key for node-to-node communication |
| `database.node_ca_cert` | `string` | Path to CA certificate (optional, uses system CA if not set) |
| `database.node_no_verify` | `bool` | Skip certificate verification (for testing/self-signed certs) |
| `database.rqlite_username` | `string` | RQLite HTTP Basic Auth credentials, used by every client this node opens — the SQL DSN and the admin API (AdminClient) — and by the orama CLI and installer, which read them from node.yaml (rqlite.IndexEndpoint). rqlited always runs with -auth, so they are required. |
| `database.rqlite_password` | `string` |  |
| `database.rqlite_auth_file` | `string` | RQLiteAuthFile is the rqlite auth JSON that carries the same user. It is the file rqlited is pointed at when RQLiteEnforceAuth is set. |
| `database.rqlite_enforce_auth` | `bool` | RQLiteEnforceAuth starts rqlited with `-auth`, making it reject unauthenticated requests. Separate from RQLiteAuthFile on purpose. The two used to be one setting, so the only way to give clients credentials was to simultaneously start refusing everyone who had none — including every peer still running the previous release, whose /join, /status and /remove calls would 401 in the middle of a rolling upgrade and look exactly like raft breaking. The rollout is therefore two passes: first every node ships credentials (RQLiteAuthFile, enforcement off), then enforcement is switched on. |
| `database.raft_election_timeout` | `time.Duration` | Raft tuning (passed through to rqlited CLI flags). Higher defaults than rqlited's 1s suit WireGuard latency. |
| `database.raft_heartbeat_timeout` | `time.Duration` | default: 2s |
| `database.raft_apply_timeout` | `time.Duration` | default: 30s |
| `database.raft_leader_lease_timeout` | `time.Duration` | default: 2s (must be &lt;= heartbeat timeout) |
| `database.cluster_sync_interval` | `time.Duration` | Dynamic discovery configuration (always enabled) |
| `database.peer_inactivity_limit` | `time.Duration` | default: 24h |
| `database.min_cluster_size` | `int` | default: 1 |
| `database.olric_http_port` | `int` | Olric cache configuration |
| `database.olric_memberlist_port` | `int` | Olric memberlist port (default: constants.OlricMemberlistPort) |
| `database.ipfs` | `config.IPFSConfig` | IPFS storage configuration |
| `database.ipfs.cluster_api_url` | `string` | ClusterAPIURL is the IPFS Cluster HTTP API URL (e.g., "http://localhost:9094") If empty, IPFS storage is disabled for this node |
| `database.ipfs.api_url` | `string` | APIURL is the IPFS HTTP API URL for content retrieval (e.g., "http://localhost:10107") If empty, defaults to "http://localhost:10107" |
| `database.ipfs.timeout` | `time.Duration` | Timeout for IPFS operations If zero, defaults to 60 seconds |
| `database.ipfs.replication_factor` | `int` | ReplicationFactor is the replication factor for pinned content If zero, defaults to 3 |
| `database.ipfs.enable_encryption` | `bool` | EnableEncryption is accepted in node.yaml for DecodeStrict compatibility. No code path encrypts IPFS uploads; the value is ignored. |
| `discovery` | `config.DiscoveryConfig` |  |
| `discovery.bootstrap_peers` | `[]string` | Peer addresses to connect to |
| `discovery.discovery_interval` | `time.Duration` | Discovery announcement interval |
| `discovery.bootstrap_port` | `int` | Default port for peer discovery |
| `discovery.http_adv_address` | `string` | HTTP advertisement address |
| `discovery.raft_adv_address` | `string` | Raft advertisement |
| `discovery.node_namespace` | `string` | Namespace for node identifiers |
| `security` | `config.SecurityConfig` |  |
| `security.enable_tls` | `bool` |  |
| `security.private_key_file` | `string` |  |
| `security.certificate_file` | `string` |  |
| `logging` | `config.LoggingConfig` |  |
| `logging.level` | `string` | debug, info, warn, error |
| `logging.format` | `string` | json, console |
| `logging.output_file` | `string` | Empty for stdout |
| `http_gateway` | `config.HTTPGatewayConfig` |  |
| `http_gateway.enabled` | `bool` | Enable HTTP gateway |
| `http_gateway.listen_addr` | `string` | Address to listen on (e.g., ":8080") |
| `http_gateway.node_name` | `string` | Node name for routing |
| `http_gateway.routes` | `map[string]config.RouteConfig` | Service routes |
| `http_gateway.routes.path_prefix` | `string` | URL path prefix (e.g., "/rqlite/http") |
| `http_gateway.routes.backend_url` | `string` | Backend service URL |
| `http_gateway.routes.timeout` | `time.Duration` | Request timeout |
| `http_gateway.routes.websocket` | `bool` | Support WebSocket upgrades |
| `http_gateway.https` | `config.HTTPSConfig` | HTTPS/TLS configuration |
| `http_gateway.https.enabled` | `bool` | Enable HTTPS (port 443) |
| `http_gateway.https.domain` | `string` | Primary domain (e.g., node-123.orama.network) |
| `http_gateway.https.auto_cert` | `bool` | Use Let's Encrypt for automatic certificate |
| `http_gateway.https.use_self_signed` | `bool` | Use self-signed certificates (pre-generated) |
| `http_gateway.https.cert_file` | `string` | Path to certificate file (if not using auto_cert) |
| `http_gateway.https.key_file` | `string` | Path to key file (if not using auto_cert) |
| `http_gateway.https.cache_dir` | `string` | Directory for Let's Encrypt certificate cache |
| `http_gateway.https.http_port` | `int` | HTTP port for ACME challenge (default: 80) |
| `http_gateway.https.https_port` | `int` | HTTPS port (default: 443) |
| `http_gateway.https.email` | `string` | Email for Let's Encrypt account |
| `http_gateway.sni` | `config.SNIConfig` | SNI-based TCP routing configuration |
| `http_gateway.sni.enabled` | `bool` | Enable SNI-based TCP routing |
| `http_gateway.sni.listen_addr` | `string` | Address to listen on (e.g., ":8443") |
| `http_gateway.sni.routes` | `map[string]string` | SNI hostname -> backend address mapping |
| `http_gateway.sni.cert_file` | `string` | Path to certificate file |
| `http_gateway.sni.key_file` | `string` | Path to key file |
| `http_gateway.client_namespace` | `string` | Full gateway configuration (for API, auth, pubsub) |
| `http_gateway.rqlite_dsn` | `string` | RQLiteDSN is accepted so DecodeStrict still reads node.yaml files rendered before it was dropped from the template. Nothing reads it: the index gateway's DSN is derived from discovery.http_adv_address and the database credentials (rqlite.IndexEndpoint, pkg/node/gateway.go). |
| `http_gateway.olric_servers` | `[]string` | List of Olric server addresses |
| `http_gateway.olric_timeout` | `time.Duration` | Timeout for Olric operations |
| `http_gateway.ipfs_cluster_api_url` | `string` | IPFS Cluster API URL |
| `http_gateway.ipfs_api_url` | `string` | IPFS API URL |
| `http_gateway.ipfs_timeout` | `time.Duration` | Timeout for IPFS operations |
| `http_gateway.base_domain` | `string` | Cluster base domain (e.g. "orama-devnet.network"); no default — the gateway refuses to start without one |
| `http_gateway.secrets_encryption_key` | `string` | SecretsEncryptionKey is the AES-256 key (hex, 64 chars) used to encrypt serverless function secrets at rest. Generated per-cluster and written into node.yaml by Phase 4 config generation. This field MUST exist or strict YAML unmarshal rejects node.yaml entirely and orama-node fails to boot (regression that shipped in v0.122.42: template + secret generator + gateway.Config consumer all landed, but this parse field and the node→gateway mapping were missed). |
| `http_gateway.ntfy_base_url` | `string` | NtfyBaseURL is the shared self-hosted ntfy base URL (e.g. "https://push.orama-devnet.network"). When set, the push ntfy provider fans each publish out to every active push node so a subscriber pinned to any instance by round-robin DNS receives it (bugboard #858). Rendered under http_gateway by Phase 4 config generation as "https://push."+dnsZone — matching the ntfy server + Caddy reverse-proxy host. Empty → no fan-out (single-host delivery, the ~87% loss the fix exists to remove). MUST exist here or the node→gateway mapping cannot populate gateway.Config.NtfyBaseURL. |
| `http_gateway.relay_allowed_suffixes` | `[]string` | RelayAllowedSuffixes are the hosts the anonymous relay (/v1/proxy/relay) may reach: a host equal to or under one of them, port 443. Empty means this cluster's BaseDomain. Another cluster's base domain listed here makes this node a relay for that cluster's fetches (docs/SECURITY.md). |
| `http_gateway.webrtc` | `config.WebRTCConfig` | WebRTC configuration (optional, enabled per-namespace) |
| `http_gateway.webrtc.enabled` | `bool` | Whether this gateway has WebRTC support active |
| `http_gateway.webrtc.sfu_port` | `int` | Local SFU signaling port to proxy to |
| `http_gateway.webrtc.turn_domain` | `string` | TURN domain (e.g., "turn.ns-myapp.dbrs.space") |
| `http_gateway.webrtc.turn_secret` | `string` | HMAC-SHA1 shared secret for TURN credential generation |
| `dns` | `config.DNSConfig` |  |
| `dns.node_names_zone` | `string` | NodeNamesZone is the zone this cluster publishes node identification names under, for example nodes.stagenet.orama.network. A node of the cluster whose nameservers answer the zone reads the chain's claimed names and serves &lt;name>.&lt;zone> as an A or AAAA record per literal IP of the named node. Empty publishes none. It must be a dedicated sub-zone strictly below the cluster's http_gateway.base_domain, never the base domain itself (a claimed name would sit next to the hostnames the cluster publishes there), and the node must have the chain reachable (a co-located global layer). |
| `chain` | `config.ChainConfig` |  |
| `chain.faucet` | `config.FaucetConfig` |  |
| `chain.faucet.enabled` | `bool` | Enabled turns the faucet on. |
| `chain.faucet.key_file` | `string` | KeyFile is the faucet key's file: owned by the gateway's account, mode 0600. Empty is constants.ChainFaucetKeyFile. |
| `sni_router` | `config.SNIRouterConfig` | SNIRouter is the stealth TURN-over-443 SNI router toggle (feat-124). Phase 4 config generation always emits this block into node.yaml, so the field MUST exist here: node.yaml is decoded with KnownFields(true) and an unknown top-level key fails the whole parse and crash-loops orama-node at boot (same failure mode as the v0.122.42 secrets_encryption_key incident). |
| `sni_router.enabled` | `bool` |  |
| `tls` | `config.TLSConfig` | TLS is written only when the node was installed with --acme-ca, and must exist here for the same KnownFields reason. |
| `tls.acme_ca` | `string` | ACMECA is the ACME directory Caddy issues certificates from; empty is Let's Encrypt production. |

## gateway.yaml (namespace gateway)

A namespace gateway's configuration, rendered by the namespace spawner and read by `orama-gateway`. Decoded by `core/pkg/gatewayspec:GatewayYAMLConfig`.

| Key | Type | Meaning |
|---|---|---|
| `listen_addr` | `string` |  |
| `client_namespace` | `string` |  |
| `rqlite_dsn` | `string` |  |
| `global_rqlite_dsn` | `string` |  |
| `rqlite_username` | `string` |  |
| `rqlite_password` | `string` |  |
| `bootstrap_peers` | `[]string` |  |
| `enable_https` | `bool` |  |
| `domain_name` | `string` |  |
| `tls_cache_dir` | `string` |  |
| `olric_servers` | `[]string` |  |
| `olric_timeout` | `string` |  |
| `ipfs_cluster_api_url` | `string` |  |
| `ipfs_api_url` | `string` |  |
| `ipfs_timeout` | `string` |  |
| `ipfs_replication_factor` | `int` |  |
| `webrtc` | `gatewayspec.GatewayYAMLWebRTC` |  |
| `webrtc.enabled` | `bool` |  |
| `webrtc.sfu_port` | `int` |  |
| `webrtc.turn_domain` | `string` |  |
| `webrtc.turn_secret` | `string` |  |
| `webrtc.turn_stealth_domain` | `string` |  |
| `secrets_encryption_key` | `string` |  |
| `ntfy_base_url` | `string` |  |
| `cluster_secret_path` | `string` |  |
| `api_key_hmac_secret` | `string` |  |
| `state_dir` | `string` |  |
| `relay_allowed_suffixes` | `[]string` |  |
| `faucet_key_file` | `string` |  |
