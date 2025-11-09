package development

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// Dependency represents an external binary dependency
type Dependency struct {
	Name        string
	Command     string
	MinVersion  string // Optional: if set, try to check version
	InstallHint string
}

// DependencyChecker handles dependency validation
type DependencyChecker struct {
	dependencies []Dependency
}

// NewDependencyChecker creates a new dependency checker
func NewDependencyChecker() *DependencyChecker {
	return &DependencyChecker{
		dependencies: []Dependency{
			{
				Name:        "IPFS",
				Command:     "ipfs",
				MinVersion:  "0.25.0",
				InstallHint: "Install with: brew install ipfs (macOS) or https://docs.ipfs.tech/install/command-line/",
			},
			{
				Name:        "IPFS Cluster Service",
				Command:     "ipfs-cluster-service",
				MinVersion:  "1.0.0",
				InstallHint: "Install with: go install github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest",
			},
			{
				Name:        "RQLite",
				Command:     "rqlited",
				InstallHint: "Install with: brew install rqlite (macOS) or https://github.com/rqlite/rqlite/releases",
			},
			{
				Name:        "Olric Server",
				Command:     "olric-server",
				InstallHint: "Install with: go install github.com/olric-data/olric/cmd/olric-server@v0.7.0",
			},
			{
				Name:        "npm (for Anyone)",
				Command:     "npm",
				InstallHint: "Install Node.js with: brew install node (macOS) or https://nodejs.org/",
			},
			{
				Name:        "OpenSSL",
				Command:     "openssl",
				InstallHint: "Install with: brew install openssl (macOS) - usually pre-installed on Linux",
			},
		},
	}
}

// CheckAll performs all dependency checks and returns a report
func (dc *DependencyChecker) CheckAll() ([]string, error) {
	var missing []string
	var hints []string

	for _, dep := range dc.dependencies {
		if _, err := exec.LookPath(dep.Command); err != nil {
			missing = append(missing, dep.Name)
			hints = append(hints, fmt.Sprintf("  %s: %s", dep.Name, dep.InstallHint))
		}
	}

	if len(missing) == 0 {
		return nil, nil // All OK
	}

	errMsg := fmt.Sprintf("Missing %d required dependencies:\n%s\n\nInstall them with:\n%s",
		len(missing), strings.Join(missing, ", "), strings.Join(hints, "\n"))
	return missing, fmt.Errorf(errMsg)
}

// PortChecker validates that required ports are available
type PortChecker struct {
	ports []int
}

// RequiredPorts defines all ports needed for dev environment
var RequiredPorts = []int{
	// LibP2P
	4001, 4002, 4003,
	// IPFS API
	4501, 4502, 4503,
	// RQLite HTTP
	5001, 5002, 5003,
	// RQLite Raft
	7001, 7002, 7003,
	// Gateway
	6001,
	// Olric
	3320, 3322,
	// Anon SOCKS
	9050,
	// IPFS Cluster
	9094, 9104, 9114,
	// IPFS Gateway
	8080, 8081, 8082,
}

// NewPortChecker creates a new port checker with required ports
func NewPortChecker() *PortChecker {
	return &PortChecker{
		ports: RequiredPorts,
	}
}

// CheckAll verifies all required ports are available
func (pc *PortChecker) CheckAll() ([]int, error) {
	var unavailable []int

	for _, port := range pc.ports {
		if !isPortAvailable(port) {
			unavailable = append(unavailable, port)
		}
	}

	if len(unavailable) == 0 {
		return nil, nil // All OK
	}

	errMsg := fmt.Sprintf("The following ports are unavailable: %v\n\nFree them or stop conflicting services and try again",
		unavailable)
	return unavailable, fmt.Errorf(errMsg)
}

// isPortAvailable checks if a TCP port is available for binding
func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// PortMap provides a human-readable mapping of ports to services
func PortMap() map[int]string {
	return map[int]string{
		4001: "Bootstrap P2P",
		4002: "Node2 P2P",
		4003: "Node3 P2P",
		4501: "Bootstrap IPFS API",
		4502: "Node2 IPFS API",
		4503: "Node3 IPFS API",
		5001: "Bootstrap RQLite HTTP",
		5002: "Node2 RQLite HTTP",
		5003: "Node3 RQLite HTTP",
		7001: "Bootstrap RQLite Raft",
		7002: "Node2 RQLite Raft",
		7003: "Node3 RQLite Raft",
		6001: "Gateway",
		3320: "Olric HTTP API",
		3322: "Olric Memberlist",
		9050: "Anon SOCKS Proxy",
		9094: "Bootstrap IPFS Cluster",
		9104: "Node2 IPFS Cluster",
		9114: "Node3 IPFS Cluster",
		8080: "Bootstrap IPFS Gateway",
		8081: "Node2 IPFS Gateway",
		8082: "Node3 IPFS Gateway",
	}
}
