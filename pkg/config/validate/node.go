package validate

import (
	"fmt"
	"net"

	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// NodeConfig represents the node configuration for validation purposes.
type NodeConfig struct {
	ID              string
	ListenAddresses []string
	DataDir         string
	MaxConnections  int
}

// ValidateNode performs validation of the node configuration.
func ValidateNode(nc NodeConfig) []error {
	var errs []error

	// Validate node ID (required for RQLite cluster membership)
	if nc.ID == "" {
		errs = append(errs, ValidationError{
			Path:    "node.id",
			Message: "must not be empty (required for cluster membership)",
			Hint:    "will be auto-generated if empty, but explicit ID recommended",
		})
	}

	// Validate listen_addresses
	if len(nc.ListenAddresses) == 0 {
		errs = append(errs, ValidationError{
			Path:    "node.listen_addresses",
			Message: "must not be empty",
		})
	}

	seen := make(map[string]bool)
	for i, addr := range nc.ListenAddresses {
		path := fmt.Sprintf("node.listen_addresses[%d]", i)

		// Parse as multiaddr
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: fmt.Sprintf("invalid multiaddr: %v", err),
				Hint:    "expected /ip{4,6}/.../tcp/<port>",
			})
			continue
		}

		// Check for TCP and valid port
		tcpAddr, err := manet.ToNetAddr(ma)
		if err != nil {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: fmt.Sprintf("cannot convert multiaddr to network address: %v", err),
				Hint:    "ensure multiaddr contains /tcp/<port>",
			})
			continue
		}

		tcpPort := tcpAddr.(*net.TCPAddr).Port
		if tcpPort < 1 || tcpPort > 65535 {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: fmt.Sprintf("invalid TCP port %d", tcpPort),
				Hint:    "port must be between 1 and 65535",
			})
		}

		if seen[addr] {
			errs = append(errs, ValidationError{
				Path:    path,
				Message: "duplicate listen address",
			})
		}
		seen[addr] = true
	}

	// Validate data_dir
	if nc.DataDir == "" {
		errs = append(errs, ValidationError{
			Path:    "node.data_dir",
			Message: "must not be empty",
		})
	} else {
		if err := ValidateDataDir(nc.DataDir); err != nil {
			errs = append(errs, ValidationError{
				Path:    "node.data_dir",
				Message: err.Error(),
			})
		}
	}

	// Validate max_connections
	if nc.MaxConnections <= 0 {
		errs = append(errs, ValidationError{
			Path:    "node.max_connections",
			Message: fmt.Sprintf("must be > 0; got %d", nc.MaxConnections),
		})
	}

	return errs
}
