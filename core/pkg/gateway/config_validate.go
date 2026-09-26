package gateway

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// ValidateConfig performs comprehensive validation of gateway configuration.
// It returns aggregated errors, allowing the caller to print all issues at once.
func (c *Config) ValidateConfig() []error {
	var errs []error

	// Validate listen_addr
	if c.ListenAddr == "" {
		errs = append(errs, fmt.Errorf("gateway.listen_addr: must not be empty"))
	} else {
		if err := validateListenAddr(c.ListenAddr); err != nil {
			errs = append(errs, fmt.Errorf("gateway.listen_addr: %w", err))
		}
	}

	// Validate client_namespace
	if c.ClientNamespace == "" {
		errs = append(errs, fmt.Errorf("gateway.client_namespace: must not be empty"))
	}

	// The base domain routes deployments and namespace gateways and decides
	// which hosts get a certificate. It used to default to a domain belonging
	// to a cluster that no longer exists, so a gateway missing it routed
	// nothing and answered every TLS check for its real domain "not allowed".
	if err := validateBaseDomain(c.BaseDomain); err != nil {
		errs = append(errs, fmt.Errorf("gateway.domain_name: %w", err))
	}

	// state_dir is where this gateway's signing keys live. With none, it has
	// nowhere it may write them (the unit is ProtectSystem=strict), so it could
	// start serving /health while every auth route is missing.
	if err := validateStateDir(c.StateDir); err != nil {
		errs = append(errs, fmt.Errorf("gateway.state_dir: %w", err))
	}

	// The node's peer id is how this gateway finds itself in the node
	// registry. Without it SQLite home-node assignment and deployment
	// placement match no node, and the index gateway's host TURN and leader
	// locality reconcilers act for no node — all of it silently.
	if err := validateNodePeerID(c.NodePeerID); err != nil {
		errs = append(errs, fmt.Errorf("gateway.node_peer_id: %w", err))
	}

	// Validate bootstrap_peers if provided
	seenPeers := make(map[string]bool)
	for i, peer := range c.BootstrapPeers {
		path := fmt.Sprintf("gateway.bootstrap_peers[%d]", i)

		_, err := multiaddr.NewMultiaddr(peer)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid multiaddr: %w", path, err))
			continue
		}

		// Check for /p2p/ component
		if !strings.Contains(peer, "/p2p/") {
			errs = append(errs, fmt.Errorf("%s: missing /p2p/<peerID> component; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>", path))
		}

		// Extract TCP port by parsing the multiaddr string directly
		tcpPortStr := extractTCPPort(peer)
		if tcpPortStr == "" {
			errs = append(errs, fmt.Errorf("%s: missing /tcp/<port> component; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>", path))
			continue
		}

		tcpPort, err := strconv.Atoi(tcpPortStr)
		if err != nil || tcpPort < 1 || tcpPort > 65535 {
			errs = append(errs, fmt.Errorf("%s: invalid TCP port %s; port must be between 1 and 65535", path, tcpPortStr))
		}

		if seenPeers[peer] {
			errs = append(errs, fmt.Errorf("%s: duplicate bootstrap peer", path))
		}
		seenPeers[peer] = true
	}

	// rqlite_dsn is required: rqlited binds only its WireGuard address, so
	// there is no default this gateway could guess.
	if c.RQLiteDSN == "" {
		errs = append(errs, fmt.Errorf("gateway.rqlite_dsn: must not be empty — set it to the rqlite this gateway serves (http://<wireguard-ip>:<port>)"))
	} else if err := validateRQLiteDSN(c.RQLiteDSN); err != nil {
		errs = append(errs, fmt.Errorf("gateway.rqlite_dsn: %w", err))
	}

	// Validate WebRTC configuration
	if c.WebRTCEnabled {
		if c.SFUPort <= 0 || c.SFUPort > 65535 {
			errs = append(errs, fmt.Errorf("gateway.sfu_port: must be between 1 and 65535 when webrtc is enabled"))
		}
		if c.TURNSecret == "" {
			errs = append(errs, fmt.Errorf("gateway.turn_secret: must not be empty when webrtc is enabled"))
		}
	}

	return errs
}

// baseDomainPattern is a lowercase DNS name of at least two labels. The base
// domain is matched as a suffix — case-sensitively — to decide which hosts get
// a certificate and which origins CORS allows, so "com" or "." would admit
// nearly anything and "Example.com" would match nothing.
var baseDomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// maxDomainLength is the longest DNS name, in its text form.
const maxDomainLength = 253

func validateBaseDomain(domain string) error {
	if strings.TrimSpace(domain) == "" {
		return fmt.Errorf("must not be empty — set it to the cluster's base domain " +
			"(the namespace spawner writes it from node.yaml http_gateway.base_domain)")
	}
	if len(domain) > maxDomainLength || !baseDomainPattern.MatchString(domain) {
		return fmt.Errorf("%q is not a lowercase domain name of at least two labels", domain)
	}
	return nil
}

// validateNodePeerID requires the libp2p peer id of the node this gateway
// runs on.
func validateNodePeerID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("must not be empty — the gateway reads it from <oramaDir>/data/identity.key, " +
			"the orama directory being the one cluster_secret_path is in; check that cluster_secret_path is set " +
			"and the node's identity key exists")
	}
	if _, err := peer.Decode(id); err != nil {
		return fmt.Errorf("%q is not a libp2p peer id: %w", id, err)
	}
	return nil
}

// validateStateDir requires an absolute state directory. A relative one would
// resolve against the unit's WorkingDirectory, outside its writable paths.
func validateStateDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("must not be empty — set it to this gateway's private directory (<orama-dir>/data/namespaces/<ns>/gateway; the namespace spawner writes it)")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("must be an absolute path, got %q", dir)
	}
	return nil
}

// validateListenAddr checks if a listen address is valid (host:port format)
func validateListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid format; expected host:port")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("port must be a number between 1 and 65535; got %q", port)
	}

	// Allow empty host (for wildcard binds like :10104)
	if host != "" && net.ParseIP(host) == nil {
		// Try as hostname (may fail later during bind, but basic validation)
		_, err := net.LookupHost(host)
		if err != nil {
			// Not an IP; assume it's a valid hostname for now
		}
	}

	return nil
}

// validateRQLiteDSN checks if an RQLite DSN is a valid URL
func validateRQLiteDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https; got %q", u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("host must not be empty")
	}

	return nil
}

// extractTCPPort extracts the TCP port from a multiaddr string.
// It assumes the multiaddr is in the format /ip{4,6}/.../tcp/<port>/p2p/<peerID>.
func extractTCPPort(multiaddrStr string) string {
	// Find the last /tcp/ component
	lastTCPIndex := strings.LastIndex(multiaddrStr, "/tcp/")
	if lastTCPIndex == -1 {
		return ""
	}

	// Extract the port part after /tcp/
	portPart := multiaddrStr[lastTCPIndex+len("/tcp/"):]

	// Find the first / component after the port part
	firstSlashIndex := strings.Index(portPart, "/")
	if firstSlashIndex == -1 {
		return portPart
	}

	return portPart[:firstSlashIndex]
}
