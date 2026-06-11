package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func getEnvBoolDefault(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

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
		ListenAddr            string        `yaml:"listen_addr"`
		ClientNamespace       string        `yaml:"client_namespace"`
		RQLiteDSN             string        `yaml:"rqlite_dsn"`
		GlobalRQLiteDSN       string        `yaml:"global_rqlite_dsn"`
		Peers                 []string      `yaml:"bootstrap_peers"`
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
		// ClusterSecretPath: see GatewayYAMLConfig docstring. Optional;
		// when set, the standalone gateway reads the file at this path
		// and populates cfg.ClusterSecret so JWT signing keys can be
		// derived deterministically (bug #215 fix).
		ClusterSecretPath string `yaml:"cluster_secret_path"`
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
		ListenAddr:            ":6001",
		ClientNamespace:       "default",
		BootstrapPeers:        nil,
		RQLiteDSN:             "",
		GlobalRQLiteDSN:       "",
		EnableHTTPS:           false,
		DomainName:            "",
		TLSCacheDir:           "",
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
	if v := strings.TrimSpace(y.RQLiteDSN); v != "" {
		cfg.RQLiteDSN = v
	}
	if v := strings.TrimSpace(y.GlobalRQLiteDSN); v != "" {
		cfg.GlobalRQLiteDSN = v
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

	// HTTPS configuration
	cfg.EnableHTTPS = y.EnableHTTPS
	if v := strings.TrimSpace(y.DomainName); v != "" {
		cfg.DomainName = v
	}
	if v := strings.TrimSpace(y.TLSCacheDir); v != "" {
		cfg.TLSCacheDir = v
	} else if cfg.EnableHTTPS {
		// Default TLS cache directory if HTTPS is enabled but not specified
		homeDir, err := os.UserHomeDir()
		if err == nil {
			cfg.TLSCacheDir = filepath.Join(homeDir, ".orama", "tls-cache")
		}
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

	// Cluster secret — bug #215 fix. The host-managed gateway in
	// pkg/node/gateway.go reads this from a known on-disk path; the
	// standalone binary (used by namespace gateways via systemd) needs the
	// same access so it can derive the cluster-wide Ed25519 JWT signing
	// key. Without this, namespace gateways had per-node random keys and
	// JWTs minted on one node were unverifiable on another, leaving
	// `caller_jwt_subject` empty in serverless host functions.
	if path := strings.TrimSpace(y.ClusterSecretPath); path != "" {
		secretBytes, err := os.ReadFile(path)
		if err != nil {
			logger.ComponentError(logging.ComponentGeneral,
				"cluster_secret_path is set but the file is unreadable; "+
					"JWTs will use a per-node random signing key and will not "+
					"verify cross-node — bug #215 will reproduce",
				zap.String("path", path),
				zap.Error(err))
		} else {
			cfg.ClusterSecret = strings.TrimSpace(string(secretBytes))
			logger.ComponentInfo(logging.ComponentGeneral,
				"Loaded cluster secret for cluster-wide JWT signing key derivation",
				zap.String("path", path))
		}
	}

	// Serverless secrets encryption key — bugboard #837 follow-up. The
	// host-managed gateway (pkg/node/gateway.go) reads this from
	// secrets/secrets-encryption-key; the standalone binary used by namespace
	// gateways via systemd receives it through this YAML field. Without it,
	// `function secrets list` returned 501 ("Secrets management not
	// available") on namespace gateways even though the host had the key.
	if v := strings.TrimSpace(y.SecretsEncryptionKey); v != "" {
		cfg.SecretsEncryptionKey = v
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
