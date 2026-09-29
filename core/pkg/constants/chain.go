package constants

// The Orama L1 (orama-global-chain) listens on its own 31000 block, outside
// the index block (10100–10199) and the tenant block (10000–10099).
//
// These values mirror chain/scripts/stagenet/deploy.sh, which writes them into
// the chain's config.toml and app.toml. core reads the chain only over these
// ports and must never import chain types: the chain is a separate Go module
// with its own dependency tree, and core talking to it through its RPC keeps
// the two releasable apart.
const (
	// ChainP2PPort is CometBFT's peer-to-peer listener, on the WireGuard address.
	ChainP2PPort = 31000
	// ChainRPCPort is CometBFT's JSON-RPC server, on 127.0.0.1 only.
	ChainRPCPort = 31001
	// ChainGRPCPort is the Cosmos SDK gRPC server.
	ChainGRPCPort = 31002
	// ChainAPIPort is the Cosmos SDK REST API.
	ChainAPIPort = 31003
	// ChainPrometheusPort is CometBFT's Prometheus metrics listener, on 127.0.0.1.
	ChainPrometheusPort = 31004

	// ChainServiceUnit is the systemd unit that runs the chain node.
	ChainServiceUnit = "orama-global-chain.service"

	// ChainHome is the chain node's home directory (oramad --home). It is
	// also cosmovisor's DAEMON_HOME: the binaries live in ChainHome/cosmovisor.
	ChainHome = "/var/lib/orama-global/chain"
	// ChainUser is the account the chain unit runs as.
	ChainUser = "orama-chain"
	// ChainDaemonName is the chain binary, cosmovisor's DAEMON_NAME.
	ChainDaemonName = "oramad"
	// ChainDenom is the chain's base denom, which `oramad init` is given.
	ChainDenom = "norama"
	// ChainNodeKeyPath is CometBFT's node_key.json under ChainHome: the
	// ed25519 key the node's p2p id is derived from.
	ChainNodeKeyPath = ChainHome + "/config/node_key.json"
	// ChainValidatorKeyPath is CometBFT's priv_validator_key.json: the
	// consensus key a validator signs blocks with.
	ChainValidatorKeyPath = ChainHome + "/config/priv_validator_key.json"
	// ChainValidatorStatePath is priv_validator_state.json, the last height,
	// round and step the key signed. CometBFT refuses to sign at or below it.
	ChainValidatorStatePath = ChainHome + "/data/priv_validator_state.json"
	// ChainGenesisPath is the genesis file oramad reads.
	ChainGenesisPath = ChainHome + "/config/genesis.json"
)

// LocalChainRPCURL is the chain's CometBFT RPC on this node. It is bound to
// 127.0.0.1, not localhost, so the address is spelled out.
func LocalChainRPCURL() string { return hostPortURL("127.0.0.1", ChainRPCPort) }

// LocalChainAPIURL is the Cosmos SDK REST API on this node, also on 127.0.0.1.
func LocalChainAPIURL() string { return hostPortURL("127.0.0.1", ChainAPIPort) }
