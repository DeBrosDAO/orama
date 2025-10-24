package main

import (
	"flag"
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

// parseGatewayConfig loads optional configs/gateway.yaml then applies env and flags.
// Priority: flags > env > yaml > defaults.
func parseGatewayConfig(logger *logging.ColoredLogger) *gateway.Config {
	// Base defaults
	cfg := &gateway.Config{
		ListenAddr:      ":6001",
		ClientNamespace: "default",
		BootstrapPeers:  nil,
		RQLiteDSN:       "",
	}

	// 1) YAML (optional)
	{
		type yamlCfg struct {
			ListenAddr      string   `yaml:"listen_addr"`
			ClientNamespace string   `yaml:"client_namespace"`
			RQLiteDSN       string   `yaml:"rqlite_dsn"`
			BootstrapPeers  []string `yaml:"bootstrap_peers"`
		}
		const path = "configs/gateway.yaml"
		if data, err := os.ReadFile(path); err == nil {
			var y yamlCfg
			// Use strict YAML decoding to reject unknown fields
			if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
				logger.ComponentError(logging.ComponentGeneral, "failed to parse configs/gateway.yaml", zap.Error(err))
				fmt.Fprintf(os.Stderr, "Configuration load error: %v\n", err)
				os.Exit(1)
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
		}
	}

	// 2) Env overrides
	if v := strings.TrimSpace(os.Getenv("GATEWAY_ADDR")); v != "" {
		cfg.ListenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("GATEWAY_NAMESPACE")); v != "" {
		cfg.ClientNamespace = v
	}
	if v := strings.TrimSpace(os.Getenv("GATEWAY_RQLITE_DSN")); v != "" {
		cfg.RQLiteDSN = v
	}
	if v := strings.TrimSpace(os.Getenv("GATEWAY_BOOTSTRAP_PEERS")); v != "" {
		parts := strings.Split(v, ",")
		var bp []string
		for _, part := range parts {
			s := strings.TrimSpace(part)
			if s != "" {
				bp = append(bp, s)
			}
		}
		cfg.BootstrapPeers = bp
	}

	// 3) Flags (override env)
	addr := flag.String("addr", "", "HTTP listen address (e.g., :6001)")
	ns := flag.String("namespace", "", "Client namespace for scoping resources")
	peers := flag.String("bootstrap-peers", "", "Comma-separated bootstrap peers for network client")

	// Do not call flag.Parse() elsewhere to avoid double-parsing
	flag.Parse()

	if a := strings.TrimSpace(*addr); a != "" {
		cfg.ListenAddr = a
	}
	if n := strings.TrimSpace(*ns); n != "" {
		cfg.ClientNamespace = n
	}
	if p := strings.TrimSpace(*peers); p != "" {
		parts := strings.Split(p, ",")
		var bp []string
		for _, part := range parts {
			s := strings.TrimSpace(part)
			if s != "" {
				bp = append(bp, s)
			}
		}
		cfg.BootstrapPeers = bp
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

	logger.ComponentInfo(logging.ComponentGeneral, "Loaded gateway configuration",
		zap.String("addr", cfg.ListenAddr),
		zap.String("namespace", cfg.ClientNamespace),
		zap.Int("bootstrap_peer_count", len(cfg.BootstrapPeers)),
	)

	return cfg
}
