package gateway

import (
	"fmt"
	"net/netip"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// libp2pListenAddrs is where this gateway's libp2p host accepts connections
// (client.ClientConfig.ListenAddrs).
//
// Only a gateway serving a named namespace needs a listener: its peer discovery
// (peer_discovery.go) registers the address it listens on, and the namespace's
// other gateways dial that address. Every other gateway only dials its
// bootstrap peers and gets none. The same predicate decides whether peer
// discovery runs at all (New).
//
// The address is this node's WireGuard IP, read from rqlite_dsn. The spawner
// points every gateway at the rqlite on its own node (tenantRQLiteURL for a
// namespace, rqlite.IndexEndpoint for the index), and rqlited binds only this
// node's WireGuard address — so the DSN's host is that address, not a guess.
// The port is left to the OS; peer discovery reads it back from the host.
func libp2pListenAddrs(cfg *Config) ([]string, error) {
	if !servesNamedNamespace(cfg.ClientNamespace) {
		return nil, nil
	}
	ep, err := rqlite.EndpointFromDSN(cfg.RQLiteDSN, cfg.RQLiteUsername, cfg.RQLitePassword)
	if err != nil {
		return nil, fmt.Errorf("namespace peer discovery listens on this node's WireGuard IP, read from rqlite_dsn: %w", err)
	}
	ip, err := netip.ParseAddr(ep.Host)
	if err != nil {
		return nil, fmt.Errorf("namespace peer discovery listens on this node's WireGuard IP, read from rqlite_dsn, "+
			"but its host %q is not an IP address", ep.Host)
	}
	if !constants.WireGuardOverlay().Contains(ip) {
		return nil, fmt.Errorf("namespace peer discovery listens on this node's WireGuard IP, read from rqlite_dsn, "+
			"but its host %s is not on the WireGuard overlay %s", ip, constants.WireGuardSubnet)
	}
	return []string{fmt.Sprintf("/ip4/%s/tcp/0", ip)}, nil
}
