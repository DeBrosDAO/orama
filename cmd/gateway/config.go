package main

import (
	"flag"
	"os"
	"strings"

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

// parseGatewayConfig parses flags and environment variables into GatewayConfig.
// Priority: flags > env > defaults.
func parseGatewayConfig(logger *logging.ColoredLogger) *gateway.Config {
	addr := flag.String("addr", getEnvDefault("GATEWAY_ADDR", ":6001"), "HTTP listen address (e.g., :6001)")
	ns := flag.String("namespace", getEnvDefault("GATEWAY_NAMESPACE", "default"), "Client namespace for scoping resources")
	peers := flag.String("bootstrap-peers", getEnvDefault("GATEWAY_BOOTSTRAP_PEERS", ""), "Comma-separated bootstrap peers for network client")

	// Do not call flag.Parse() elsewhere to avoid double-parsing
	flag.Parse()

	var bootstrap []string
	if p := strings.TrimSpace(*peers); p != "" {
		parts := strings.Split(p, ",")
		for _, part := range parts {
			val := strings.TrimSpace(part)
			if val != "" {
				bootstrap = append(bootstrap, val)
			}
		}
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Loaded gateway configuration",
		zap.String("addr", *addr),
		zap.String("namespace", *ns),
		zap.Int("bootstrap_peer_count", len(bootstrap)),
	)

	return &gateway.Config{
		ListenAddr:      *addr,
		ClientNamespace: *ns,
		BootstrapPeers:  bootstrap,
	}
}
