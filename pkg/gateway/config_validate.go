package gateway

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

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

		_, err := multiaddr.NewMultiaddr(peer)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid multiaddr: %v; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>", path, err))
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
