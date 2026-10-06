package constants

import (
	"net"
	"strconv"
)

// Tor is the node's only anonymity backend. Every node runs a client-only Tor
// daemon (orama-namespace-tor@index) whose SOCKS port serves the gateway's
// /v1/proxy/anon and /v1/proxy/tunnel and the anon_fetch WASM host function.
// The installer writes the port into the torrc and the gateway dials it, so
// both read it from here.
const (
	// TorSOCKSHost is the loopback address the SOCKS port binds. It is never
	// reachable off-host.
	TorSOCKSHost = "127.0.0.1"
	// TorSOCKSPort is Tor's conventional client SOCKS port. It is an edge
	// port, outside the 10100 index block.
	TorSOCKSPort = 9050

	// TorConfigPath is the Orama-written torrc the Tor unit runs with. The
	// distro's /etc/tor/torrc belongs to the masked tor@default instance and
	// is not used.
	TorConfigPath = "/etc/orama/tor/torrc"
)

// TorSOCKSAddr is the host:port of the node's Tor SOCKS5 listener.
func TorSOCKSAddr() string {
	return net.JoinHostPort(TorSOCKSHost, strconv.Itoa(TorSOCKSPort))
}
