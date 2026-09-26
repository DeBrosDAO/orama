package main

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/namespace"
)

// loopbackHost is where the index gateway serves this host: Caddy
// reverse-proxies to it, Caddy's DNS-01 provider calls it, and the CLI reads it
// (constants.LocalGatewayURL).
const loopbackHost = "127.0.0.1"

// listenAddrs is every address this gateway binds: listen_addr, which the
// spawner sets to this node's WireGuard address, and for the index gateway
// loopback on the same port as well.
//
// The index gateway used to bind every interface (`:port`), the public one
// included, with only the firewall in front of it. Nothing needs it there:
// the internet reaches it through Caddy on loopback, and other nodes over the
// mesh. An index listen_addr that is not an overlay address — a YAML from
// before this change, still ":10104" — is refused rather than bound; orama-node
// rewrites it on its next reconcile and restarts the gateway.
func listenAddrs(clientNamespace, listenAddr string) ([]string, error) {
	if !indexListener(clientNamespace) {
		return []string{listenAddr}, nil
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen_addr %q: %w", listenAddr, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !constants.WireGuardOverlay().Contains(ip) {
		return nil, fmt.Errorf("the index gateway binds this node's WireGuard address and loopback, and listen_addr %q "+
			"is not an address on %s; orama-node rewrites it on its next reconcile", listenAddr, constants.WireGuardSubnet)
	}
	return []string{listenAddr, net.JoinHostPort(loopbackHost, port)}, nil
}

// indexListener is the cluster gateway, including a process whose
// client_namespace is still the pre-rename "default" or empty. Those bind
// loopback as well as the overlay, which is where Caddy and the CLI reach them.
func indexListener(clientNamespace string) bool {
	switch strings.TrimSpace(clientNamespace) {
	case "", "default":
		return true
	default:
		return namespace.IsIndexGateway(clientNamespace)
	}
}
