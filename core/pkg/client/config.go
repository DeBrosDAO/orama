package client

import (
	"fmt"
	"time"
)

// ClientConfig represents configuration for network clients
type ClientConfig struct {
	AppName           string        `json:"app_name"`
	DatabaseName      string        `json:"database_name"`
	BootstrapPeers    []string      `json:"peers"`
	DatabaseEndpoints []string      `json:"database_endpoints"`
	GatewayURL        string        `json:"gateway_url"`    // Gateway URL for HTTP API access
	ConnectTimeout    time.Duration `json:"connect_timeout"`
	RetryAttempts     int           `json:"retry_attempts"`
	RetryDelay        time.Duration `json:"retry_delay"`
	QuietMode         bool          `json:"quiet_mode"`     // Suppress debug/info logs
	APIKey            string        `json:"api_key"`        // API key for gateway auth
	JWT               string        `json:"jwt"`            // Optional JWT bearer token
	IdentityPath      string        `json:"identity_path"`  // Path to persistent LibP2P identity key file
}

// DefaultClientConfig returns a default client configuration
func DefaultClientConfig(appName string) *ClientConfig {
	// Base defaults
	peers := DefaultBootstrapPeers()
	endpoints := DefaultDatabaseEndpoints()

	return &ClientConfig{
		AppName:           appName,
		DatabaseName:      fmt.Sprintf("%s_db", appName),
		BootstrapPeers:    peers,
		DatabaseEndpoints: endpoints,
		GatewayURL:        "",
		ConnectTimeout:    time.Second * 30,
		RetryAttempts:     3,
		RetryDelay:        time.Second * 5,
		QuietMode:         false,
		APIKey:            "",
		JWT:               "",
	}
}
