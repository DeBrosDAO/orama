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
	return missing, fmt.Errorf("%s", errMsg)
}

// PortChecker validates that required ports are available
type PortChecker struct {
	ports []int
}

// RequiredPorts defines all ports needed for dev environment
// Computed from DefaultTopology
var RequiredPorts = DefaultTopology().AllPorts()

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
	return unavailable, fmt.Errorf("%s", errMsg)
}

// isPortAvailable checks if a TCP port is available for binding
func isPortAvailable(port int) bool {
	// Port 0 is reserved and means "assign any available port"
	if port == 0 {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// PortMap provides a human-readable mapping of ports to services
func PortMap() map[int]string {
	return DefaultTopology().PortMap()
}
