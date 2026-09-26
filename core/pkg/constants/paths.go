package constants

import "path/filepath"

// The on-disk layout under a node's orama directory (/opt/orama/.orama in
// production), for everything a gateway or the shared TURN server writes.
//
// Every gateway runs as orama-namespace-gateway@<ns>: the orama user, with
// ProtectSystem=strict, and only data/ (and its own data/namespaces/<ns>)
// writable. secrets/ and configs/ are read-only to it. Anything a gateway
// writes at runtime therefore has to live under data/, and these are the only
// places it does. They are defined once here because the gateway, the
// namespace spawner, the systemd templates, orama-node's legacy-layout migration
// and the node report all have to agree on them exactly.
const (
	// IndexNamespace is the host/cluster gateway's instance name
	// (orama-namespace-gateway@index) and its client_namespace.
	IndexNamespace = "index"

	// DataSubdir is the writable tree under the orama directory.
	DataSubdir = "data"
	// NamespacesSubdir holds one directory per namespace instance on this host.
	NamespacesSubdir = "namespaces"
	// GatewayStateSubdir is a gateway's state directory inside its namespace
	// directory: its own signing keys and its encryption-root cache. One per
	// gateway, so two gateways on one host never load the same key file. It
	// is not a privilege boundary: every gateway runs as the orama user.
	GatewayStateSubdir = "gateway"
	// SQLiteSubdir holds tenant SQLite databases, one directory per namespace.
	SQLiteSubdir = "sqlite"
	// DeploymentsSubdir holds deployment trees, one per orama-deploy-*@ instance.
	DeploymentsSubdir = "deployments"
	// TURNSubdir holds the shared host TURN server's config.
	TURNSubdir = "turn"
	// TURNConfigFileName is the shared host TURN server's config file.
	TURNConfigFileName = "turn.yaml"

	// GatewayStateDirMode is the state directory's mode: it holds private keys.
	GatewayStateDirMode = 0o700
	// GatewayRSAKeyFileName is a gateway's RSA JWT signing key, in its state
	// directory.
	GatewayRSAKeyFileName = "jwt-signing-key.pem"
	// GatewayEdDSAKeyFileName is a gateway's own Ed25519 JWT signing key, in
	// its state directory.
	GatewayEdDSAKeyFileName = "jwt-eddsa-key.pem"
)

// DataDir is <oramaDir>/data.
func DataDir(oramaDir string) string {
	return filepath.Join(oramaDir, DataSubdir)
}

// NamespacesDir is <oramaDir>/data/namespaces.
func NamespacesDir(oramaDir string) string {
	return filepath.Join(DataDir(oramaDir), NamespacesSubdir)
}

// GatewayStateDir is <namespacesDir>/<namespace>/gateway, the state directory
// of the gateway serving namespace on this host.
func GatewayStateDir(namespacesDir, namespace string) string {
	return filepath.Join(namespacesDir, namespace, GatewayStateSubdir)
}

// SQLiteBaseDir is <oramaDir>/data/sqlite.
func SQLiteBaseDir(oramaDir string) string {
	return filepath.Join(DataDir(oramaDir), SQLiteSubdir)
}

// DeploymentsBaseDir is <oramaDir>/data/deployments — the directory the
// orama-deploy-{go,node,npm}@ templates run each deployment from.
func DeploymentsBaseDir(oramaDir string) string {
	return filepath.Join(DataDir(oramaDir), DeploymentsSubdir)
}

// HostTURNConfigPath is <oramaDir>/data/turn/turn.yaml — the TURN_CONFIG that
// orama-turn.service runs with.
func HostTURNConfigPath(oramaDir string) string {
	return filepath.Join(DataDir(oramaDir), TURNSubdir, TURNConfigFileName)
}
