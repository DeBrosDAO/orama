package client

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// ClientConfig represents configuration for network clients
type ClientConfig struct {
	AppName           string        `json:"app_name"`
	DatabaseName      string        `json:"database_name"`
	BootstrapPeers    []string      `json:"peers"`
	DatabaseEndpoints []string      `json:"database_endpoints"`
	GatewayURL        string        `json:"gateway_url"` // Gateway URL for HTTP API access
	ConnectTimeout    time.Duration `json:"connect_timeout"`
	RetryAttempts     int           `json:"retry_attempts"`
	RetryDelay        time.Duration `json:"retry_delay"`
	QuietMode         bool          `json:"quiet_mode"`    // Suppress debug/info logs
	APIKey            string        `json:"api_key"`       // API key for gateway auth
	JWT               string        `json:"jwt"`           // Optional JWT bearer token
	IdentityPath      string        `json:"identity_path"` // Path to persistent LibP2P identity key file
	PubSubSocket      string        `json:"pubsub_socket"` // unix socket of the node's app GossipSub API (orama-namespace-pubsub@index)

	// IPFSClusterAPIPassword authenticates the network status's read of this
	// node's IPFS Cluster REST API (ipfs.ClusterRESTPassword). Never
	// serialised.
	IPFSClusterAPIPassword string `json:"-"`

	// ListenAddrs are the libp2p multiaddrs the client's host accepts
	// connections on, e.g. "/ip4/10.0.0.3/tcp/0" for a node's WireGuard
	// address. Empty means no listener: the client only dials its bootstrap
	// peers, which needs none. An unspecified address (0.0.0.0, ::) is
	// refused — it is every interface, the public one included.
	ListenAddrs []string `json:"listen_addrs"`
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
		PubSubSocket:      pubsub.DefaultSocketPath,
	}
}
