package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// For transition, alias main.GatewayConfig to pkg/gateway.Config
// server.go will be removed; this keeps compatibility until then.
type GatewayConfig = gateway.Config

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

// parseGatewayConfig loads gateway.yaml from ~/.debros exclusively.
func parseGatewayConfig(logger *logging.ColoredLogger) *gateway.Config {
	// Determine config path
	configPath, err := config.DefaultPath("gateway.yaml")
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "Failed to determine config path", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Load YAML
	type yamlCfg struct {
		ListenAddr      string   `yaml:"listen_addr"`
		ClientNamespace string   `yaml:"client_namespace"`
		RQLiteDSN       string   `yaml:"rqlite_dsn"`
		BootstrapPeers  []string `yaml:"bootstrap_peers"`
		Domain          string   `yaml:"domain"`
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "Config file not found",
			zap.String("path", configPath),
			zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nConfig file not found at %s\n", configPath)
		fmt.Fprintf(os.Stderr, "Generate it using: network-cli config init --type gateway\n")
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
		ListenAddr:      ":6001",
		ClientNamespace: "default",
		BootstrapPeers:  nil,
		RQLiteDSN:       "",
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
	if len(y.BootstrapPeers) > 0 {
		var bp []string
		for _, p := range y.BootstrapPeers {
			p = strings.TrimSpace(p)
			if p != "" {
				bp = append(bp, p)
			}
		}
		if len(bp) > 0 {
			cfg.BootstrapPeers = bp
		}
	}

	if v := strings.TrimSpace(y.Domain); v != "" {
		cfg.Domain = v
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
		zap.Int("bootstrap_peer_count", len(cfg.BootstrapPeers)),
	)

	return cfg
}
