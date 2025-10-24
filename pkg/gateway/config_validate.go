package gateway

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
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
			errs = append(errs, fmt.Errorf("gateway.listen_addr: %v", err))
		}
	}

	// Validate client_namespace
	if c.ClientNamespace == "" {
		errs = append(errs, fmt.Errorf("gateway.client_namespace: must not be empty"))
	}

	// Validate bootstrap_peers if provided
	seenPeers := make(map[string]bool)
	for i, peer := range c.BootstrapPeers {
		path := fmt.Sprintf("gateway.bootstrap_peers[%d]", i)

		ma, err := multiaddr.NewMultiaddr(peer)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid multiaddr: %v; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>", path, err))
			continue
		}

		// Check for /p2p/ component
		if !strings.Contains(peer, "/p2p/") {
			errs = append(errs, fmt.Errorf("%s: missing /p2p/<peerID> component; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>", path))
		}

		// Try to extract TCP addr to validate port
		tcpAddr, err := manet.ToNetAddr(ma)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot convert to network address: %v", path, err))
			continue
		}

		tcpPort := tcpAddr.(*net.TCPAddr).Port
		if tcpPort < 1 || tcpPort > 65535 {
			errs = append(errs, fmt.Errorf("%s: invalid TCP port %d; port must be between 1 and 65535", path, tcpPort))
		}

		if seenPeers[peer] {
			errs = append(errs, fmt.Errorf("%s: duplicate bootstrap peer", path))
		}
		seenPeers[peer] = true
	}

	// Validate rqlite_dsn if provided
	if c.RQLiteDSN != "" {
		if err := validateRQLiteDSN(c.RQLiteDSN); err != nil {
			errs = append(errs, fmt.Errorf("gateway.rqlite_dsn: %v", err))
		}
	}

	return errs
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

	// Allow empty host (for wildcard binds like :6001)
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
		return fmt.Errorf("invalid URL: %v", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https; got %q", u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("host must not be empty")
	}

	return nil
}
