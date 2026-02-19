package node

import (
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

// startTURNServer initializes and starts the built-in TURN server
func (n *Node) startTURNServer() error {
	if !n.config.TURNServer.Enabled {
		n.logger.ComponentInfo(logging.ComponentNode, "Built-in TURN server disabled")
		return nil
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Starting built-in TURN server")

	// Get shared secret from gateway TURN config or TURNServer config
	sharedSecret := ""
	if n.config.HTTPGateway.TURN != nil && n.config.HTTPGateway.TURN.SharedSecret != "" {
		sharedSecret = n.config.HTTPGateway.TURN.SharedSecret
	}

	if sharedSecret == "" {
		n.logger.ComponentWarn(logging.ComponentNode, "TURN server enabled but no shared_secret configured in http_gateway.turn")
		return nil
	}

	// Build TURN server config
	turnCfg := &turn.Config{
		Enabled:       true,
		ListenAddr:    n.config.TURNServer.ListenAddr,
		PublicIP:      n.config.TURNServer.PublicIP,
		Realm:         n.config.TURNServer.Realm,
		SharedSecret:  sharedSecret,
		CredentialTTL: 24 * 60 * 60, // 24 hours in seconds (will be converted)
		MinPort:       n.config.TURNServer.MinPort,
		MaxPort:       n.config.TURNServer.MaxPort,
		// TLS configuration for TURNS
		TLSEnabled:    n.config.TURNServer.TLSEnabled,
		TLSListenAddr: n.config.TURNServer.TLSListenAddr,
		TLSCertFile:   n.config.TURNServer.TLSCertFile,
		TLSKeyFile:    n.config.TURNServer.TLSKeyFile,
	}

	// Apply defaults
	if turnCfg.ListenAddr == "" {
		turnCfg.ListenAddr = "0.0.0.0:3478"
	}
	if turnCfg.Realm == "" {
		turnCfg.Realm = "orama.network"
	}
	if turnCfg.MinPort == 0 {
		turnCfg.MinPort = 49152
	}
	if turnCfg.MaxPort == 0 {
		turnCfg.MaxPort = 65535
	}
	if turnCfg.TLSListenAddr == "" && turnCfg.TLSEnabled {
		turnCfg.TLSListenAddr = "0.0.0.0:443"
	}

	// Create and start TURN server
	server, err := turn.NewServer(turnCfg, n.logger.Logger)
	if err != nil {
		return err
	}

	if err := server.Start(); err != nil {
		return err
	}

	n.turnServer = server

	n.logger.ComponentInfo(logging.ComponentNode, "Built-in TURN server started",
		zap.String("listen_addr", turnCfg.ListenAddr),
		zap.String("realm", turnCfg.Realm),
		zap.Bool("turns_enabled", turnCfg.TLSEnabled),
	)

	return nil
}
