package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// ValidationError represents a single validation error with context.
type ValidationError struct {
	Path    string // e.g., "discovery.bootstrap_peers[0]" or "discovery.peers[0]"
	Message string // e.g., "invalid multiaddr"
	Hint    string // e.g., "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>"
}

func (e ValidationError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s; %s", e.Path, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// Validate performs comprehensive validation of the entire config.
// It aggregates all errors and returns them, allowing the caller to print all issues at once.
func (c *Config) Validate() []error {
	var errs []error

	// Validate node config
	errs = append(errs, c.validateNode()...)
	// Validate database config
	errs = append(errs, c.validateDatabase()...)
	// Validate discovery config
	errs = append(errs, c.validateDiscovery()...)
	// Validate security config
	errs = append(errs, c.validateSecurity()...)
	// Validate logging config
	errs = append(errs, c.validateLogging()...)
	// Cross-field validations
	errs = append(errs, c.validateCrossFields()...)

	return errs
}

func (c *Config) validateNode() []error {
	var errs []error
	nc := c.Node

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
				Hint:    "expected /ip{4,6}/.../ tcp/<port>",
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
		if err := validateDataDir(nc.DataDir); err != nil {
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

func (c *Config) validateDatabase() []error {
	var errs []error
	dc := c.Database

	// Validate data_dir
	if dc.DataDir == "" {
		errs = append(errs, ValidationError{
			Path:    "database.data_dir",
			Message: "must not be empty",
		})
	} else {
		if err := validateDataDir(dc.DataDir); err != nil {
			errs = append(errs, ValidationError{
				Path:    "database.data_dir",
				Message: err.Error(),
			})
		}
	}

	// Validate replication_factor
	if dc.ReplicationFactor < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.replication_factor",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.ReplicationFactor),
		})
	} else if dc.ReplicationFactor%2 == 0 {
		// Warn about even replication factor (Raft best practice: odd)
		// For now we log a note but don't error
		_ = fmt.Sprintf("note: database.replication_factor %d is even; Raft recommends odd numbers for quorum", dc.ReplicationFactor)
	}

	// Validate shard_count
	if dc.ShardCount < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.shard_count",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.ShardCount),
		})
	}

	// Validate max_database_size
	if dc.MaxDatabaseSize < 0 {
		errs = append(errs, ValidationError{
			Path:    "database.max_database_size",
			Message: fmt.Sprintf("must be >= 0; got %d", dc.MaxDatabaseSize),
		})
	}

	// Validate rqlite_port
	if dc.RQLitePort < 1 || dc.RQLitePort > 65535 {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_port",
			Message: fmt.Sprintf("must be between 1 and 65535; got %d", dc.RQLitePort),
		})
	}

	// Validate rqlite_raft_port
	if dc.RQLiteRaftPort < 1 || dc.RQLiteRaftPort > 65535 {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_raft_port",
			Message: fmt.Sprintf("must be between 1 and 65535; got %d", dc.RQLiteRaftPort),
		})
	}

	// Ports must differ
	if dc.RQLitePort == dc.RQLiteRaftPort {
		errs = append(errs, ValidationError{
			Path:    "database.rqlite_raft_port",
			Message: fmt.Sprintf("must differ from database.rqlite_port (%d)", dc.RQLitePort),
		})
	}

	// Validate rqlite_join_address format if provided (optional for all nodes)
	// The first node in a cluster won't have a join address; subsequent nodes will
	if dc.RQLiteJoinAddress != "" {
		if err := validateHostPort(dc.RQLiteJoinAddress); err != nil {
			errs = append(errs, ValidationError{
				Path:    "database.rqlite_join_address",
				Message: err.Error(),
				Hint:    "expected format: host:port",
			})
		}
	}

	// Validate cluster_sync_interval
	if dc.ClusterSyncInterval != 0 && dc.ClusterSyncInterval < 10*time.Second {
		errs = append(errs, ValidationError{
			Path:    "database.cluster_sync_interval",
			Message: fmt.Sprintf("must be >= 10s or 0 (for default); got %v", dc.ClusterSyncInterval),
			Hint:    "recommended: 30s",
		})
	}

	// Validate peer_inactivity_limit
	if dc.PeerInactivityLimit != 0 {
		if dc.PeerInactivityLimit < time.Hour {
			errs = append(errs, ValidationError{
				Path:    "database.peer_inactivity_limit",
				Message: fmt.Sprintf("must be >= 1h or 0 (for default); got %v", dc.PeerInactivityLimit),
				Hint:    "recommended: 24h",
			})
		} else if dc.PeerInactivityLimit > 7*24*time.Hour {
			errs = append(errs, ValidationError{
				Path:    "database.peer_inactivity_limit",
				Message: fmt.Sprintf("must be <= 7d; got %v", dc.PeerInactivityLimit),
				Hint:    "recommended: 24h",
			})
		}
	}

	// Validate min_cluster_size
	if dc.MinClusterSize < 1 {
		errs = append(errs, ValidationError{
			Path:    "database.min_cluster_size",
			Message: fmt.Sprintf("must be >= 1; got %d", dc.MinClusterSize),
		})
	}

	return errs
}

func (c *Config) validateDiscovery() []error {
	var errs []error
	disc := c.Discovery

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

	// Validate peer addresses (optional - can be empty for first node)
	// All nodes are unified, so peer addresses are optional

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
		tcpPortStr := extractTCPPort(peer)
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
		if err := validateHostOrHostPort(disc.HttpAdvAddress); err != nil {
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
		if err := validateHostOrHostPort(disc.RaftAdvAddress); err != nil {
			errs = append(errs, ValidationError{
				Path:    "discovery.raft_adv_address",
				Message: err.Error(),
				Hint:    "expected format: host or host:port",
			})
		}
	}

	return errs
}

func (c *Config) validateSecurity() []error {
	var errs []error
	sec := c.Security

	// Validate logging level
	if sec.EnableTLS {
		if sec.PrivateKeyFile == "" {
			errs = append(errs, ValidationError{
				Path:    "security.private_key_file",
				Message: "required when enable_tls is true",
			})
		} else {
			if err := validateFileReadable(sec.PrivateKeyFile); err != nil {
				errs = append(errs, ValidationError{
					Path:    "security.private_key_file",
					Message: err.Error(),
				})
			}
		}

		if sec.CertificateFile == "" {
			errs = append(errs, ValidationError{
				Path:    "security.certificate_file",
				Message: "required when enable_tls is true",
			})
		} else {
			if err := validateFileReadable(sec.CertificateFile); err != nil {
				errs = append(errs, ValidationError{
					Path:    "security.certificate_file",
					Message: err.Error(),
				})
			}
		}
	}

	return errs
}

func (c *Config) validateLogging() []error {
	var errs []error
	log := c.Logging

	// Validate level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[log.Level] {
		errs = append(errs, ValidationError{
			Path:    "logging.level",
			Message: fmt.Sprintf("invalid value %q", log.Level),
			Hint:    "allowed values: debug, info, warn, error",
		})
	}

	// Validate format
	validFormats := map[string]bool{"json": true, "console": true}
	if !validFormats[log.Format] {
		errs = append(errs, ValidationError{
			Path:    "logging.format",
			Message: fmt.Sprintf("invalid value %q", log.Format),
			Hint:    "allowed values: json, console",
		})
	}

	// Validate output_file
	if log.OutputFile != "" {
		dir := filepath.Dir(log.OutputFile)
		if dir != "" && dir != "." {
			if err := validateDirWritable(dir); err != nil {
				errs = append(errs, ValidationError{
					Path:    "logging.output_file",
					Message: fmt.Sprintf("parent directory not writable: %v", err),
				})
			}
		}
	}

	return errs
}

func (c *Config) validateCrossFields() []error {
	var errs []error
	return errs
}

// Helper validation functions

func validateDataDir(path string) error {
	if path == "" {
		return fmt.Errorf("must not be empty")
	}

	// Expand ~ to home directory
	expandedPath := os.ExpandEnv(path)
	if strings.HasPrefix(expandedPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %v", err)
		}
		expandedPath = filepath.Join(home, expandedPath[1:])
	}

	if info, err := os.Stat(expandedPath); err == nil {
		// Directory exists; check if it's a directory and writable
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory")
		}
		// Try to write a test file to check permissions
		testFile := filepath.Join(expandedPath, ".write_test")
		if err := os.WriteFile(testFile, []byte(""), 0644); err != nil {
			return fmt.Errorf("directory not writable: %v", err)
		}
		os.Remove(testFile)
	} else if os.IsNotExist(err) {
		// Directory doesn't exist; check if parent is writable
		parent := filepath.Dir(expandedPath)
		if parent == "" || parent == "." {
			parent = "."
		}
		// Allow parent not existing - it will be created at runtime
		if info, err := os.Stat(parent); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("parent directory not accessible: %v", err)
			}
			// Parent doesn't exist either - that's ok, will be created
		} else if !info.IsDir() {
			return fmt.Errorf("parent path is not a directory")
		} else {
			// Parent exists, check if writable
			if err := validateDirWritable(parent); err != nil {
				return fmt.Errorf("parent directory not writable: %v", err)
			}
		}
	} else {
		return fmt.Errorf("cannot access path: %v", err)
	}

	return nil
}

func validateDirWritable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access directory: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}

	// Try to write a test file
	testFile := filepath.Join(path, ".write_test")
	if err := os.WriteFile(testFile, []byte(""), 0644); err != nil {
		return fmt.Errorf("directory not writable: %v", err)
	}
	os.Remove(testFile)

	return nil
}

func validateFileReadable(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %v", err)
	}
	return nil
}

func validateHostPort(hostPort string) error {
	parts := strings.Split(hostPort, ":")
	if len(parts) != 2 {
		return fmt.Errorf("expected format host:port")
	}

	host := parts[0]
	port := parts[1]

	if host == "" {
		return fmt.Errorf("host must not be empty")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("port must be a number between 1 and 65535; got %q", port)
	}

	return nil
}

func validateHostOrHostPort(addr string) error {
	// Try to parse as host:port first
	if strings.Contains(addr, ":") {
		return validateHostPort(addr)
	}

	// Otherwise just check if it's a valid hostname/IP
	if addr == "" {
		return fmt.Errorf("address must not be empty")
	}

	return nil
}

func extractTCPPort(multiaddrStr string) string {
	// Look for the /tcp/ protocol code
	parts := strings.Split(multiaddrStr, "/")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "tcp" {
			// The port is the next part
			if i+1 < len(parts) {
				return parts[i+1]
			}
			break
		}
	}
	return ""
}
