package validate

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/multiformats/go-multiaddr"
)

// DiscoveryConfig represents the discovery configuration for validation purposes.
type DiscoveryConfig struct {
	BootstrapPeers    []string
	DiscoveryInterval time.Duration
	BootstrapPort     int
	HttpAdvAddress    string
	RaftAdvAddress    string
}

// ValidateDiscovery performs validation of the discovery configuration.
func ValidateDiscovery(disc DiscoveryConfig) []error {
	var errs []error

	// Validate discovery_interval
	if disc.DiscoveryInterval <= 0 {
		errs = append(errs, ValidationError{
			Path:    "discovery.discovery_interval",
			Message: fmt.Sprintf("must be > 0; got %v", disc.DiscoveryInterval),
		})
	}

	// Validate peer discovery port
	if disc.BootstrapPort < 1 || disc.BootstrapPort > 65535 {
		errs = append(errs, ValidationError{
			Path:    "discovery.bootstrap_port",
			Message: fmt.Sprintf("must be between 1 and 65535; got %d", disc.BootstrapPort),
		})
	}

	// Validate peer addresses (optional - all nodes are unified peers now)
	// Validate each peer multiaddr
	seenPeers := make(map[string]bool)
	for i, peer := range disc.BootstrapPeers {
		path := fmt.Sprintf("discovery.bootstrap_peers[%d]", i)

		_, err := multiaddr.NewMultiaddr(peer)
		if err != nil {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: fmt.Sprintf("invalid multiaddr: %v", err),
				Hint:    "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>",
			})
			continue
		}

		// Check for /p2p/ component
		if !strings.Contains(peer, "/p2p/") {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: "missing /p2p/<peerID> component",
				Hint:    "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>",
			})
		}

		// Extract TCP port by parsing the multiaddr string directly
		// Look for /tcp/ in the peer string
		tcpPortStr := ExtractTCPPort(peer)
		if tcpPortStr == "" {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: "missing /tcp/<port> component",
				Hint:    "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>",
			})
			continue
		}

		tcpPort, err := strconv.Atoi(tcpPortStr)
		if err != nil || tcpPort < 1 || tcpPort > 65535 {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: fmt.Sprintf("invalid TCP port %s", tcpPortStr),
				Hint:    "port must be between 1 and 65535",
			})
		}

		if seenPeers[peer] {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: "duplicate peer",
			})
		}
		seenPeers[peer] = true
	}

	// Validate http_adv_address (required for cluster discovery)
	if disc.HttpAdvAddress == "" {
		errs = append(errs, ValidationError{
			Path:    "discovery.http_adv_address",
			Message: "required for RQLite cluster discovery",
			Hint:    "set to your public HTTP address (e.g., 51.83.128.181:5001)",
		})
	} else {
		if err := ValidateHostOrHostPort(disc.HttpAdvAddress); err != nil {
			errs = append(errs, ValidationError{
				Path:    "discovery.http_adv_address",
				Message: err.Error(),
				Hint:    "expected format: host or host:port",
			})
		}
	}

	// Validate raft_adv_address (required for cluster discovery)
	if disc.RaftAdvAddress == "" {
		errs = append(errs, ValidationError{
			Path:    "discovery.raft_adv_address",
			Message: "required for RQLite cluster discovery",
			Hint:    "set to your public Raft address (e.g., 51.83.128.181:7001)",
		})
	} else {
		if err := ValidateHostOrHostPort(disc.RaftAdvAddress); err != nil {
			errs = append(errs, ValidationError{
				Path:    "discovery.raft_adv_address",
				Message: err.Error(),
				Hint:    "expected format: host or host:port",
			})
		}
	}

	return errs
}
