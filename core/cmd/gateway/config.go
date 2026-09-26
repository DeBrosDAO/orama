package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// parseGatewayConfig loads gateway.yaml from ~/.orama exclusively.
// It accepts an optional --config flag for absolute paths (used by systemd services).
func parseGatewayConfig(logger *logging.ColoredLogger) *gateway.Config {
	// Parse --config flag (optional, for systemd services that pass absolute paths)
	configFlag := flag.String("config", "", "Config file path (absolute path or filename in ~/.orama)")
	flag.Parse()

	// Determine config path
	var configPath string
	var err error
	if *configFlag != "" {
		// If --config flag is provided, use it (handles both absolute and relative paths)
		if filepath.IsAbs(*configFlag) {
			configPath = *configFlag
		} else {
			configPath, err = config.DefaultPath(*configFlag)
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "Failed to determine config path", zap.Error(err))
				fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		// Default behavior: look for gateway.yaml in ~/.orama/data/, ~/.orama/configs/, or ~/.orama/
		configPath, err = config.DefaultPath("gateway.yaml")
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral, "Failed to determine config path", zap.Error(err))
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			os.Exit(1)
		}
	}

	// Load YAML
	type yamlWebRTCCfg struct {
		Enabled    bool   `yaml:"enabled"`
		SFUPort    int    `yaml:"sfu_port"`
		TURNDomain string `yaml:"turn_domain"`
		TURNSecret string `yaml:"turn_secret"`
		// TURNStealthDomain is the neutral stealth TURNS:443 host (feat-124).
		// Maps to cfg.StealthCDNDomain so turn.credentials advertises the
		// stealth rung of the URI ladder.
		TURNStealthDomain string `yaml:"turn_stealth_domain"`
	}

	type yamlCfg struct {
		ListenAddr      string   `yaml:"listen_addr"`
		ClientNamespace string   `yaml:"client_namespace"`
		RQLiteDSN       string   `yaml:"rqlite_dsn"`
		GlobalRQLiteDSN string   `yaml:"global_rqlite_dsn"`
		RQLiteUsername  string   `yaml:"rqlite_username"`
		RQLitePassword  string   `yaml:"rqlite_password"`
		Peers           []string `yaml:"bootstrap_peers"`
		// EnableHTTPS is accepted so DecodeStrict does not reject leftover
		// YAML. The gateway never terminates public TLS (Caddy does). A
		// true value is a config error, not a silent no-op.
		EnableHTTPS           bool          `yaml:"enable_https"`
		DomainName            string        `yaml:"domain_name"`
		TLSCacheDir           string        `yaml:"tls_cache_dir"`
		OlricServers          []string      `yaml:"olric_servers"`
		OlricTimeout          string        `yaml:"olric_timeout"`
		IPFSClusterAPIURL     string        `yaml:"ipfs_cluster_api_url"`
		IPFSAPIURL            string        `yaml:"ipfs_api_url"`
		IPFSTimeout           string        `yaml:"ipfs_timeout"`
		IPFSReplicationFactor int           `yaml:"ipfs_replication_factor"`
		WebRTC                yamlWebRTCCfg `yaml:"webrtc"`
		// SecretsEncryptionKey: see GatewayYAMLConfig docstring. Optional;
		// when set, the standalone gateway populates
		// cfg.SecretsEncryptionKey so serverless function secrets can be
		// encrypted/decrypted (bugboard #837 follow-up). Empty leaves
		// secrets management disabled (fail-loud).
		SecretsEncryptionKey string `yaml:"secrets_encryption_key"`
		// ClusterSecretPath: see GatewayYAMLConfig docstring. Required:
		// the gateway reads the cluster secret from it (JWT signing keys
		// are derived from it, bug #215) and, from the orama directory it
		// is in, the node's identity (NodePeerID) — loadNodeIdentity.
		ClusterSecretPath string `yaml:"cluster_secret_path"`
		// APIKeyHMACSecret: see GatewayYAMLConfig docstring. Optional;
		// when set, the standalone gateway populates cfg.APIKeyHMACSecret
		// so API keys are hashed the same way as the main gateway
		// (bugboard #160 fix). Without it, a namespace gateway cannot
		// authenticate any key from the core registry (which stores
		// HMAC-SHA256 hashes) and would persist any key it issues itself
		// in plaintext.
		APIKeyHMACSecret string `yaml:"api_key_hmac_secret"`
		// NtfyBaseURL: see GatewayYAMLConfig docstring. Optional; when set,
		// the standalone gateway populates cfg.NtfyBaseURL so the ntfy push
		// provider has a server to publish to (bugboard #274). Without it a
		// namespace gateway can only reach ntfy if the tenant's stored
		// credential carries its own base_url.
		NtfyBaseURL string `yaml:"ntfy_base_url"`
		// StateDir: see GatewayYAMLConfig. Required — the gateway's private,
		// writable directory for its signing keys and encryption-root cache.
		StateDir string `yaml:"state_dir"`
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "Config file not found",
			zap.String("path", configPath),
			zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nConfig file not found at %s\n", configPath)
		fmt.Fprintf(os.Stderr, "Generate it using: orama config init --type gateway\n")
		os.Exit(1)
	}

	var y yamlCfg
	// Use strict YAML decoding to reject unknown fields
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		logger.ComponentError(logging.ComponentGeneral, "Failed to parse gateway config", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration parse error: %v\n", err)
		os.Exit(1)
	}

	// Build config from YAML
	cfg := &gateway.Config{
		ListenAddr:            fmt.Sprintf(":%d", constants.GatewayAPIPort),
		ClientNamespace:       "default",
		BootstrapPeers:        nil,
		RQLiteDSN:             "",
		GlobalRQLiteDSN:       "",
		DomainName:            "",
		OlricServers:          nil,
		OlricTimeout:          0,
		IPFSClusterAPIURL:     "",
		IPFSAPIURL:            "",
		IPFSTimeout:           0,
		IPFSReplicationFactor: 0,
	}

	if v := strings.TrimSpace(y.ListenAddr); v != "" {
		cfg.ListenAddr = v
	}
	if v := strings.TrimSpace(y.ClientNamespace); v != "" {
		cfg.ClientNamespace = v
	}
	cfg.StateDir = strings.TrimSpace(y.StateDir)
	if v := strings.TrimSpace(y.RQLiteDSN); v != "" {
		cfg.RQLiteDSN = v
	}
	if v := strings.TrimSpace(y.GlobalRQLiteDSN); v != "" {
		cfg.GlobalRQLiteDSN = v
	}
	if v := strings.TrimSpace(y.RQLiteUsername); v != "" {
		cfg.RQLiteUsername = v
	}
	if v := strings.TrimSpace(y.RQLitePassword); v != "" {
		cfg.RQLitePassword = v
	}
	if len(y.Peers) > 0 {
		var peers []string
		for _, p := range y.Peers {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
		if len(peers) > 0 {
			cfg.BootstrapPeers = peers
		}
	}

	if y.EnableHTTPS {
		logger.ComponentError(logging.ComponentGeneral, "enable_https is not supported; Caddy terminates public TLS")
		fmt.Fprintf(os.Stderr, "\nenable_https is not supported. Public TLS is Caddy (DNS-01).\n")
		os.Exit(1)
	}
	if v := strings.TrimSpace(y.DomainName); v != "" {
		cfg.DomainName = v
		cfg.BaseDomain = v
	}

	// Olric configuration
	if len(y.OlricServers) > 0 {
		cfg.OlricServers = y.OlricServers
	}
	if v := strings.TrimSpace(y.OlricTimeout); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			cfg.OlricTimeout = parsed
		} else {
			logger.ComponentWarn(logging.ComponentGeneral, "invalid olric_timeout, using default", zap.String("value", v), zap.Error(err))
		}
	}

	// IPFS configuration
	if v := strings.TrimSpace(y.IPFSClusterAPIURL); v != "" {
		cfg.IPFSClusterAPIURL = v
	}
	if v := strings.TrimSpace(y.IPFSAPIURL); v != "" {
		cfg.IPFSAPIURL = v
	}
	if v := strings.TrimSpace(y.IPFSTimeout); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			cfg.IPFSTimeout = parsed
		} else {
			logger.ComponentWarn(logging.ComponentGeneral, "invalid ipfs_timeout, using default", zap.String("value", v), zap.Error(err))
		}
	}
	if y.IPFSReplicationFactor > 0 {
		cfg.IPFSReplicationFactor = y.IPFSReplicationFactor
	}

	// Cluster secret — bug #215 fix — and, through its path, the node this
	// gateway runs on. The secret derives the cluster-wide Ed25519 JWT signing
	// key (without it, namespace gateways had per-node random keys and JWTs
	// minted on one node were unverifiable on another). Its path locates the
	// orama directory, hence the node's identity key: without that the gateway
	// has no NodePeerID, and home-node assignment, deployment placement, host
	// TURN and leader locality silently matched no node. So it is required.
	identity, err := loadNodeIdentity(y.ClusterSecretPath)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "Cannot identify the node this gateway runs on", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}
	cfg.ClusterSecret = identity.clusterSecret
	// The orama directory, for READS only (secrets/, identity, node config)
	// and for the shared data/ trees. Nothing this gateway owns is written
	// under it directly: secrets/ and configs/ are read-only to the unit. Its
	// own state goes to StateDir.
	cfg.DataDir = identity.oramaDir
	cfg.NodePeerID = identity.peerID
	logger.ComponentInfo(logging.ComponentGeneral, "Loaded the cluster secret and this node's identity",
		zap.String("path", strings.TrimSpace(y.ClusterSecretPath)), zap.String("node_peer_id", identity.peerID))

	// Serverless secrets encryption key — bugboard #837 follow-up. The
	// host-managed gateway (pkg/node/gateway.go) reads this from
	// secrets/secrets-encryption-key; the standalone binary used by namespace
	// gateways via systemd receives it through this YAML field. Without it,
	// `function secrets list` returned 501 ("Secrets management not
	// available") on namespace gateways even though the host had the key.
	if v := strings.TrimSpace(y.NtfyBaseURL); v != "" {
		cfg.NtfyBaseURL = v
	}
	if v := strings.TrimSpace(y.SecretsEncryptionKey); v != "" {
		cfg.SecretsEncryptionKey = v
	}

	// API key HMAC secret — bugboard #160 fix. The host-managed gateway
	// (pkg/node/gateway.go) reads this from secrets/api-key-hmac-secret; the
	// standalone binary used by namespace gateways via systemd receives it
	// through this YAML field. Without it, HashAPIKey returns keys
	// unhashed, so a namespace gateway can't authenticate core-registry
	// keys (stored as HMAC-SHA256 hashes) and stores any key it issues
	// itself in plaintext.
	if v := strings.TrimSpace(y.APIKeyHMACSecret); v != "" {
		cfg.APIKeyHMACSecret = v
	}

	// WebRTC configuration
	cfg.WebRTCEnabled = y.WebRTC.Enabled
	if y.WebRTC.SFUPort > 0 {
		cfg.SFUPort = y.WebRTC.SFUPort
	}
	if v := strings.TrimSpace(y.WebRTC.TURNDomain); v != "" {
		cfg.TURNDomain = v
	}
	if v := strings.TrimSpace(y.WebRTC.TURNSecret); v != "" {
		cfg.TURNSecret = v
	}
	if v := strings.TrimSpace(y.WebRTC.TURNStealthDomain); v != "" {
		cfg.StealthCDNDomain = v
	}

	if strings.TrimSpace(cfg.APIKeyHMACSecret) == "" {
		fmt.Fprintf(os.Stderr, "\napi_key_hmac_secret is required (bugboard #163).\n")
		fmt.Fprintf(os.Stderr, "Spawn writes it from ~/.orama/secrets/api-key-hmac-secret.\n")
		os.Exit(1)
	}

	// Validate configuration
	if errs := cfg.ValidateConfig(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\nGateway configuration errors (%d):\n", len(errs))
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", err)
		}
		fmt.Fprintf(os.Stderr, "\nPlease fix the configuration and try again.\n")
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Loaded gateway configuration from YAML",
		zap.String("path", configPath),
		zap.String("addr", cfg.ListenAddr),
		zap.String("namespace", cfg.ClientNamespace),
		zap.Int("peer_count", len(cfg.BootstrapPeers)),
	)

	return cfg
}

// nodeIdentity is what a gateway learns from cluster_secret_path about the
// node it runs on.
type nodeIdentity struct {
	clusterSecret string
	oramaDir      string
	peerID        string
}

// loadNodeIdentity reads the cluster secret at clusterSecretPath
// (<oramaDir>/secrets/cluster-secret) and the peer id of the node whose orama
// directory that is.
func loadNodeIdentity(clusterSecretPath string) (nodeIdentity, error) {
	path := strings.TrimSpace(clusterSecretPath)
	if path == "" {
		return nodeIdentity{}, fmt.Errorf("cluster_secret_path is required: it is <oramaDir>/secrets/cluster-secret, " +
			"and it is how this gateway finds the node's identity key (<oramaDir>/data/identity.key) and data " +
			"directory; the namespace spawner writes it into every gateway YAML")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nodeIdentity{}, fmt.Errorf("read cluster secret %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return nodeIdentity{}, fmt.Errorf("cluster secret %s is empty; restore it from another node of this cluster", path)
	}
	oramaDir := filepath.Dir(filepath.Dir(path))
	peerID, err := nodePeerID(oramaDir)
	if err != nil {
		return nodeIdentity{}, err
	}
	return nodeIdentity{clusterSecret: secret, oramaDir: oramaDir, peerID: peerID}, nil
}

// nodePeerID is the libp2p peer id of the node this gateway runs on, from the
// node's identity key. SQLite home-node assignment and deployment placement
// compare it against the node registry; a gateway without it matches no node.
func nodePeerID(oramaDir string) (string, error) {
	path := filepath.Join(oramaDir, "data", "identity.key")
	info, err := encryption.LoadIdentity(path)
	if err != nil {
		return "", fmt.Errorf("read this node's identity %s: %w", path, err)
	}
	return info.PeerID.String(), nil
}
